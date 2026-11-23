package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	defer airs("stop")
	logTail := func() string { b, _ := os.ReadFile(filepath.Join(stateDir, "airs.log")); return string(b) }

	if out, code := airs("notes.md"); code != 0 || !strings.Contains(out, "serving notes.md") {
		t.Fatalf("add: %d %s\nlog: %s", code, out, logTail())
	}
	if c := get("/notes.md"); c != 200 {
		t.Fatalf("GET /notes.md %d", c)
	}
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
		t.Fatalf("re-add: %s", out)
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
		return
	}
	// The daemon unloads its transient launchd job when it exits.
	label := client.Label(paths.Dirs{State: stateDir})
	deadline := time.Now().Add(2 * time.Second)
	for exec.Command("/bin/launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Run() == nil {
		if time.Now().After(deadline) {
			exec.Command("/bin/launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Run()
			t.Fatalf("launchd job %s still loaded after the daemon exited", label)
		}
		time.Sleep(100 * time.Millisecond)
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
