// Package cli implements the airs command line.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"airs/internal/api"
	"airs/internal/client"
	"airs/internal/paths"
	"airs/internal/qr"
	"airs/internal/render"
	"airs/internal/state"
)

const usage = `airs — read your markdown on other devices

  airs <file|folder> [--any]   share a file or folder (prints link + QR code)
  airs list                    what's shared
  airs remove <id|name>        stop sharing
  airs qr [id|name]            show a QR code (no argument: the index page)
  airs send <id|name>          AirDrop the link
  airs trust [label]           trust the current network (e.g. your home Wi-Fi)
  airs untrust                 stop trusting the current network
  airs stop                    stop the server (your list is kept)

Items are only shared on trusted networks. --any shares an item on any network;
any_network = true in ~/.config/airs/config.toml turns the check off.
`

type cmd struct {
	out, err io.Writer
	dirs     paths.Dirs
}

// errUsage marks errors that should be followed by the usage hint.
type errUsage string

func (e errUsage) Error() string { return string(e) }

// Run executes the command line and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	dirs, err := paths.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, "airs:", err)
		return 1
	}
	c := &cmd{out: stdout, err: stderr, dirs: dirs}
	if err := c.dispatch(args); err != nil {
		var amb *client.AmbiguousError
		switch {
		case errors.As(err, &amb):
			fmt.Fprintf(stderr, "airs: %d items match; use the id:\n", len(amb.Matches))
			for _, m := range amb.Matches {
				fmt.Fprintf(stderr, "  %d  %s\n", m.ID, tildePath(m.Path))
			}
		default:
			fmt.Fprintln(stderr, "airs:", err)
			var u errUsage
			if errors.As(err, &u) {
				fmt.Fprintln(stderr, "run 'airs help' for usage")
			}
		}
		return 1
	}
	return 0
}

func (c *cmd) dispatch(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(c.out, usage)
		return nil
	}
	rest := args[1:]
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(c.out, usage)
		return nil
	case "list", "ls":
		return c.list()
	case "remove", "rm":
		q, err := oneArg("remove <id|name>", rest)
		if err != nil {
			return err
		}
		return c.remove(q)
	case "qr":
		if len(rest) > 1 {
			return errUsage("usage: airs qr [id|name]")
		}
		q := ""
		if len(rest) == 1 {
			q = rest[0]
		}
		return c.qr(q)
	case "send":
		q, err := oneArg("send <id|name>", rest)
		if err != nil {
			return err
		}
		return c.send(q)
	case "trust":
		if len(rest) > 1 {
			return errUsage("usage: airs trust [label]")
		}
		label := ""
		if len(rest) == 1 {
			label = rest[0]
		}
		return c.trust(label)
	case "untrust":
		return c.untrust()
	case "stop":
		return c.stop()
	case "__serve":
		return errUsage("__serve is internal")
	}
	return c.add(args)
}

func oneArg(u string, rest []string) (string, error) {
	if len(rest) != 1 {
		return "", errUsage("usage: airs " + u)
	}
	return rest[0], nil
}

func (c *cmd) connect() (*client.Client, error) { return client.Connect(c.dirs, true) }

// connectIfShared starts the daemon only when there is something to restore.
func (c *cmd) connectIfShared() (*client.Client, error) {
	if cl, err := client.Connect(c.dirs, false); err == nil {
		return cl, nil
	}
	st, err := state.Open(c.dirs.StateFile())
	if err != nil {
		return nil, err
	}
	if len(st.Items()) == 0 {
		return nil, errNothingShared
	}
	return c.connect()
}

var errNothingShared = errors.New("nothing is being shared")

func (c *cmd) add(args []string) error {
	var target string
	any := false
	for _, a := range args {
		switch {
		case a == "--any":
			any = true
		case len(a) > 1 && a[0] == '-':
			return errUsage("unknown flag " + a)
		case target != "":
			return errUsage("usage: airs <file|folder> [--any]")
		default:
			target = a
		}
	}
	if target == "" {
		return errUsage("usage: airs <file|folder> [--any]")
	}
	abs, err := canonical(target)
	if err != nil {
		return fmt.Errorf("no such file or folder: %s", target)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("no such file or folder: %s", target)
	}
	if !fi.IsDir() {
		if _, ok := render.ForPath(abs); !ok {
			return errors.New("airs only shows markdown for now (.md, .markdown)")
		}
	}
	cl, err := c.connect()
	if err != nil {
		return err
	}
	res, err := cl.Add(api.AddRequest{Path: abs, Any: any})
	if err != nil {
		return err
	}
	verb := "serving"
	if res.Existed {
		verb = "already serving"
	}
	fmt.Fprintf(c.out, "%s %s → %s\n", verb, res.Item.Name, res.Item.URL)
	if !res.NetworkTrusted && !res.Item.AnyNetwork {
		st, _ := cl.Status()
		if !st.AnyNetwork {
			printUntrusted(c.out, res.Item, target)
			return nil
		}
	}
	return qr.Terminal(c.out, res.Item.URL)
}

func (c *cmd) list() error {
	cl, err := c.connectIfShared()
	if errors.Is(err, errNothingShared) {
		fmt.Fprintln(c.out, "nothing is being shared")
		return nil
	}
	if err != nil {
		return err
	}
	items, err := cl.List()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(c.out, "nothing is being shared")
		return nil
	}
	printList(c.out, items)
	return nil
}

// query turns a remove/qr/send argument into what the daemon matches: an id or
// name as typed, or the absolute path when it names something on disk.
func query(q string) string {
	if _, err := strconv.Atoi(q); err == nil {
		return q
	}
	if abs, err := canonical(q); err == nil {
		return abs
	}
	return q
}

// canonical is the absolute, symlink-resolved path of an existing file, so the
// same file reached two ways (e.g. /tmp vs /private/tmp) matches.
func canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func (c *cmd) remove(q string) error {
	cl, err := c.connectIfShared()
	if err != nil {
		return err
	}
	it, err := cl.Remove(query(q))
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "stopped sharing %s\n", it.Name)
	return nil
}

func (c *cmd) qr(q string) error {
	cl, err := c.connectIfShared()
	if err != nil {
		return err
	}
	var u string
	if q == "" {
		st, err := cl.Status()
		if err != nil {
			return err
		}
		u = st.BaseURL + "/"
	} else {
		it, err := cl.Find(query(q))
		if err != nil {
			return err
		}
		u = it.URL
	}
	fmt.Fprintln(c.out, u)
	return qr.Terminal(c.out, u)
}

func (c *cmd) trust(label string) error {
	cl, err := c.connect()
	if err != nil {
		return err
	}
	st, err := cl.Trust(label)
	if err != nil {
		return err
	}
	name := "this network"
	if st.NetworkLabel != "" {
		name = fmt.Sprintf("this network (%q)", st.NetworkLabel)
	}
	fmt.Fprintf(c.out, "✓ trusted %s — shared items are visible to other devices here\n", name)
	return nil
}

func (c *cmd) untrust() error {
	cl, err := c.connect()
	if err != nil {
		return err
	}
	if _, err := cl.Untrust(); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "this network is no longer trusted; shared items are only visible on this Mac")
	return nil
}

func (c *cmd) stop() error {
	cl, err := client.Connect(c.dirs, false)
	if err != nil {
		return err
	}
	if err := cl.Stop(); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "airs stopped (your list is kept for next time)")
	return nil
}
