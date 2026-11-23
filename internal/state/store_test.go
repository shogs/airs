package state

import (
	"errors"
	"path/filepath"
	"testing"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func mustAdd(t *testing.T, s *Store, path string, dir bool) Item {
	t.Helper()
	it, _, err := s.Add(path, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestAddAssignsIncrementingIDs(t *testing.T) {
	s, _ := open(t)
	if a := mustAdd(t, s, "/x/a.md", false); a.ID != 1 {
		t.Fatalf("a=%d", a.ID)
	}
	if b := mustAdd(t, s, "/x/b.md", false); b.ID != 2 {
		t.Fatalf("b=%d", b.ID)
	}
	if _, err := s.Remove("1"); err != nil {
		t.Fatal(err)
	}
	if c := mustAdd(t, s, "/x/c.md", false); c.ID != 3 {
		t.Fatalf("c=%d", c.ID)
	}
	s.Remove("2")
	s.Remove("3")
	if d := mustAdd(t, s, "/x/d.md", false); d.ID != 1 {
		t.Fatalf("d=%d", d.ID)
	}
}

func TestAddExistingPathReturnsExisting(t *testing.T) {
	s, _ := open(t)
	a := mustAdd(t, s, "/x/a.md", false)
	b, existed, err := s.Add("/x/a.md", false, false)
	if err != nil || !existed || b.ID != a.ID {
		t.Fatalf("got %+v %v %v", b, existed, err)
	}
	c, existed, _ := s.Add("/x/a.md", false, true)
	if !existed || !c.AnyNetwork || !s.Items()[0].AnyNetwork {
		t.Fatalf("any not updated: %+v", c)
	}
}

func TestFindOrder(t *testing.T) {
	s, _ := open(t)
	mustAdd(t, s, "/abs/notes.md", false)
	mustAdd(t, s, "/p/docs", true)
	cases := map[string]int{"2": 2, "/abs/notes.md": 1, "notes.md": 1, "docs": 2, "docs/": 2}
	for q, want := range cases {
		it, err := s.Find(q)
		if err != nil || it.ID != want {
			t.Errorf("Find(%q) = %+v, %v; want id %d", q, it, err, want)
		}
	}
}

func TestFindNumericFallsThrough(t *testing.T) {
	s, _ := open(t)
	mustAdd(t, s, "/p/2024", true)
	mustAdd(t, s, "/p/x.md", false)
	// "2024" is not an existing ID, so it matches the basename.
	if it, err := s.Find("2024"); err != nil || it.ID != 1 {
		t.Fatalf("got %+v %v", it, err)
	}
}

func TestFindAmbiguous(t *testing.T) {
	s, _ := open(t)
	mustAdd(t, s, "/a/notes.md", false)
	mustAdd(t, s, "/b/notes.md", false)
	_, err := s.Find("notes.md")
	var amb *AmbiguousError
	if !errors.As(err, &amb) || len(amb.Matches) != 2 {
		t.Fatalf("got %v", err)
	}
}

func TestFindNotFound(t *testing.T) {
	s, _ := open(t)
	if _, err := s.Find("zzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.Remove("zzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove got %v", err)
	}
}

func TestPersistence(t *testing.T) {
	s, p := open(t)
	mustAdd(t, s, "/a/notes.md", false)
	if err := s.Trust(Network{Fingerprint: "fp1", Label: "home"}); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	items := s2.Items()
	if len(items) != 1 || items[0].Path != "/a/notes.md" || items[0].Slug != "notes.md" {
		t.Fatalf("items %+v", items)
	}
	if !s2.IsTrusted("fp1") || s2.TrustedLabel("fp1") != "home" {
		t.Fatal("trust not persisted")
	}
	if !s2.Untrust("fp1") || s2.IsTrusted("fp1") || s2.Untrust("fp1") {
		t.Fatal("untrust")
	}
}

func TestSlugs(t *testing.T) {
	s, _ := open(t)
	a := mustAdd(t, s, "/a/README.md", false)
	b := mustAdd(t, s, "/b/README.md", false)
	c := mustAdd(t, s, "/c/docs", true)
	d := mustAdd(t, s, "/x/_airs", true)
	if a.Slug != "README.md" || b.Slug != "README-2.md" || c.Slug != "docs" || d.Slug != "_airs-2" {
		t.Fatalf("slugs %q %q %q %q", a.Slug, b.Slug, c.Slug, d.Slug)
	}
	s.Remove("/a/README.md")
	if e := mustAdd(t, s, "/d/README.md", false); e.Slug != "README.md" {
		t.Fatalf("freed slug not reused: %q", e.Slug)
	}
	if got, ok := s.BySlug("README-2.md"); !ok || got.Path != "/b/README.md" {
		t.Fatalf("BySlug %+v %v", got, ok)
	}
}

func TestNameAndKey(t *testing.T) {
	if (Item{Path: "/p/docs", IsDir: true}).Name() != "docs/" || (Item{Path: "/p/a.md"}).Name() != "a.md" {
		t.Fatal("Name")
	}
	a := Item{ID: 1, Slug: "x.md", Path: "/a"}
	b := Item{ID: 1, Slug: "x.md", Path: "/b"}
	if a.Key() == b.Key() || a.Key() != a.Key() {
		t.Fatal("Key")
	}
}
