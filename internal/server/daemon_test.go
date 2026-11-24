package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"airs/internal/api"
	"airs/internal/config"
	"airs/internal/netinfo"
	"airs/internal/notes"
	"airs/internal/paths"
	"airs/internal/state"
)

// mutableNet is a netinfo.Source tests can switch between networks.
type mutableNet struct {
	mu   sync.Mutex
	info netinfo.Info
	err  error
}

func (m *mutableNet) Current() (netinfo.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.info, m.err
}

func (m *mutableNet) set(i netinfo.Info, err error) {
	m.mu.Lock()
	m.info, m.err = i, err
	m.mu.Unlock()
}

var netA = netinfo.Info{GatewayIP: "192.168.1.1", GatewayMAC: "aa:aa:aa:aa:aa:aa", LocalIP: "127.0.0.1"}
var netB = netinfo.Info{GatewayIP: "192.168.1.1", GatewayMAC: "bb:bb:bb:bb:bb:bb", LocalIP: "127.0.0.1"}

type harness struct {
	d    *Daemon
	dirs paths.Dirs
	net  *mutableNet
	ctl  *http.Client
	done chan error
	tmp  string
}

// shortTemp keeps Unix socket paths under the 104-byte macOS limit.
func shortTemp(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "airs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	if r, err := filepath.EvalSymlinks(d); err == nil {
		d = r // stored paths are symlink-resolved
	}
	return d
}

func newHarness(t *testing.T, seed func(*state.Store)) *harness {
	t.Helper()
	tmp := shortTemp(t)
	dirs := paths.Dirs{Config: filepath.Join(tmp, "c"), State: filepath.Join(tmp, "s")}
	if seed != nil {
		os.MkdirAll(dirs.State, 0o700)
		st, _ := state.Open(dirs.StateFile())
		seed(st)
	}
	h := &harness{dirs: dirs, net: &mutableNet{info: netA}, done: make(chan error, 1), tmp: tmp}
	return h
}

func (h *harness) start(t *testing.T, cfg config.Config) {
	t.Helper()
	d, err := NewDaemon(h.dirs, cfg, h.net)
	if err != nil {
		t.Fatal(err)
	}
	d.NetPollInterval = 50 * time.Millisecond
	d.IdleGrace = 300 * time.Millisecond
	h.d = d
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(3 * time.Second):
		}
	})
	go func() { h.done <- d.Run(ctx) }()
	h.ctl = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", h.dirs.Socket())
	}}}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", h.dirs.Socket()); err == nil {
			c.Close()
			return
		}
		select {
		case err := <-h.done:
			h.done <- err
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("daemon did not start")
}

func (h *harness) call(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, "http://airs"+path, r)
	resp, err := h.ctl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (h *harness) waitExit(t *testing.T, within time.Duration) error {
	t.Helper()
	select {
	case err := <-h.done:
		h.done <- err
		return err
	case <-time.After(within):
		t.Fatal("daemon did not exit")
		return nil
	}
}

func (h *harness) file(t *testing.T, rel string) string {
	p := filepath.Join(h.tmp, "files", rel)
	mkfile(t, p)
	return p
}

func trusted(s *state.Store) { s.Trust(state.Network{Fingerprint: netA.Fingerprint(), Label: "home"}) }

func TestControlAddListRemove(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	p := h.file(t, "notes.md")
	var add api.AddResponse
	if code := h.call(t, "POST", "/items", api.AddRequest{Path: p}, &add); code != 200 {
		t.Fatalf("add %d", code)
	}
	if add.Item.ID != 1 || add.Item.Status != api.StatusShared || !add.NetworkTrusted || add.Existed {
		t.Fatalf("%+v", add)
	}
	if !strings.HasSuffix(add.Item.URL, "/notes.md") {
		t.Fatalf("url %s", add.Item.URL)
	}
	var items []api.ItemView
	h.call(t, "GET", "/items", nil, &items)
	if len(items) != 1 {
		t.Fatalf("%+v", items)
	}
	// LAN HTTP is live
	resp, err := http.Get("http://" + h.d.HTTPAddr() + "/files/notes.md")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("http %v %v", err, resp)
	}
	resp.Body.Close()
	var removed api.ItemView
	if code := h.call(t, "DELETE", "/items/1", nil, &removed); code != 200 || removed.ID != 1 {
		t.Fatalf("remove %d %+v", code, removed)
	}
}

