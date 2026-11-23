package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"airs/internal/config"
	"airs/internal/state"
)

func mkfile(t *testing.T, p string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDir(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "a.md"))
	mkfile(t, filepath.Join(root, "sub", "b.md"))
	mkfile(t, filepath.Join(root, "My Notes", "Été.md"))
	it := state.Item{Path: root, IsDir: true}
	for _, rel := range []string{"a.md", "sub/b.md", "My Notes/Été.md", "", "sub"} {
		if _, err := Resolve(it, rel); err != nil {
			t.Errorf("%q: %v", rel, err)
		}
	}
}

func TestResolveEscapes(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "sub", "b.md"))
	mkfile(t, filepath.Join(root, ".git", "config"))
	mkfile(t, filepath.Join(root, ".hidden.md"))
	mkfile(t, filepath.Join(filepath.Dir(root), "x"))
	it := state.Item{Path: root, IsDir: true}
	for _, rel := range []string{"../x", "sub/../../x", ".git/config", ".hidden.md", "/etc/passwd"} {
		if _, err := Resolve(it, rel); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: got %v", rel, err)
		}
	}
}

func TestResolveSymlinkOut(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mkfile(t, filepath.Join(outside, "secret.md"))
	os.Symlink(outside, filepath.Join(root, "link"))
	if _, err := Resolve(state.Item{Path: root, IsDir: true}, "link/secret.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestResolveSingleFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "notes.md")
	mkfile(t, f)
	mkfile(t, filepath.Join(dir, "img.png"))
	mkfile(t, filepath.Join(dir, "secret.txt"))
	mkfile(t, filepath.Join(filepath.Dir(dir), "img.png"))
	it := state.Item{Path: f}
	if p, err := Resolve(it, ""); err != nil || p != f {
		t.Errorf("file: %q %v", p, err)
	}
	if _, err := Resolve(it, "img.png"); err != nil {
		t.Errorf("img: %v", err)
	}
	for _, rel := range []string{"secret.txt", "../img.png", "notes.md"} {
		if _, err := Resolve(it, rel); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: got %v", rel, err)
		}
	}
}

func TestAllowed(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		loop, trusted, cfgAny, itemAny := mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0
		got := Allowed(state.Item{AnyNetwork: itemAny}, config.Config{AnyNetwork: cfgAny}, trusted, loop)
		if got != (mask != 0) {
			t.Errorf("mask %04b: got %v", mask, got)
		}
	}
}
