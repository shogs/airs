package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"airs/internal/render"
	"airs/internal/state"
)

// ErrNotFound means the path is unknown or outside what was shared.
var ErrNotFound = errors.New("not found")

// Resolve maps a slash-separated, URL-decoded path inside item to an absolute
// path, refusing anything outside the item (.., symlinks out, hidden names).
// For a single-file item, rel "" is the file itself and anything else must be
// an image beside it.
func Resolve(item state.Item, rel string) (string, error) {
	if strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") || strings.ContainsRune(rel, 0) {
		return "", ErrNotFound
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." || strings.HasPrefix(seg, ".") {
			return "", ErrNotFound
		}
	}
	root := item.Path
	if !item.IsDir {
		if rel == "" {
			return item.Path, nil
		}
		if !render.IsImage(rel) {
			return "", ErrNotFound
		}
		root = filepath.Dir(item.Path)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", ErrNotFound
	}
	cand := filepath.Join(root, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(cand)
	if err != nil {
		return "", ErrNotFound
	}
	if real != realRoot && !strings.HasPrefix(real, realRoot+string(os.PathSeparator)) {
		return "", ErrNotFound
	}
	return cand, nil
}
