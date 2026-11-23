package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"airs/internal/client"
	"airs/internal/paths"
)

// TestEndToEnd drives the real binary: it starts its daemon through launchd
// (or directly, if launchctl is unavailable), serves HTTP, and exits on its own.
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e")
	}
	tmp, _ := os.MkdirTemp("/tmp", "airse2e")
	tmp, _ = filepath.EvalSymlinks(tmp)
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "airs")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	home := filepath.Join(tmp, "home")
	os.MkdirAll(filepath.Join(home, "docs", "sub"), 0o755)
	os.WriteFile(filepath.Join(home, "notes.md"), []byte("# Notes\n"), 0o644)
	os.WriteFile(filepath.Join(home, "docs", "sub", "a.md"), []byte("# A\n"), 0o644)
	ln, _ := net.Listen("tcp", ":0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfgDir := filepath.Join(tmp, "c", "airs")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(fmt.Sprintf("port = %d\nany_network = true\n", port)), 0o644)
	stateDir := filepath.Join(tmp, "s", "airs")
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(tmp, "c"), "XDG_STATE_HOME="+filepath.Join(tmp, "s"))

	airs := func(args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env, cmd.Dir = env, home
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	get := func(path string) int {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
		if err != nil {
			return 0
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	defer func() {
		airs("stop")
		label := client.Label(paths.Dirs{State: stateDir})
		exec.Command("/bin/launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Run()
	}()
	logTail := func() string { b, _ := os.ReadFile(filepath.Join(stateDir, "airs.log")); return string(b) }

	if out, code := airs("notes.md"); code != 0 || !strings.Contains(out, "serving notes.md") {
		t.Fatalf("add: %d %s\nlog: %s", code, out, logTail())
	}
	if c := get("/notes.md"); c != 200 {
		t.Fatalf("GET /notes.md %d", c)
	}
	e2eNote(t, port, home)
	if out, code := airs("docs"); code != 0 || !strings.Contains(out, "docs/") {
		t.Fatalf("add folder: %d %s", code, out)
	}
	if c := get("/docs/sub/a.md"); c != 200 {
		t.Fatalf("GET folder page %d", c)
	}
	if out, _ := airs("list"); strings.Count(out, "\n") != 3 {
		t.Fatalf("list:\n%s", out)
	}
	airs("remove", "notes.md")
	airs("remove", "2")

	sock := filepath.Join(stateDir, "airs.sock")
	waitGone := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(sock); os.IsNotExist(err) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("daemon still running\nlog: %s", logTail())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitGone()

	// Kill -9 leaves a stale socket; the next command must recover.
	if out, code := airs("notes.md"); code != 0 {
		t.Fatalf("re-add: %s\nlog: %s", out, logTail())
	}
	pid := daemonPID(t, bin, env, home)
	syscall.Kill(pid, syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
	if out, code := airs("list"); code != 0 || !strings.Contains(out, "notes.md") {
		t.Fatalf("after kill -9: %d %s\nlog: %s", code, out, logTail())
	}
	if c := get("/notes.md"); c != 200 {
		t.Fatalf("GET after restart %d", c)
	}
	if _, code := airs("stop"); code != 0 {
		t.Fatal("stop")
	}
	waitGone()
	if out, code := airs("stop"); code != 1 || !strings.Contains(out, "not running") {
		t.Fatalf("second stop: %d %s", code, out)
	}
	if strings.Contains(logTail(), "launchctl bootstrap failed") {
		t.Log("note: daemon was started directly (launchctl unavailable here)")
	}

	// stop followed immediately by another command (e.g. after editing the
	// config) must leave a working daemon.
	airs("notes.md")
	for i := 0; i < 3; i++ {
		if out, code := airs("stop"); code != 0 {
			t.Fatalf("stop %d: %s", i, out)
		}
		if out, code := airs("list"); code != 0 || !strings.Contains(out, "notes.md") {
			t.Fatalf("list right after stop (%d): %d %s\nlog: %s", i, code, out, logTail())
		}
		if c := get("/notes.md"); c != 200 {
			t.Fatalf("GET right after stop+list (%d): %d\nlog: %s", i, c, logTail())
		}
	}
	airs("stop")

	// Two first starts at once both succeed.
	os.WriteFile(filepath.Join(home, "a.md"), []byte("# A\n"), 0o644)
	os.WriteFile(filepath.Join(home, "b.md"), []byte("# B\n"), 0o644)
	airs("remove", "notes.md")
	type res struct {
		out  string
		code int
	}
	results := make(chan res, 2)
	for _, f := range []string{"a.md", "b.md"} {
		go func(f string) {
			cmd := exec.Command(bin, f)
			cmd.Env, cmd.Dir = env, home
			out, err := cmd.CombinedOutput()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
			results <- res{string(out), code}
		}(f)
	}
	for i := 0; i < 2; i++ {
		if r := <-results; r.code != 0 {
			t.Fatalf("concurrent start: %d %s\nlog: %s", r.code, r.out, logTail())
		}
	}
	if out, _ := airs("list"); !strings.Contains(out, "a.md") || !strings.Contains(out, "b.md") {
		t.Fatalf("after concurrent start:\n%s", out)
	}
}

func daemonPID(t *testing.T, bin string, env []string, home string) int {
	t.Helper()
	// status is not a CLI command; read it through the socket the way the CLI does.
	sock := filepath.Join(strings.TrimPrefix(envValue(env, "XDG_STATE_HOME"), ""), "airs", "airs.sock")
	c := http.Client{Transport: &http.Transport{Dial: func(_, _ string) (net.Conn, error) { return net.Dial("unix", sock) }}}
	resp, err := c.Get("http://airs/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		PID int `json:"pid"`
	}
	json.NewDecoder(resp.Body).Decode(&st)
	if st.PID == 0 {
		t.Fatal("no pid")
	}
	return st.PID
}

func envValue(env []string, k string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], k+"=") {
			return env[i][len(k)+1:]
		}
	}
	return ""
}

// e2eNote adds a note to notes.md through the LAN server, as a page would,
// and checks it lands in notes.notes.md and comes back in the render JSON.
func e2eNote(t *testing.T, port int, home string) {
	t.Helper()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	resp, err := http.Get(base + "/notes.md")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := regexp.MustCompile(`<script type="application/json" id="airs-doc">(.*?)</script>`).FindSubmatch(page)
	if m == nil {
		t.Fatal("no airs-doc JSON on the page")
	}
	var doc struct {
		Key          string `json:"key"`
		NotesEnabled bool   `json:"notesEnabled"`
	}
	if err := json.Unmarshal(m[1], &doc); err != nil || !doc.NotesEnabled {
		t.Fatalf("doc JSON %s: %v", m[1], err)
	}
	body := `{"block":"Notes","index":0,"section":"notes","heading":"Notes","quote":"","text":"e2e note"}`
	resp, err = http.Post(base+"/_airs/notes/"+doc.Key+"/", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("POST note: %d", resp.StatusCode)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "notes.notes.md")); !strings.Contains(string(b), "e2e note") {
		t.Fatalf("notes file: %q", b)
	}
	resp, err = http.Get(base + "/_airs/render/" + doc.Key + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"text":"e2e note"`) {
		t.Fatalf("render JSON: %s", b)
	}
}
