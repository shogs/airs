package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"airs/internal/config"
	"airs/internal/netinfo"
	"airs/internal/notes"
	"airs/internal/state"
)

// renderVia fetches the live render JSON for slug/rel as `from` would.
func (f *fixture) renderVia(t *testing.T, slug, rel, from string) (int, map[string]any) {
	t.Helper()
	it, ok := f.store.ByLink("p", slug)
	if !ok {
		t.Fatal("no item", slug)
	}
	code, _, body := f.get(t, "/_airs/render/"+it.Key()+"/"+rel, from)
	var m map[string]any
	json.Unmarshal([]byte(body), &m)
	return code, m
}

func addNote(t *testing.T, doc, text string) {
	t.Helper()
	var w notes.Writer
	if _, err := w.Append(notes.Path(doc), filepath.Base(doc), notes.Input{Block: "b", Text: text}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func noteTexts(m map[string]any) []string {
	var out []string
	list, _ := m["notes"].([]any)
	for _, n := range list {
		out = append(out, n.(map[string]any)["text"].(string))
	}
	return out
}

func TestCanWrite(t *testing.T) {
	for _, c := range []struct {
		cfg            config.Config
		trusted, local bool
		want           bool
	}{
		{config.Config{}, false, true, true},
		{config.Config{}, true, false, true},
		{config.Config{AnyNetwork: true}, false, false, true},
		{config.Config{}, false, false, false},
	} {
		if got := CanWrite(c.cfg, c.trusted, c.local); got != c.want {
			t.Errorf("%+v: got %v", c, got)
		}
	}
}

func TestRenderJSONIncludesNotes(t *testing.T) {
	f := newFixture(t)
	code, m := f.renderVia(t, "notes.md", "", remote)
	if code != 200 || m["notesEnabled"] != true || m["notes"] == nil || len(noteTexts(m)) != 0 {
		t.Fatalf("no notes yet: %d %v", code, m)
	}
	addNote(t, filepath.Join(f.dir, "notes.md"), "hello note")
	_, m = f.renderVia(t, "notes.md", "", remote)
	if got := noteTexts(m); len(got) != 1 || got[0] != "hello note" {
		t.Fatalf("%v", m["notes"])
	}
	_, _, page := f.get(t, "/p/notes.md", remote)
	if !strings.Contains(page, `"notesEnabled":true`) || !strings.Contains(page, "hello note") {
		t.Fatal("page doc JSON lacks notes")
	}
	addNote(t, filepath.Join(f.dir, "docs", "a.md"), "folder note")
	_, m = f.renderVia(t, "docs", "a.md", remote)
	if got := noteTexts(m); len(got) != 1 || got[0] != "folder note" {
		t.Fatalf("%v", m)
	}
}

func TestNotesReadOnlyForAnyItemOnUntrustedNetwork(t *testing.T) {
	f := newFixture(t)
	addNote(t, filepath.Join(f.dir, "notes.md"), "kept")
	f.store.Add(filepath.Join(f.dir, "notes.md"), false, state.Flags{AnyNetwork: true})
	f.ns.Set(netinfo.Info{LocalIP: "192.168.1.20"}, false)
	code, m := f.renderVia(t, "notes.md", "", remote)
	if code != 200 || m["notesEnabled"] != false || len(noteTexts(m)) != 1 {
		t.Fatalf("remote: %d %v", code, m)
	}
	_, m = f.renderVia(t, "notes.md", "", "127.0.0.1:5000")
	if m["notesEnabled"] != true {
		t.Fatalf("from the Mac: %v", m)
	}
}

func TestNotesDisabled(t *testing.T) {
	f := newFixture(t)
	f.h = NewHTTP(f.store, config.Config{Port: 7777}, f.ns, f.hub) // notes = false
	if _, m := f.renderVia(t, "notes.md", "", remote); m["notesEnabled"] != false {
		t.Fatalf("config off: %v", m)
	}
	f = newFixture(t)
	f.store.Add(filepath.Join(f.dir, "notes.md"), false, state.Flags{NoNotes: true})
	if _, m := f.renderVia(t, "notes.md", "", remote); m["notesEnabled"] != false {
		t.Fatalf("item off: %v", m)
	}
	os.WriteFile(filepath.Join(f.dir, "docs", "x.notes.md"), []byte("# n\n"), 0o644)
	if code, m := f.renderVia(t, "docs", "x.notes.md", remote); code != 200 || m["notesEnabled"] != false {
		t.Fatalf("notes file page: %d %v", code, m)
	}
}

func TestPagesHaveLivePill(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/p/notes.md", "/p/docs/", "/p/docs/a.md"} {
		if _, _, body := f.get(t, p, remote); !strings.Contains(body, `id="airs-live"`) {
			t.Errorf("%s has no live pill", p)
		}
	}
}
