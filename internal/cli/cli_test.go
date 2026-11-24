package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"airs/internal/client"
	"airs/internal/config"
	"airs/internal/netinfo"
	"airs/internal/notes"
	"airs/internal/paths"
	"airs/internal/server"
)

type env struct {
	tmp     string
	home    string
	net     netinfo.Fake
	cancels []context.CancelFunc
	wg      sync.WaitGroup
}

var homeNet = netinfo.Info{GatewayIP: "192.168.1.1", GatewayMAC: "aa:aa:aa:aa:aa:aa", LocalIP: "127.0.0.1"}

// setup points airs at a short temp dir and starts daemons in-process.
func setup(t *testing.T, trusted bool) *env {
	t.Helper()
	tmp, err := os.MkdirTemp("/tmp", "airscli")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{tmp: tmp, home: filepath.Join(tmp, "home"), net: netinfo.Fake{Info: homeNet}}
	os.MkdirAll(filepath.Join(e.home, "proj", ".git"), 0o755)
	t.Chdir(filepath.Join(e.home, "proj")) // the project is "proj" unless a test moves
	t.Setenv("HOME", e.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "c"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmp, "s"))
	old := client.Start
	client.Start = func(dirs paths.Dirs) error {
		d, err := server.NewDaemon(dirs, config.Config{Port: 0, Notes: true}, e.net)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(context.Background())
		e.cancels = append(e.cancels, cancel)
		e.wg.Add(1)
		go func() { defer e.wg.Done(); d.Run(ctx) }()
		return nil
	}
	t.Cleanup(func() {
		client.Start = old
		for _, c := range e.cancels {
			c()
		}
		e.wg.Wait()
		os.RemoveAll(tmp)
	})
	if trusted {
		if _, _, code := e.run(t, "trust", "home"); code != 0 {
			t.Fatal("trust failed")
		}
	}
	return e
}

func (e *env) run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	return out.String(), errb.String(), code
}

