// Package project names the project a share belongs to: the git repository a
// folder is in, or the folder itself when it isn't in one.
package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Fallback is the project used when no valid name can be worked out.
const Fallback = "files"

// Valid reports whether name can be a project, i.e. a single URL path segment
// that isn't airs' reserved prefix.
func Valid(name string) error {
	switch {
	case name == "":
		return errors.New("project name is empty")
	case name == "_airs":
		return errors.New("_airs is reserved and can't be a project name")
	case name == "." || name == "..":
		return errors.New("project name can't be . or ..")
	case strings.Contains(name, "/"):
		return errors.New("project name can't contain /")
	}
	return nil
}

// Name is the name of the git repository containing dir (the main repository
// for a worktree), or dir's own name when it isn't in a repository. dir need
// not exist; its parents are searched all the same.
func Name(dir string) string {
	dir = filepath.Clean(dir)
	n := filepath.Base(dir)
	for d := dir; ; d = filepath.Dir(d) {
		if fi, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			n = filepath.Base(d)
			if fi.Mode().IsRegular() {
				if main, ok := worktreeMain(d); ok {
					n = main
				}
			}
			break
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	if Valid(n) != nil {
		return Fallback
	}
	return n
}

// worktreeMain reads the .git file in dir and, when it points into a main
// repository's .git/worktrees, returns that repository's name.
func worktreeMain(dir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return "", false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return "", false
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	gitdir = filepath.Clean(gitdir)
	if filepath.Base(filepath.Dir(gitdir)) != "worktrees" {
		return "", false
	}
	common := filepath.Dir(filepath.Dir(gitdir)) // <main>/.git
	return filepath.Base(filepath.Dir(common)), true
}