func TestControlRemoveByPath(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	p := h.file(t, "notes.md")
	h.call(t, "POST", "/items", api.AddRequest{Path: p}, nil)
	if code := h.call(t, "DELETE", "/items/"+url.PathEscape(p), nil, nil); code != 200 {
		t.Fatalf("remove by path %d", code)
	}
}

func TestControlAddRejectsBadPath(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	var e api.ErrorResponse
	if code := h.call(t, "POST", "/items", api.AddRequest{Path: "relative.md"}, &e); code != 400 {
		t.Fatalf("relative %d", code)
	}
	if code := h.call(t, "POST", "/items", api.AddRequest{Path: "/nope/x.md"}, &e); code != 400 {
		t.Fatalf("missing %d", code)
	}
}

func TestControlAmbiguousRemove(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "a/notes.md")}, nil)
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "b/notes.md")}, nil)
	var e api.ErrorResponse
	if code := h.call(t, "DELETE", "/items/notes.md", nil, &e); code != 409 || len(e.Matches) != 2 {
		t.Fatalf("%d %+v", code, e)
	}
	if code := h.call(t, "DELETE", "/items/nope.md", nil, &e); code != 404 {
		t.Fatalf("not found %d", code)
	}
}

func TestTrustUntrust(t *testing.T) {
	h := newHarness(t, nil)
	h.start(t, config.Config{Port: 0})
	var add api.AddResponse
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "notes.md")}, &add)
	if add.NetworkTrusted || add.Item.Status != api.StatusBlocked {
		t.Fatalf("%+v", add)
	}
	var st api.StatusResponse
	if code := h.call(t, "POST", "/trust", api.TrustRequest{Label: "home"}, &st); code != 200 || !st.NetworkTrusted || st.NetworkLabel != "home" {
		t.Fatalf("%d %+v", code, st)
	}
	var items []api.ItemView
	h.call(t, "GET", "/items", nil, &items)
	if items[0].Status != api.StatusShared {
		t.Fatalf("%+v", items)
	}
	h.call(t, "DELETE", "/trust", nil, &st)
	h.call(t, "GET", "/items", nil, &items)
	if st.NetworkTrusted || items[0].Status != api.StatusBlocked {
		t.Fatalf("%+v %+v", st, items)
	}
}

func TestTrustUnidentifiedNetwork(t *testing.T) {
	h := newHarness(t, nil)
	h.net.set(netinfo.Info{LocalIP: "127.0.0.1"}, os.ErrNotExist)
	h.start(t, config.Config{Port: 0})
	var e api.ErrorResponse
	if code := h.call(t, "POST", "/trust", api.TrustRequest{}, &e); code != 409 || e.Error == "" {
		t.Fatalf("%d %+v", code, e)
	}
}

// remoteGet requests the LAN server from a non-loopback source address.
func remoteGet(t *testing.T, h *harness, path string) int {
	t.Helper()
	req := httptestRequest(path)
	rec := newRecorder()
	h.d.lanHandler().ServeHTTP(rec, req)
	return rec.Code
}

func TestNetworkChangeReGates(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "notes.md")}, nil)
	if code := remoteGet(t, h, "/files/notes.md"); code != 200 {
		t.Fatalf("trusted %d", code)
	}
	h.net.set(netB, nil)
	deadline := time.Now().Add(2 * time.Second)
	for remoteGet(t, h, "/files/notes.md") != 403 {
		if time.Now().After(deadline) {
			t.Fatal("still shared after network change")
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.net.set(netA, nil)
	deadline = time.Now().Add(2 * time.Second)
	for remoteGet(t, h, "/files/notes.md") != 200 {
		if time.Now().After(deadline) {
			t.Fatal("not shared again after returning")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestOfflineStillServesLocal(t *testing.T) {
	h := newHarness(t, trusted)
	h.net.set(netinfo.Info{}, os.ErrNotExist)
	h.start(t, config.Config{Port: 0})
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "notes.md")}, nil)
	var st api.StatusResponse
	h.call(t, "GET", "/status", nil, &st)
	if !strings.HasPrefix(st.BaseURL, "http://localhost:") || st.NetworkTrusted {
		t.Fatalf("%+v", st)
	}
	resp, err := http.Get("http://" + h.d.HTTPAddr() + "/files/notes.md")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", err, resp)
	}
	resp.Body.Close()
}

func TestExitWhenEmpty(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "notes.md")}, nil)
	h.call(t, "DELETE", "/items/1", nil, nil)
	if err := h.waitExit(t, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.dirs.Socket()); !os.IsNotExist(err) {
		t.Fatal("socket not removed")
	}
}