func (e *env) file(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join(e.home, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// onTerminal makes the CLI treat its output as a terminal for one test.
func onTerminal(t *testing.T) {
	old := isTerminal
	isTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { isTerminal = old })
}

func TestAddPrintsURLAndQR(t *testing.T) {
	e := setup(t, true)
	onTerminal(t)
	out, errs, code := e.run(t, e.file(t, "notes.md"))
	if code != 0 || !strings.Contains(out, "serving notes.md → http://") || !strings.Contains(out, "/notes.md") || !strings.ContainsAny(out, "█▀▄") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if strings.Contains(out, "airs trust") {
		t.Fatalf("warning on trusted network: %s", out)
	}
	out, _, _ = e.run(t, e.file(t, "notes.md"))
	if !strings.Contains(out, "already serving notes.md") {
		t.Fatal(out)
	}
}

func TestAddRelativePath(t *testing.T) {
	e := setup(t, true)
	e.file(t, "rel/doc.md")
	wd, _ := os.Getwd()
	os.Chdir(filepath.Join(e.home, "rel"))
	defer os.Chdir(wd)
	if out, errs, code := e.run(t, "doc.md"); code != 0 || !strings.Contains(out, "serving doc.md") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
}

func TestAddUntrustedWarning(t *testing.T) {
	e := setup(t, false)
	out, _, code := e.run(t, e.file(t, "notes.md"))
	if code != 0 || !strings.Contains(out, "isn't trusted") || !strings.Contains(out, "airs trust") || !strings.Contains(out, "--any") {
		t.Fatalf("%d %s", code, out)
	}
	out, _, _ = e.run(t, e.file(t, "other.md"), "--any")
	if strings.Contains(out, "isn't trusted") {
		t.Fatalf("warning despite --any: %s", out)
	}
}

func TestAddRejectsNonMarkdown(t *testing.T) {
	e := setup(t, true)
	p := filepath.Join(e.home, "x.txt")
	os.WriteFile(p, []byte("x"), 0o644)
	if _, errs, code := e.run(t, p); code != 1 || !strings.Contains(errs, "only shows markdown") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestAddMissingPath(t *testing.T) {
	e := setup(t, true)
	if _, errs, code := e.run(t, filepath.Join(e.home, "nope.md")); code != 1 || !strings.Contains(errs, "no such file") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestSubcommandBeatsPath(t *testing.T) {
	e := setup(t, true)
	os.WriteFile(filepath.Join(e.home, "list"), []byte("x"), 0o644)
	wd, _ := os.Getwd()
	os.Chdir(e.home)
	defer os.Chdir(wd)
	if out, _, code := e.run(t, "list"); code != 0 || !strings.Contains(out, "nothing is being shared") {
		t.Fatalf("list: %d %s", code, out)
	}
	if _, errs, code := e.run(t, "./list"); code != 1 || !strings.Contains(errs, "only shows markdown") {
		t.Fatalf("./list treated as subcommand: %d %s", code, errs)
	}
}

func TestListFormatting(t *testing.T) {
	e := setup(t, false)
	e.run(t, e.file(t, "notes/notes.md"))
	os.MkdirAll(filepath.Join(e.home, "docs"), 0o755)
	e.run(t, filepath.Join(e.home, "docs"), "--any")
	out, _, code := e.run(t, "list")
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"ID", "NAME", "~/notes/notes.md", "blocked (untrusted network)", "docs/", "shared", "/docs/"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRemoveByIDNameAndPath(t *testing.T) {
	e := setup(t, true)
	a := e.file(t, "a.md")
	e.run(t, a)
	e.run(t, e.file(t, "b.md"))
	e.run(t, e.file(t, "c.md"))
	if out, _, code := e.run(t, "remove", "1"); code != 0 || !strings.Contains(out, "a.md") {
		t.Fatalf("by id: %d %s", code, out)
	}
	if _, _, code := e.run(t, "remove", "b.md"); code != 0 {
		t.Fatal("by name")
	}
	wd, _ := os.Getwd()
	os.Chdir(e.home)
	defer os.Chdir(wd)
	if _, errs, code := e.run(t, "remove", "./c.md"); code != 0 {
		t.Fatalf("by relative path: %s", errs)
	}
}

func TestRemoveAmbiguousPrintsMatches(t *testing.T) {
	e := setup(t, true)
	a, b := e.file(t, "a/notes.md"), e.file(t, "b/notes.md")
	e.run(t, a)
	e.run(t, b)
	wd, _ := os.Getwd()
	os.Chdir(e.tmp) // so "notes.md" is not a path on disk
	defer os.Chdir(wd)
	_, errs, code := e.run(t, "remove", "notes.md")
	if code != 1 || !strings.Contains(errs, "~/a/notes.md") || !strings.Contains(errs, "~/b/notes.md") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestRemoveWhenNothingShared(t *testing.T) {
	e := setup(t, false)
	if _, errs, code := e.run(t, "remove", "x.md"); code != 1 || !strings.Contains(errs, "nothing is being shared") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestQR(t *testing.T) {
	e := setup(t, true)
	onTerminal(t)
	e.run(t, e.file(t, "notes.md"))
	out, _, code := e.run(t, "qr", "notes.md")
	if code != 0 || !strings.Contains(out, "/notes.md") || !strings.ContainsAny(out, "█▀▄") {
		t.Fatalf("%d %s", code, out)
	}
	out, _, code = e.run(t, "qr")
	if code != 0 || !strings.Contains(out, "http://") {
		t.Fatalf("index: %d %s", code, out)
	}
}

func TestTrustUntrust(t *testing.T) {
	e := setup(t, false)
	e.run(t, e.file(t, "notes.md"))
	if out, _, code := e.run(t, "trust", "home"); code != 0 || !strings.Contains(out, "home") {
		t.Fatalf("%d %s", code, out)
	}
	if out, _, _ := e.run(t, "list"); !strings.Contains(out, "shared") || strings.Contains(out, "blocked") {
		t.Fatal(out)
	}
	if _, _, code := e.run(t, "untrust"); code != 0 {
		t.Fatal(code)
	}
	if out, _, _ := e.run(t, "list"); !strings.Contains(out, "blocked") {
		t.Fatal(out)
	}
}

func TestStop(t *testing.T) {
	e := setup(t, true)
	e.run(t, e.file(t, "notes.md"))
	if _, _, code := e.run(t, "stop"); code != 0 {
		t.Fatal(code)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, errs, code := e.run(t, "stop")
		if code == 1 && strings.Contains(errs, "not running") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("still running")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// list restarts the daemon and restores the item
	if out, _, _ := e.run(t, "list"); !strings.Contains(out, "notes.md") {
		t.Fatal(out)
	}
}

func TestHelp(t *testing.T) {
	e := setup(t, false)
	for _, args := range [][]string{nil, {"help"}, {"-h"}} {
		out, _, code := e.run(t, args...)
		if code != 0 || !strings.Contains(out, "airs <file|folder>") {
			t.Fatalf("%v: %d %s", args, code, out)
		}
	}
	if _, errs, code := e.run(t, "remove"); code != 1 || !strings.Contains(errs, "usage") {
		t.Fatalf("remove without arg: %d %s", code, errs)
	}
}

func TestAddSubcommand(t *testing.T) {
	e := setup(t, false)
	out, errs, code := e.run(t, "add", e.file(t, "notes.md"), "--any")
	if code != 0 || !strings.Contains(out, "serving notes.md") || strings.Contains(out, "isn't trusted") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if _, errs, code := e.run(t, "add"); code == 0 || !strings.Contains(errs, "usage: airs add <file|folder> [--any]") {
		t.Fatalf("bare add: %d %q", code, errs)
	}
}

func TestAddNoNotes(t *testing.T) {
	e := setup(t, true)
	if out, errs, code := e.run(t, "add", e.file(t, "quiet.md"), "--no-notes"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	e.run(t, e.file(t, "loud.md"))
	out, _, _ := e.run(t, "list")
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "quiet.md") && !strings.HasSuffix(strings.TrimSpace(line), "(no notes)"):
			t.Fatalf("quiet.md row: %q", line)
		case strings.Contains(line, "loud.md") && strings.Contains(line, "(no notes)"):
			t.Fatalf("loud.md row: %q", line)
		}
	}
}

func TestHelpMentionsNoNotes(t *testing.T) {
	e := setup(t, false)
	out, _, _ := e.run(t, "help")
	if !strings.Contains(out, "--no-notes") || !strings.Contains(out, "notes = false") {
		t.Fatal(out)
	}
}

func TestAddProjectFromCwd(t *testing.T) {
	e := setup(t, true)
	out, errs, code := e.run(t, e.file(t, "notes.md"))
	if code != 0 || !strings.Contains(out, "/proj/notes.md") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
}

func TestAddProjectFlag(t *testing.T) {
	e := setup(t, true)
	if out, _, _ := e.run(t, e.file(t, "a.md"), "--project", "other"); !strings.Contains(out, "/other/a.md") {
		t.Fatal(out)
	}
	if out, _, _ := e.run(t, e.file(t, "b.md"), "--project=other"); !strings.Contains(out, "/other/b.md") {
		t.Fatal(out)
	}
	// Re-adding without --project keeps the project; with it, the share moves.
	if out, _, _ := e.run(t, e.file(t, "a.md")); !strings.Contains(out, "/other/a.md") {
		t.Fatal(out)
	}
	if out, _, _ := e.run(t, e.file(t, "a.md"), "--project", "third"); !strings.Contains(out, "/third/a.md") {
		t.Fatal(out)
	}
	for _, bad := range [][]string{{"--project", "_airs"}, {"--project"}, {"--project="}} {
		if _, errs, code := e.run(t, append([]string{e.file(t, "c.md")}, bad...)...); code != 1 || !strings.Contains(errs, "project") {
			t.Fatalf("%v: %d %s", bad, code, errs)
		}
	}
}

func TestAddNoQRWhenNotTerminal(t *testing.T) {
	e := setup(t, true)
	if out, _, _ := e.run(t, e.file(t, "notes.md")); strings.ContainsAny(out, "█▀▄") {
		t.Fatalf("QR printed to a non-terminal:\n%s", out)
	}
}

func TestAddQRFlag(t *testing.T) {
	e := setup(t, true)
	if out, _, _ := e.run(t, e.file(t, "notes.md"), "--qr"); !strings.ContainsAny(out, "█▀▄") {
		t.Fatalf("no QR:\n%s", out)
	}
}

func TestAddJSON(t *testing.T) {
	e := setup(t, false)
	out, errs, code := e.run(t, e.file(t, "notes.md"), "--json")
	var m map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &m) != nil {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	for _, k := range []string{"id", "name", "project", "path", "url", "notes_path", "notes", "existed", "status", "network_trusted"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %q in %s", k, out)
		}
	}
	if _, ok := m["qr"]; ok || m["status"] != "blocked" || m["project"] != "proj" {
		t.Fatalf("%s", out)
	}
	if strings.Contains(out, "isn't trusted") {
		t.Fatal("warning text in JSON output")
	}
	out, _, _ = e.run(t, e.file(t, "notes.md"), "--json", "--qr")
	json.Unmarshal([]byte(out), &m)
	if qr, _ := m["qr"].(string); !strings.ContainsAny(qr, "█▀▄") || m["existed"] != true {
		t.Fatalf("%s", out)
	}
}

func TestAddNotesDir(t *testing.T) {
	e := setup(t, true)
	out, errs, code := e.run(t, e.file(t, "elsewhere/plan.md"), "--notes-dir", ".claude/notes", "--json")
	var m map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &m) != nil {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	wd, _ := os.Getwd()
	wd, _ = filepath.EvalSymlinks(wd)
	if m["notes_path"] != filepath.Join(wd, ".claude", "notes", "plan.notes.md") {
		t.Fatalf("%s", out)
	}
	os.MkdirAll(filepath.Join(e.home, "folder"), 0o755)
	if _, errs, code := e.run(t, filepath.Join(e.home, "folder"), "--notes-dir=x"); code != 1 || !strings.Contains(errs, "notes-dir") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestListJSON(t *testing.T) {
	e := setup(t, true)
	e.run(t, e.file(t, "notes.md"))
	out, _, code := e.run(t, "list", "--json")
	var items []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &items) != nil || len(items) != 1 || items[0]["project"] != "proj" {
		t.Fatalf("%d %s", code, out)
	}
	if out, _, _ := e.run(t, "remove", "1", "--json"); !strings.HasPrefix(out, "{") || json.Unmarshal([]byte(out), &map[string]any{}) != nil {
		t.Fatalf("remove --json: %s", out)
	}
	if out, _, _ := e.run(t, "list", "--json"); strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list: %q", out)
	}
}

func TestNotesCommand(t *testing.T) {
	e := setup(t, true)
	doc := e.file(t, "notes.md")
	e.run(t, doc)
	real, _ := filepath.EvalSymlinks(doc)
	out, errs, code := e.run(t, "notes", doc)
	if code != 0 || !strings.Contains(out, "notes: "+notes.Path(real)) || !strings.Contains(out, "no notes yet") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	var w notes.Writer
	w.Append(notes.Path(real), "notes.md", notes.Input{Heading: "Hi", Text: "fix the intro"}, time.Now())
	if out, _, _ := e.run(t, "notes", doc); !strings.Contains(out, "fix the intro") || strings.Contains(out, "no notes yet") {
		t.Fatal(out)
	}
	out, _, _ = e.run(t, "notes", doc, "--json")
	var nr struct {
		Path, Doc string
		Notes     []struct{ Text string }
	}
	if json.Unmarshal([]byte(out), &nr) != nil || nr.Path != notes.Path(real) || len(nr.Notes) != 1 || nr.Notes[0].Text != "fix the intro" {
		t.Fatalf("%s", out)
	}
	if _, errs, code := e.run(t, "notes", e.file(t, "other.md")); code != 1 || !strings.Contains(errs, "not shared") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestJSONErrors(t *testing.T) {
	e := setup(t, true)
	out, _, code := e.run(t, filepath.Join(e.home, "missing.md"), "--json")
	var m map[string]string
	if code != 1 || json.Unmarshal([]byte(out), &m) != nil || !strings.Contains(m["error"], "no such file") {
		t.Fatalf("%d %q", code, out)
	}
	if _, errs, code := e.run(t, "trust", "--json"); code != 1 || !strings.Contains(errs, "--json") {
		t.Fatalf("trust --json: %d %s", code, errs)
	}
}

func TestQRJSON(t *testing.T) {
	e := setup(t, true)
	e.run(t, e.file(t, "notes.md"))
	out, _, code := e.run(t, "qr", "1", "--json")
	var m map[string]string
	if code != 0 || json.Unmarshal([]byte(out), &m) != nil || !strings.Contains(m["url"], "/proj/notes.md") || !strings.ContainsAny(m["qr"], "█▀▄") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestHelpMentionsAgentFlags(t *testing.T) {
	e := setup(t, false)
	out, _, _ := e.run(t, "help")
	for _, want := range []string{"--project", "--notes-dir", "--json", "--qr", "airs notes"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestAddProjectThroughSymlinkedCwd(t *testing.T) {
	e := setup(t, true)
	link := filepath.Join(e.home, "shortcut")
	if err := os.Symlink(filepath.Join(e.home, "proj"), link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(link)
	if out, errs, code := e.run(t, e.file(t, "notes.md")); code != 0 || !strings.Contains(out, "/proj/notes.md") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
}

func TestRemoveAll(t *testing.T) {
	e := setup(t, true)
	if out, _, code := e.run(t, "remove", "--all"); code != 0 || !strings.Contains(out, "nothing is being shared") {
		t.Fatalf("nothing shared: %d %s", code, out)
	}
	e.run(t, e.file(t, "a.md"))
	e.run(t, e.file(t, "b.md"))
	out, errs, code := e.run(t, "remove", "--all")
	if code != 0 || !strings.Contains(out, "a.md") || !strings.Contains(out, "b.md") || !strings.Contains(out, "stopped sharing 2 items") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	if out, _, _ := e.run(t, "list"); !strings.Contains(out, "nothing is being shared") {
		t.Fatalf("still shared:\n%s", out)
	}
	e.run(t, e.file(t, "c.md"))
	out, _, _ = e.run(t, "rm", "--all", "--json")
	var removed []map[string]any
	if json.Unmarshal([]byte(out), &removed) != nil || len(removed) != 1 || removed[0]["name"] != "c.md" {
		t.Fatalf("json: %s", out)
	}
	if _, errs, code := e.run(t, "remove", "--all", "x.md"); code != 1 || !strings.Contains(errs, "usage") {
		t.Fatalf("--all with a name: %d %s", code, errs)
	}
}
