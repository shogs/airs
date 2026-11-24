// Package cli implements the airs command line.
package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"airs/internal/api"
	"airs/internal/client"
	"airs/internal/paths"
	"airs/internal/project"
	"airs/internal/qr"
	"airs/internal/render"
	"airs/internal/state"
)

const usage = `airs — read your markdown on other devices

  airs <file|folder> [--any] [--no-notes] [--project <name>] [--notes-dir <dir>]
                               share a file or folder (prints link + QR code)
  airs add <file|folder> …     same as above
  airs list                    what's shared
  airs remove <id|name>        stop sharing
  airs remove --all [--yes]    stop sharing everything (asks first; notes files are kept)
  airs notes <file|id|name>    show a document's notes and where they're kept
  airs qr [id|name]            show a QR code (no argument: the index page)
  airs send <id|name>          AirDrop the link
  airs trust [label]           trust the current network (e.g. your home Wi-Fi)
  airs untrust                 stop trusting the current network
  airs stop                    stop the server (your list is kept)

Items are only shared on trusted networks. --any shares an item on any network;
any_network = true in ~/.config/airs/config.toml turns the check off.
Readers can add notes, saved beside the document (README.md → README.notes.md).
--no-notes turns that off for one item; notes = false in the config turns it off everywhere.
--notes-dir <dir> keeps a file's notes in <dir> instead.

Links are http://<mac>:<port>/<project>/<name>. The project is the git repository
you run airs in (or the folder's name outside one); --project <name> sets it and
moves a share that's already there.

For scripts and agents:
  --json   print one JSON value (add, list, qr, notes, remove); errors as {"error": …}
  --qr     print the QR code even when output isn't a terminal (with --json: a "qr" field)
`

type cmd struct {
	out, err io.Writer
	dirs     paths.Dirs
	json     bool // --json: print one JSON value on stdout
}

// stdin is where questions are answered; tests replace it.
var stdin io.Reader = os.Stdin

// isTerminal reports whether w is a terminal, where the QR code is worth printing.
var isTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// jsonCommands are the commands that can print JSON.
var jsonCommands = map[string]bool{"add": true, "list": true, "ls": true, "qr": true, "notes": true, "remove": true, "rm": true}

