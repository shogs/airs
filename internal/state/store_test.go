package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	it, _, err := s.Add(path, dir, Flags{Project: "p"})
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
	b, existed, err := s.Add("/x/a.md", false, Flags{})
	if err != nil || !existed || b.ID != a.ID {
		t.Fatalf("got %+v %v %v", b, existed, err)
	}
	c, existed, _ := s.Add("/x/a.md", false, Flags{AnyNetwork: true})
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
	if got, ok := s.ByLink("p", "README-2.md"); !ok || got.Path != "/b/README.md" {
		t.Fatalf("ByLink %+v %v", got, ok)
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

func TestAddFlags(t *testing.T) {
	s, p := open(t)
	it, _, err := s.Add("/x/a.md", false, Flags{NoNotes: true})
	if err != nil || !it.NoNotes {
		t.Fatal(it, err)
	}
	it, existed, _ := s.Add("/x/a.md", false, Flags{})
	if !existed || !it.NoNotes {
		t.Fatalf("plain re-add cleared NoNotes: %+v", it)
	}
	it, _, _ = s.Add("/x/a.md", false, Flags{AnyNetwork: true})
	if !it.AnyNetwork || !it.NoNotes {
		t.Fatalf("%+v", it)
	}
	s2, _ := Open(p)
	if got := s2.Items()[0]; !got.NoNotes || !got.AnyNetwork {
		t.Fatalf("not persisted: %+v", got)
	}
}

func TestSlugsPerProject(t *testing.T) {
	s, _ := open(t)
	a, _, _ := s.Add("/a/x.md", false, Flags{Project: "p"})
	b, _, _ := s.Add("/b/x.md", false, Flags{Project: "p"})
	c, _, _ := s.Add("/c/x.md", false, Flags{Project: "q"})
	if a.Slug != "x.md" || b.Slug != "x-2.md" || c.Slug != "x.md" {
		t.Fatalf("slugs %q %q %q", a.Slug, b.Slug, c.Slug)
	}
}

func TestAddProject(t *testing.T) {
	s, _ := open(t)
	it, _, _ := s.Add("/a/x.md", false, Flags{Project: "proj"})
	if it.Project != "proj" {
		t.Fatalf("%+v", it)
	}
}

func TestAddWithoutProjectNamesOne(t *testing.T) {
	s, _ := open(t)
	it, _, _ := s.Add("/nowhere/folder/x.md", false, Flags{})
	if it.Project != "folder" {
		t.Fatalf("%+v", it)
	}
}

func TestReAddKeepsProject(t *testing.T) {
	s, _ := open(t)
	s.Add("/a/x.md", false, Flags{Project: "p"})
	it, existed, _ := s.Add("/a/x.md", false, Flags{Project: "other"})
	if !existed || it.Project != "p" {
		t.Fatalf("%+v", it)
	}
}

func TestReAddMovesProject(t *testing.T) {
	s, p := open(t)
	s.Add("/b/x.md", false, Flags{Project: "q"})
	old, _, _ := s.Add("/a/x.md", false, Flags{Project: "p"})
	it, existed, _ := s.Add("/a/x.md", false, Flags{Project: "q", MoveProject: true})
	if !existed || it.Project != "q" || it.Slug != "x-2.md" || it.Key() == old.Key() {
		t.Fatalf("%+v", it)
	}
	if _, ok := s.ByLink("p", "x.md"); ok {
		t.Fatal("old link still found")
	}
	s2, _ := Open(p)
	if got, ok := s2.ByLink("q", "x-2.md"); !ok || got.Path != "/a/x.md" {
		t.Fatalf("not persisted: %+v", got)
	}
}

func TestReAddNotesDir(t *testing.T) {
	s, _ := open(t)
	s.Add("/a/x.md", false, Flags{Project: "p"})
	it, _, _ := s.Add("/a/x.md", false, Flags{NotesDir: "/n"})
	if it.NotesDir != "/n" {
		t.Fatalf("%+v", it)
	}
	it, _, _ = s.Add("/a/x.md", false, Flags{})
	if it.NotesDir != "/n" {
		t.Fatalf("plain re-add cleared NotesDir: %+v", it)
	}
}

func TestByLink(t *testing.T) {
	s, _ := open(t)
	s.Add("/a/x.md", false, Flags{Project: "p"})
	if _, ok := s.ByLink("p", "x.md"); !ok {
		t.Fatal("not found")
	}
	if _, ok := s.ByLink("q", "x.md"); ok {
		t.Fatal("found under the wrong project")
	}
}

func TestKeyIncludesProject(t *testing.T) {
	a := Item{Slug: "x.md", Path: "/a", Project: "p"}
	b := Item{Slug: "x.md", Path: "/a", Project: "q"}
	if a.Key() == b.Key() {
		t.Fatal("same key")
	}
}

func TestMigrationAddsProject(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	os.MkdirAll(filepath.Join(repo, "docs"), 0o755)
	os.WriteFile(filepath.Join(repo, "doc.md"), []byte("# d\n"), 0o644)
	p := filepath.Join(tmp, "state.json")
	old := `{"items":[
 {"id":1,"path":"` + filepath.Join(repo, "doc.md") + `","is_dir":false,"any_network":false,"slug":"doc.md"},
 {"id":2,"path":"` + filepath.Join(repo, "docs") + `","is_dir":true,"any_network":false,"slug":"docs"},
 {"id":3,"path":"` + filepath.Join(repo, "gone", "doc.md") + `","is_dir":false,"any_network":false,"slug":"doc-2.md"},
 {"id":4,"path":"` + filepath.Join(repo, "other", "doc.md") + `","is_dir":false,"any_network":false,"slug":"doc.md"}
],"trusted":[]}`
	os.WriteFile(p, []byte(old), 0o600)
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	items := s.Items()
	for _, it := range items {
		if it.Project != "repo" {
			t.Fatalf("item %d project %q", it.ID, it.Project)
		}
	}
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.Slug] {
			t.Fatalf("duplicate slug %q", it.Slug)
		}
		seen[it.Slug] = true
	}
	if items[0].Slug != "doc.md" {
		t.Fatalf("lowest id should keep its slug: %+v", items[0])
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"project": "repo"`) {
		t.Fatalf("not saved: %s", b)
	}
}

func TestContaining(t *testing.T) {
	s, _ := open(t)
	s.Add("/r/docs", true, Flags{Project: "p"})
	s.Add("/r/a.md", false, Flags{Project: "p"})
	it, rel, ok := s.Containing("/r/docs/a/b.md")
	if !ok || it.Path != "/r/docs" || rel != "a/b.md" {
		t.Fatalf("%+v %q %v", it, rel, ok)
	}
	if _, _, ok := s.Containing("/r/docsx/a.md"); ok {
		t.Fatal("prefix match")
	}
	if _, _, ok := s.Containing("/r/a.md"); ok {
		t.Fatal("file item counted as a folder")
	}
}
