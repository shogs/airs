package project

import (
	"os"
	"path/filepath"
	"testing"
)

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestNameGitRepo(t *testing.T) {
	tmp := t.TempDir()
	mkdir(t, filepath.Join(tmp, "myrepo", ".git"))
	mkdir(t, filepath.Join(tmp, "myrepo", "docs", "plans"))
	if got := Name(filepath.Join(tmp, "myrepo", "docs", "plans")); got != "myrepo" {
		t.Fatalf("got %q", got)
	}
	if got := Name(filepath.Join(tmp, "myrepo")); got != "myrepo" {
		t.Fatalf("repo root: got %q", got)
	}
}

func TestNameWorktree(t *testing.T) {
	tmp := t.TempDir()
	mkdir(t, filepath.Join(tmp, "main", ".git", "worktrees", "wt"))
	wt := filepath.Join(tmp, "elsewhere", "wt")
	mkdir(t, filepath.Join(wt, "sub"))
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(tmp, "main", ".git", "worktrees", "wt")+"\n"), 0o644)
	if got := Name(filepath.Join(wt, "sub")); got != "main" {
		t.Fatalf("absolute gitdir: got %q", got)
	}
	// A relative gitdir resolves against the .git file's folder.
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: ../../main/.git/worktrees/wt\n"), 0o644)
	if got := Name(wt); got != "main" {
		t.Fatalf("relative gitdir: got %q", got)
	}
}

func TestNameBadGitFile(t *testing.T) {
	tmp := t.TempDir()
	mkdir(t, filepath.Join(tmp, "odd", "sub"))
	os.WriteFile(filepath.Join(tmp, "odd", ".git"), []byte("garbage"), 0o644)
	if got := Name(filepath.Join(tmp, "odd", "sub")); got != "odd" {
		t.Fatalf("got %q", got)
	}
}

func TestNameSubmoduleGitFile(t *testing.T) {
	// A gitdir that isn't under .../worktrees/ (e.g. a submodule) names the folder holding .git.
	tmp := t.TempDir()
	mkdir(t, filepath.Join(tmp, "super", ".git", "modules", "lib"))
	mkdir(t, filepath.Join(tmp, "super", "lib"))
	os.WriteFile(filepath.Join(tmp, "super", "lib", ".git"), []byte("gitdir: ../.git/modules/lib\n"), 0o644)
	if got := Name(filepath.Join(tmp, "super", "lib")); got != "lib" {
		t.Fatalf("got %q", got)
	}
}

func TestNameNoRepo(t *testing.T) {
	tmp := t.TempDir()
	mkdir(t, filepath.Join(tmp, "plain", "folder"))
	if got := Name(filepath.Join(tmp, "plain", "folder")); got != "folder" {
		t.Fatalf("got %q", got)
	}
}

func TestNameMissingDir(t *testing.T) {
	tmp := t.TempDir()
	mkdir(t, filepath.Join(tmp, "myrepo", ".git"))
	if got := Name(filepath.Join(tmp, "myrepo", "gone", "deeper")); got != "myrepo" {
		t.Fatalf("got %q", got)
	}
}

func TestNameInvalidFallsBack(t *testing.T) {
	if got := Name("/"); got != Fallback || Fallback != "files" {
		t.Fatalf("got %q", got)
	}
}

func TestValid(t *testing.T) {
	for _, ok := range []string{"airreader", "my notes", "日本", "a.b"} {
		if err := Valid(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "_airs", "a/b", ".", ".."} {
		if Valid(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