func (c *cmd) printJSON(v any) error {
	enc := json.NewEncoder(c.out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
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
	var rest []string
	for _, a := range args {
		if a == "--json" {
			c.json = true
		} else {
			rest = append(rest, a)
		}
	}
	args = rest
	if c.json && len(args) > 0 && !jsonCommands[args[0]] && !isCommand(args[0]) {
		args = append([]string{"add"}, args...) // airs <path> --json
	}
	err = nil
	if c.json && (len(args) == 0 || !jsonCommands[args[0]]) {
		err = errUsage("--json works with add, list, qr, notes and remove")
	}
	if err == nil {
		err = c.dispatch(args)
	}
	if err != nil {
		if c.json {
			c.printJSON(map[string]string{"error": err.Error()})
		}
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
	case "add":
		return c.add(rest, addUsage("airs add"))
	case "list", "ls":
		return c.list()
	case "notes":
		q, err := oneArg("notes <file|id|name>", rest)
		if err != nil {
			return err
		}
		return c.notes(q)
	case "remove", "rm":
		all, yes, other := false, false, 0
		for _, a := range rest {
			switch a {
			case "--all":
				all = true
			case "--yes", "-y":
				yes = true
			default:
				other++
			}
		}
		if all || yes {
			if !all || other > 0 {
				return errUsage("usage: airs remove --all [--yes]")
			}
			return c.removeAll(yes)
		}
		q, err := oneArg("remove <id|name>", rest)
		if err != nil {
			return err
		}
		return c.remove(q)
	case "qr":
		if len(rest) > 1 || (len(rest) == 1 && rest[0] == "--qr") {
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
	return c.add(args, addUsage("airs"))
}

func addUsage(prefix string) string {
	return "usage: " + prefix + " <file|folder> [--any] [--no-notes] [--project <name>] [--notes-dir <dir>] [--json] [--qr]"
}

// isCommand reports whether a is a subcommand rather than a path to share.
func isCommand(a string) bool {
	switch a {
	case "help", "-h", "--help", "add", "list", "ls", "notes", "remove", "rm", "qr", "send", "trust", "untrust", "stop", "__serve":
		return true
	}
	return false
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

// addOptions are the flags of airs <file|folder> / airs add.
type addOptions struct {
	target, project, notesDir string
	any, noNotes, qr          bool
}

func parseAdd(args []string, usageMsg string) (addOptions, error) {
	var o addOptions
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(a, "=")
		switch {
		case a == "--any":
			o.any = true
		case a == "--no-notes":
			o.noNotes = true
		case a == "--qr":
			o.qr = true
		case name == "--project" || name == "--notes-dir":
			if !hasVal {
				if i+1 >= len(args) {
					return o, errUsage(name + " needs a value")
				}
				i++
				val = args[i]
			}
			if val == "" {
				return o, errUsage(name + " needs a value")
			}
			if name == "--project" {
				if err := project.Valid(val); err != nil {
					return o, errUsage(err.Error())
				}
				o.project = val
			} else {
				o.notesDir = val
			}
		case len(a) > 1 && a[0] == '-':
			return o, errUsage("unknown flag " + a)
		case o.target != "":
			return o, errUsage(usageMsg)
		default:
			o.target = a
		}
	}
	if o.target == "" {
		return o, errUsage(usageMsg)
	}
	return o, nil
}

// addResult is airs add --json output: the item plus how the add went.
type addResult struct {
	api.ItemView
	Existed        bool   `json:"existed"`
	NetworkTrusted bool   `json:"network_trusted"`
	QR             string `json:"qr,omitempty"`
}

func (c *cmd) add(args []string, usageMsg string) error {
	o, err := parseAdd(args, usageMsg)
	if err != nil {
		return err
	}
	abs, err := canonical(o.target)
	if err != nil {
		return fmt.Errorf("no such file or folder: %s", o.target)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("no such file or folder: %s", o.target)
	}
	if !fi.IsDir() {
		if _, ok := render.ForPath(abs); !ok {
			return errors.New("airs only shows markdown for now (.md, .markdown)")
		}
	} else if o.notesDir != "" {
		return errUsage("--notes-dir is for files; a folder's notes stay beside each file")
	}
	req := api.AddRequest{Path: abs, Any: o.any, NoNotes: o.noNotes, Project: o.project, MoveProject: o.project != ""}
	if req.Project == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		// Getwd follows $PWD, which may go through a symlink; name the real folder.
		if real, err := filepath.EvalSymlinks(wd); err == nil {
			wd = real
		}
		req.Project = project.Name(wd)
	}
	if o.notesDir != "" {
		if req.NotesDir, err = filepath.Abs(o.notesDir); err != nil {
			return err
		}
	}
	cl, err := c.connect()
	if err != nil {
		return err
	}
	res, err := cl.Add(req)
	if err != nil {
		return err
	}
	if c.json {
		out := addResult{ItemView: res.Item, Existed: res.Existed, NetworkTrusted: res.NetworkTrusted}
		if o.qr {
			if out.QR, err = qr.Medium(res.Item.URL); err != nil {
				return err
			}
		}
		return c.printJSON(out)
	}
	verb := "serving"
	if res.Existed {
		verb = "already serving"
	}
	fmt.Fprintf(c.out, "%s %s → %s\n", verb, res.Item.Name, res.Item.URL)
	if res.Item.Status == api.StatusBlocked {
		printUntrusted(c.out, res.Item, o.target)
		return nil
	}
	return c.printQR(res.Item.URL, o.qr)
}

// printQR prints the QR code on a terminal, or anywhere when asked (--qr).
func (c *cmd) printQR(u string, asked bool) error {
	switch {
	case isTerminal(c.out):
		return qr.Terminal(c.out, u)
	case asked:
		s, err := qr.Medium(u)
		if err != nil {
			return err
		}
		_, err = io.WriteString(c.out, s)
		return err
	}
	return nil
}

func (c *cmd) list() error {
	cl, err := c.connectIfShared()
	if errors.Is(err, errNothingShared) {
		if c.json {
			return c.printJSON([]api.ItemView{})
		}
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
	if c.json {
		return c.printJSON(items)
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
	if c.json {
		return c.printJSON(it)
	}
	fmt.Fprintf(c.out, "stopped sharing %s\n", it.Name)
	return nil
}

// removeAll stops sharing everything; notes files and trusted networks stay.
// Unless yes is set it lists the names and asks first, defaulting to no.
func (c *cmd) removeAll(yes bool) error {
	cl, err := c.connectIfShared()
	if errors.Is(err, errNothingShared) {
		if c.json {
			return c.printJSON([]api.ItemView{})
		}
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
	if len(items) > 0 && !yes {
		// The question goes to stderr so stdout stays clean for --json.
		fmt.Fprintf(c.err, "This stops sharing %d %s:\n", len(items), plural(len(items), "item", "items"))
		for _, it := range items {
			fmt.Fprintf(c.err, "  %s\n", it.Name)
		}
		fmt.Fprint(c.err, "Remove all? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		fmt.Fprintln(c.err)
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return errors.New("cancelled; nothing was removed (--yes skips the question)")
		}
	}
	removed := []api.ItemView{}
	for _, it := range items {
		r, err := cl.Remove(strconv.Itoa(it.ID))
		if err != nil {
			return err
		}
		removed = append(removed, r)
		if !c.json {
			fmt.Fprintf(c.out, "stopped sharing %s\n", r.Path)
		}
	}
	if c.json {
		return c.printJSON(removed)
	}
	if len(removed) == 0 {
		fmt.Fprintln(c.out, "nothing is being shared")
	} else {
		fmt.Fprintf(c.out, "stopped sharing %d %s\n", len(removed), plural(len(removed), "item", "items"))
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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
	if c.json {
		s, err := qr.Medium(u)
		if err != nil {
			return err
		}
		return c.printJSON(map[string]string{"url": u, "qr": s})
	}
	fmt.Fprintln(c.out, u)
	return qr.Terminal(c.out, u)
}

func (c *cmd) notes(q string) error {
	cl, err := c.connectIfShared()
	if errors.Is(err, errNothingShared) {
		return fmt.Errorf("not shared: %s", q)
	}
	if err != nil {
		return err
	}
	nr, err := cl.Notes(query(q))
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(nr)
	}
	fmt.Fprintf(c.out, "notes: %s\n", nr.Path)
	b, err := os.ReadFile(nr.Path)
	if len(nr.Notes) == 0 || err != nil {
		fmt.Fprintln(c.out, "no notes yet")
		return nil
	}
	fmt.Fprintln(c.out)
	_, err = c.out.Write(b)
	return err
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
	st, err := cl.Status()
	if err != nil {
		return err
	}
	if err := cl.Stop(); err != nil {
		return err
	}
	// Return only once the daemon has exited, so a command right after `stop`
	// starts a fresh one instead of reaching the one shutting down.
	waitExit(st.PID, 5*time.Second)
	fmt.Fprintln(c.out, "airs stopped (your list is kept for next time)")
	return nil
}

func waitExit(pid int, max time.Duration) {
	if pid <= 0 || pid == os.Getpid() { // in-process daemon (tests)
		return
	}
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}
