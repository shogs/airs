package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"airs/internal/config"
	"airs/internal/netinfo"
	"airs/internal/state"
)

const lanHost = "192.168.1.20:7777"

// send makes a write request arriving on the trusted address, JSON by default.
func (f *fixture) send(t *testing.T, method, path, from, body string, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = from
	req.Host = lanHost
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	la, _ := net.ResolveTCPAddr("tcp", lanHost)
	req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, la))
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	return rec.Code, m
}

func notesURL(t *testing.T, f *fixture, slug, rel string) string {
	t.Helper()
	it, ok := f.store.BySlug(slug)
	if !ok {
		t.Fatal("no item", slug)
	}
	return "/_airs/notes/" + it.Key() + "/" + rel
}

const goodNote = `{"block":"b","index":0,"section":"","heading":"Notes","quote":"","text":"hello"}`

func fileText(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

func TestPostNote(t *testing.T) {
	f := newFixture(t)
	code, m := f.send(t, "POST", notesURL(t, f, "notes.md", ""), remote, goodNote, nil)
	if code != 201 || m["text"] != "hello" || m["id"] == "" {
		t.Fatalf("%d %v", code, m)
	}
	if !strings.Contains(fileText(t, filepath.Join(f.dir, "notes.notes.md")), "hello") {
		t.Fatal("not saved beside notes.md")
	}
	if code, _ := f.send(t, "POST", notesURL(t, f, "docs", "a.md"), remote, goodNote, nil); code != 201 {
		t.Fatal(code)
	}
	if !strings.Contains(fileText(t, filepath.Join(f.dir, "docs", "a.notes.md")), "hello") {
		t.Fatal("not saved beside docs/a.md")
	}
}

func TestPostNoteErrors(t *testing.T) {
	type tc struct {
		name  string
		setup func(f *fixture)
		slug  string
		rel   string
		from  string
		body  string
		hdr   map[string]string
		want  int
	}
	untrusted := func(f *fixture) { f.ns.Set(netinfo.Info{LocalIP: "192.168.1.20"}, false) }
	cases := []tc{
		{name: "unknown key", want: 410},
		{name: "untrusted network", setup: untrusted, want: 403},
		{name: "any item on untrusted network", setup: func(f *fixture) {
			untrusted(f)
			f.store.Add(filepath.Join(f.dir, "notes.md"), false, state.Flags{AnyNetwork: true})
		}, want: 403},
		{name: "untrusted but from the Mac", setup: untrusted, from: "127.0.0.1:5000", want: 201},
		{name: "config any_network", setup: func(f *fixture) {
			untrusted(f)
			f.h = NewHTTP(f.store, config.Config{Port: 7777, Notes: true, AnyNetwork: true}, f.ns, f.hub)
		}, want: 201},
		{name: "notes off in config", setup: func(f *fixture) {
			f.h = NewHTTP(f.store, config.Config{Port: 7777}, f.ns, f.hub)
		}, want: 403},
		{name: "notes off for item", setup: func(f *fixture) {
			f.store.Add(filepath.Join(f.dir, "notes.md"), false, state.Flags{NoNotes: true})
		}, want: 403},
		{name: "notes file itself", setup: func(f *fixture) {
			os.WriteFile(filepath.Join(f.dir, "docs", "x.notes.md"), []byte("# n\n"), 0o644)
		}, slug: "docs", rel: "x.notes.md", want: 403},
		{name: "text content type", hdr: map[string]string{"Content-Type": "text/plain"}, want: 415},
		{name: "foreign origin", hdr: map[string]string{"Origin": "http://evil.example"}, want: 403},
		{name: "null origin", hdr: map[string]string{"Origin": "null"}, want: 403},
		{name: "own origin", hdr: map[string]string{"Origin": "http://" + lanHost}, want: 201},
		{name: "json with charset", hdr: map[string]string{"Content-Type": "application/json; charset=utf-8"}, want: 201},
		{name: "text too long", body: `{"text":"` + strings.Repeat("x", 10001) + `"}`, want: 400},
		{name: "body too big", body: `{"text":"x","block":"` + strings.Repeat("b", 17<<10) + `"}`, want: 400},
		{name: "bad json", body: `{`, want: 400},
		{name: "empty", body: `{"text":"  ","quote":""}`, want: 400},
		{name: "image", slug: "docs", rel: "pic.png", want: 404},
		{name: "folder", slug: "docs", rel: "sub", want: 404},
		{name: "missing doc", slug: "docs", rel: "nope.md", want: 404},
		{name: "hidden", slug: "docs", rel: ".git/config", want: 404},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.setup != nil {
				c.setup(f)
			}
			slug, path := c.slug, ""
			if slug == "" {
				slug = "notes.md"
			}
			if c.name == "unknown key" {
				path = "/_airs/notes/nope-1234/"
			} else {
				path = notesURL(t, f, slug, c.rel)
			}
			from, body := c.from, c.body
			if from == "" {
				from = remote
			}
			if body == "" {
				body = goodNote
			}
			code, m := f.send(t, "POST", path, from, body, c.hdr)
			if code != c.want {
				t.Fatalf("got %d %v, want %d", code, m, c.want)
			}
			if code >= 400 && code != 410 && m["error"] == nil {
				t.Fatalf("no error message: %v", m)
			}
		})
	}
}