func TestAddCancelsIdleExit(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "notes.md")}, nil)
	time.Sleep(600 * time.Millisecond) // > IdleGrace
	var st api.StatusResponse
	if code := h.call(t, "GET", "/status", nil, &st); code != 200 {
		t.Fatalf("daemon exited despite an item: %d", code)
	}
}

func TestStartEmptyExits(t *testing.T) {
	h := newHarness(t, nil)
	h.start(t, config.Config{Port: 0})
	if err := h.waitExit(t, 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestStopKeepsItems(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	p := h.file(t, "notes.md")
	h.call(t, "POST", "/items", api.AddRequest{Path: p}, nil)
	h.call(t, "POST", "/stop", nil, nil)
	if err := h.waitExit(t, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Open(h.dirs.StateFile())
	if items := st.Items(); len(items) != 1 || items[0].Path != p {
		t.Fatalf("%+v", items)
	}
}

func TestRestoresItemsOnStart(t *testing.T) {
	h := newHarness(t, trusted)
	f := h.file(t, "notes.md")
	st, _ := state.Open(h.dirs.StateFile())
	st.Add(f, false, state.Flags{})
	h.start(t, config.Config{Port: 0})
	var items []api.ItemView
	h.call(t, "GET", "/items", nil, &items)
	if len(items) != 1 || items[0].Status != api.StatusShared {
		t.Fatalf("%+v", items)
	}
}

func TestMissingStatus(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	p := h.file(t, "notes.md")
	h.call(t, "POST", "/items", api.AddRequest{Path: p}, nil)
	os.Remove(p)
	var items []api.ItemView
	h.call(t, "GET", "/items", nil, &items)
	if items[0].Status != api.StatusMissing {
		t.Fatalf("%+v", items)
	}
}

func TestStaleSocketRemoved(t *testing.T) {
	h := newHarness(t, trusted)
	os.MkdirAll(h.dirs.State, 0o700)
	ln, err := net.Listen("unix", h.dirs.Socket())
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close() // leaves a dead socket file behind, like kill -9
	st, _ := state.Open(h.dirs.StateFile())
	st.Add(h.file(t, "notes.md"), false, state.Flags{})
	h.start(t, config.Config{Port: 0})
	var s api.StatusResponse
	if code := h.call(t, "GET", "/status", nil, &s); code != 200 || s.PID != os.Getpid() {
		t.Fatalf("%d %+v", code, s)
	}
}

func TestSecondInstanceRefused(t *testing.T) {
	h := newHarness(t, trusted)
	st, _ := state.Open(func() string { os.MkdirAll(h.dirs.State, 0o700); return h.dirs.StateFile() }())
	st.Add(h.file(t, "notes.md"), false, state.Flags{})
	h.start(t, config.Config{Port: 0})
	d2, _ := NewDaemon(h.dirs, config.Config{Port: 0}, h.net)
	err := d2.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("got %v", err)
	}
	var s api.StatusResponse
	if code := h.call(t, "GET", "/status", nil, &s); code != 200 {
		t.Fatal("first daemon disturbed")
	}
}

func TestPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	h := newHarness(t, trusted)
	d, _ := NewDaemon(h.dirs, config.Config{Port: port}, h.net)
	err = d.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("got %v", err)
	}
}

// Browsers and HTTP clients open spare connections that never send a request;
// http.Server.Shutdown waits up to 5s for those. Stop must not.
func TestStopWithIdleUnusedConnections(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "notes.md")}, nil)
	ctlIdle, err := net.Dial("unix", h.dirs.Socket())
	if err != nil {
		t.Fatal(err)
	}
	defer ctlIdle.Close()
	lanIdle, err := net.Dial("tcp", h.d.HTTPAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer lanIdle.Close()
	time.Sleep(50 * time.Millisecond)
	h.call(t, "POST", "/stop", nil, nil)
	if err := h.waitExit(t, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestControlFind(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "a/notes.md")}, nil)
	var v api.ItemView
	if code := h.call(t, "GET", "/items/notes.md", nil, &v); code != 200 || v.ID != 1 {
		t.Fatalf("%d %+v", code, v)
	}
	if code := h.call(t, "GET", "/items/1", nil, &v); code != 200 || v.ID != 1 {
		t.Fatalf("by id %d", code)
	}
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "b/notes.md")}, nil)
	var e api.ErrorResponse
	if code := h.call(t, "GET", "/items/notes.md", nil, &e); code != 409 || len(e.Matches) != 2 {
		t.Fatalf("ambiguous %d %+v", code, e)
	}
	if code := h.call(t, "GET", "/items/zzz", nil, &e); code != 404 {
		t.Fatalf("missing %d", code)
	}
}

