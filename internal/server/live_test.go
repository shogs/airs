package server

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"airs/internal/netinfo"
	"airs/internal/state"
)

func liveFixture(t *testing.T) (*fixture, *httptest.Server, state.Item) {
	t.Helper()
	f := newFixture(t)
	for _, it := range f.store.Items() {
		if err := f.hub.Watch(it.ID, it.Path, it.IsDir, watchPaths(it)...); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(f.h)
	t.Cleanup(srv.Close)
	it, _ := f.store.ByLink("p", "notes.md")
	return f, srv, it
}

// readEvent returns the next SSE event name, skipping comments.
func readEvent(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	type res struct {
		ev  string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				ch <- res{"", err}
				return
			}
			if strings.HasPrefix(line, "event: ") {
				ch <- res{strings.TrimSpace(strings.TrimPrefix(line, "event: ")), nil}
				return
			}
		}
	}()
	select {
	case x := <-ch:
		if x.err != nil {
			t.Fatalf("read: %v", x.err)
		}
		return x.ev
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
		return ""
	}
}

func openEvents(t *testing.T, srv *httptest.Server, key string) (*http.Response, *bufio.Reader) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/_airs/events/" + key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	r := bufio.NewReader(resp.Body)
	if ev := readEvent(t, r); ev != "ready" {
		t.Fatalf("first event %q", ev)
	}
	return resp, r
}

func TestEventsChangeOnWrite(t *testing.T) {
	f, srv, it := liveFixture(t)
	_, r := openEvents(t, srv, it.Key())
	os.WriteFile(filepath.Join(f.dir, "notes.md"), []byte("# Changed\n"), 0o644)
	if ev := readEvent(t, r); ev != "change" {
		t.Fatalf("got %q", ev)
	}
}

func TestEventsGoneOnUnwatch(t *testing.T) {
	f, srv, it := liveFixture(t)
	_, r := openEvents(t, srv, it.Key())
	f.hub.Unwatch(it.ID)
	if ev := readEvent(t, r); ev != "gone" {
		t.Fatalf("got %q", ev)
	}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestRenderJSON(t *testing.T) {
	_, srv, it := liveFixture(t)
	code, m := getJSON(t, srv.URL+"/_airs/render/"+it.Key()+"/")
	if code != 200 || m["markdown"] != "# Notes\n\n![](img.png)\n" || !strings.Contains(m["html"].(string), "<h1") || m["title"] != "Notes" {
		t.Fatalf("%d %v", code, m)
	}
}

func TestRenderJSONInFolder(t *testing.T) {
	f, srv, _ := liveFixture(t)
	d, _ := f.store.ByLink("p", "docs")
	code, m := getJSON(t, srv.URL+"/_airs/render/"+d.Key()+"/sub/b.md")
	if code != 200 || !strings.Contains(m["html"].(string), "<h1") {
		t.Fatalf("%d %v", code, m)
	}
}

func TestRenderMissing(t *testing.T) {
	f, srv, it := liveFixture(t)
	os.Remove(filepath.Join(f.dir, "notes.md"))
	code, m := getJSON(t, srv.URL+"/_airs/render/"+it.Key()+"/")
	if code != 404 || m["missing"] != true {
		t.Fatalf("%d %v", code, m)
	}
}

func TestStaleKeyAfterNameReuse(t *testing.T) {
	f, srv, _ := liveFixture(t)
	a := filepath.Join(f.dir, "a", "README.md")
	b := filepath.Join(f.dir, "b", "README.md")
	mkfile(t, a)
	mkfile(t, b)
	old, _, _ := f.store.Add(a, false, state.Flags{Project: "p"})
	f.store.Remove(a)
	f.hub.Unwatch(old.ID)
	nu, _, _ := f.store.Add(b, false, state.Flags{Project: "p"})
	if nu.Slug != old.Slug {
		t.Fatalf("slug not reused: %q vs %q", nu.Slug, old.Slug)
	}
	for _, p := range []string{"/_airs/events/" + old.Key(), "/_airs/render/" + old.Key() + "/"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 410 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestEventsForbiddenUntrusted(t *testing.T) {
	f, _, it := liveFixture(t)
	f.ns.Set(netinfo.Info{}, false)
	for _, p := range []string{"/_airs/events/" + it.Key(), "/_airs/render/" + it.Key() + "/"} {
		if code, _, _ := f.get(t, p, remote); code != 403 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestEventsChangeOnNotesWrite(t *testing.T) {
	f, srv, it := liveFixture(t)
	_, r := openEvents(t, srv, it.Key())
	os.WriteFile(filepath.Join(f.dir, "notes.notes.md"), []byte("note\n"), 0o644)
	if ev := readEvent(t, r); ev != "change" {
		t.Fatalf("got %q", ev)
	}
}

func TestEventsChangeOnNotesDirWrite(t *testing.T) {
	f := newFixture(t)
	nd := t.TempDir()
	it, _, _ := f.store.Add(filepath.Join(f.dir, "notes.md"), false, state.Flags{NotesDir: nd})
	if err := f.hub.Watch(it.ID, it.Path, it.IsDir, watchPaths(it)...); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f.h)
	t.Cleanup(srv.Close)
	_, r := openEvents(t, srv, it.Key())
	os.WriteFile(filepath.Join(nd, "notes.notes.md"), []byte("note\n"), 0o644)
	if ev := readEvent(t, r); ev != "change" {
		t.Fatalf("got %q", ev)
	}
}