func TestPostNoteSymlinkedDoc(t *testing.T) {
	f := newFixture(t)
	real := filepath.Join(t.TempDir(), "real.md")
	os.WriteFile(real, []byte("# Real\n"), 0o644)
	link := filepath.Join(f.dir, "link.md")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f.store.Add(link, false, state.Flags{})
	if code, m := f.send(t, "POST", notesURL(t, f, "link.md", ""), remote, goodNote, nil); code != 201 {
		t.Fatal(code, m)
	}
	if !strings.Contains(fileText(t, filepath.Join(filepath.Dir(real), "real.notes.md")), "hello") {
		t.Fatal("notes not beside the symlink target")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "link.notes.md")); err == nil {
		t.Fatal("notes written beside the symlink")
	}
}

func TestPostNoteReadOnlyFolder(t *testing.T) {
	f := newFixture(t)
	os.Chmod(f.dir, 0o555)
	t.Cleanup(func() { os.Chmod(f.dir, 0o755) })
	code, m := f.send(t, "POST", notesURL(t, f, "notes.md", ""), remote, goodNote, nil)
	if code != 500 || m["error"] != "couldn't save note: permission denied" {
		t.Fatalf("%d %v", code, m)
	}
}

func TestPutNote(t *testing.T) {
	f := newFixture(t)
	u := notesURL(t, f, "notes.md", "")
	_, m := f.send(t, "POST", u, remote, goodNote, nil)
	id := m["id"].(string)
	put := func(was, text, id string) (int, map[string]any) {
		b, _ := json.Marshal(map[string]string{"id": id, "was": was, "text": text})
		return f.send(t, "PUT", u, remote, string(b), nil)
	}
	if code, m := put("hello", "changed", id); code != 200 || m["text"] != "changed" || m["edited"] == nil {
		t.Fatalf("%d %v", code, m)
	}
	if !strings.Contains(fileText(t, filepath.Join(f.dir, "notes.notes.md")), "changed") {
		t.Fatal("file not updated")
	}
	code, m := put("hello", "again", id)
	if code != 409 || m["note"].(map[string]any)["text"] != "changed" {
		t.Fatalf("stale was: %d %v", code, m)
	}
	if code, _ := put("", "x", "nope"); code != 404 {
		t.Fatal("unknown id", code)
	}
	if code, _ := put("changed", "", id); code != 200 {
		t.Fatal("empty text", code)
	}
	f.ns.Set(netinfo.Info{LocalIP: "192.168.1.20"}, false)
	if code, _ := put("", "y", id); code != 403 {
		t.Fatal("untrusted PUT", code)
	}
}

func TestPostNoteSymlinkedNotesFile(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(t.TempDir(), "authorized_keys")
	os.WriteFile(target, []byte("keep\n"), 0o600)
	os.Symlink(target, filepath.Join(f.dir, "docs", "a.notes.md"))
	if code, _ := f.send(t, "POST", notesURL(t, f, "docs", "a.md"), remote, goodNote, nil); code < 400 {
		t.Fatal("wrote through a symlinked notes file", code)
	}
	if fileText(t, target) != "keep\n" {
		t.Fatal("target changed")
	}
}

func TestNotesOfSymlinkedDocInFolder(t *testing.T) {
	f := newFixture(t)
	link := filepath.Join(f.dir, "docs", "link.md")
	if err := os.Symlink(filepath.Join(f.dir, "docs", "sub", "b.md"), link); err != nil {
		t.Fatal(err)
	}
	if code, m := f.send(t, "POST", notesURL(t, f, "docs", "link.md"), remote, goodNote, nil); code != 201 {
		t.Fatal(code, m)
	}
	if _, m := f.renderVia(t, "docs", "link.md", remote); len(noteTexts(m)) != 1 {
		t.Fatalf("note vanished: %v", m["notes"])
	}
	os.WriteFile(filepath.Join(f.dir, "docs", "y.notes.md"), []byte("# n\n"), 0o644)
	os.Symlink(filepath.Join(f.dir, "docs", "y.notes.md"), filepath.Join(f.dir, "docs", "x.md"))
	if _, m := f.renderVia(t, "docs", "x.md", remote); m["notesEnabled"] != false {
		t.Fatalf("link to a notes file offers notes: %v", m)
	}
}