func TestControlPathsAreSymlinkResolved(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0})
	real := h.file(t, "real/notes.md")
	link := filepath.Join(h.tmp, "link")
	os.Symlink(filepath.Dir(real), link)
	var add api.AddResponse
	h.call(t, "POST", "/items", api.AddRequest{Path: filepath.Join(link, "notes.md")}, &add)
	if add.Item.Path != real {
		t.Fatalf("stored %s, want %s", add.Item.Path, real)
	}
	if code := h.call(t, "DELETE", "/items/"+url.PathEscape(filepath.Join(link, "notes.md")), nil, nil); code != 200 {
		t.Fatalf("remove via symlink path %d", code)
	}
}

func TestAddProjectInURL(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0, Notes: true})
	var add api.AddResponse
	h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "notes.md"), Project: "proj"}, &add)
	if add.Item.Project != "proj" || !strings.HasSuffix(add.Item.URL, "/proj/notes.md") {
		t.Fatalf("%+v", add.Item)
	}
	if code := remoteGet(t, h, "/proj/notes.md"); code != 200 {
		t.Fatal(code)
	}
}

func TestAddCreatesNotesDir(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0, Notes: true})
	nd := filepath.Join(h.tmp, "nd", "sub")
	var add api.AddResponse
	if code := h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "plan.md"), NotesDir: nd}, &add); code != 200 {
		t.Fatal(code)
	}
	if fi, err := os.Stat(nd); err != nil || !fi.IsDir() {
		t.Fatalf("notes dir not created: %v", err)
	}
	real, _ := filepath.EvalSymlinks(nd)
	if add.Item.NotesPath != filepath.Join(real, "plan.notes.md") {
		t.Fatalf("notes path %q", add.Item.NotesPath)
	}
}

func TestAddRejectsNotesDirForFolder(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0, Notes: true})
	dir := filepath.Dir(h.file(t, "d/a.md"))
	if code := h.call(t, "POST", "/items", api.AddRequest{Path: dir, NotesDir: filepath.Join(h.tmp, "nd")}, nil); code != 400 {
		t.Fatal(code)
	}
}

func TestAddRejectsBadProject(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0, Notes: true})
	if code := h.call(t, "POST", "/items", api.AddRequest{Path: h.file(t, "notes.md"), Project: "_airs"}, nil); code != 400 {
		t.Fatal(code)
	}
}

func TestControlNotes(t *testing.T) {
	h := newHarness(t, trusted)
	h.start(t, config.Config{Port: 0, Notes: true})
	p := h.file(t, "notes.md")
	h.call(t, "POST", "/items", api.AddRequest{Path: p}, nil)
	real, _ := filepath.EvalSymlinks(p)
	var nr api.NotesResponse
	if code := h.call(t, "GET", "/notes/"+url.PathEscape(p), nil, &nr); code != 200 {
		t.Fatal(code)
	}
	if nr.Path != notes.Path(real) || nr.Doc != real || nr.Notes == nil || len(nr.Notes) != 0 {
		t.Fatalf("%+v", nr)
	}
	var w notes.Writer
	w.Append(notes.Path(real), "notes.md", notes.Input{Heading: "N", Text: "hi"}, time.Now())
	h.call(t, "GET", "/notes/"+url.PathEscape(p), nil, &nr)
	if len(nr.Notes) != 1 || nr.Notes[0].Text != "hi" {
		t.Fatalf("%+v", nr)
	}
	// A file inside a shared folder.
	inner := h.file(t, "d/sub/a.md")
	h.call(t, "POST", "/items", api.AddRequest{Path: filepath.Dir(filepath.Dir(inner))}, nil)
	realInner, _ := filepath.EvalSymlinks(inner)
	if code := h.call(t, "GET", "/notes/"+url.PathEscape(inner), nil, &nr); code != 200 || nr.Path != notes.Path(realInner) {
		t.Fatalf("%d %+v", code, nr)
	}
	var e api.ErrorResponse
	if code := h.call(t, "GET", "/notes/"+url.PathEscape(h.file(t, "other.md")), nil, &e); code != 404 || !strings.HasPrefix(e.Error, "not shared") {
		t.Fatalf("%d %+v", code, e)
	}
}
