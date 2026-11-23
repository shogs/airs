// Package state persists the served items and trusted networks.
package state

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ErrNotFound is returned when no item matches a query.
var ErrNotFound = errors.New("not found")

// ReservedSlug is the URL prefix airs uses for its own endpoints.
const ReservedSlug = "_airs"

// AmbiguousError is returned when a name matches more than one item.
type AmbiguousError struct{ Matches []Item }

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%d items match; use the id or full path", len(e.Matches))
}

// Item is a served file or folder.
type Item struct {
	ID         int    `json:"id"`
	Path       string `json:"path"`
	IsDir      bool   `json:"is_dir"`
	AnyNetwork bool   `json:"any_network"`
	NoNotes    bool   `json:"no_notes,omitempty"`
	Slug       string `json:"slug"`
}

// Flags are the per-item options set when adding.
type Flags struct{ AnyNetwork, NoNotes bool }

// Name is the basename, with a trailing slash for folders.
func (it Item) Name() string {
	n := filepath.Base(it.Path)
	if it.IsDir {
		n += "/"
	}
	return n
}

// Key identifies this exact item (slug + path), so a reused slug gets a new key.
func (it Item) Key() string {
	h := sha256.Sum256([]byte(it.Path))
	return fmt.Sprintf("%s-%x", it.Slug, h[:4])
}

// Network is a trusted network.
type Network struct {
	Fingerprint string `json:"fingerprint"`
	Label       string `json:"label,omitempty"`
}

type data struct {
	Items   []Item    `json:"items"`
	Trusted []Network `json:"trusted"`
}

// Store is a mutex-guarded, file-backed state store.
type Store struct {
	mu   sync.Mutex
	path string
	d    data
}

// Open loads path; a missing file yields an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.d); err != nil {
		return nil, fmt.Errorf("state %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Items returns a copy of the items sorted by ID.
func (s *Store) Items() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Item(nil), s.d.Items...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Add adds absPath, or returns the existing item (updating AnyNetwork when any is set).
func (s *Store) Add(absPath string, isDir bool, f Flags) (Item, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, it := range s.d.Items {
		if it.Path == absPath {
			// Flags given on a re-add are turned on; omitted ones are left alone.
			if (f.AnyNetwork && !it.AnyNetwork) || (f.NoNotes && !it.NoNotes) {
				s.d.Items[i].AnyNetwork = it.AnyNetwork || f.AnyNetwork
				s.d.Items[i].NoNotes = it.NoNotes || f.NoNotes
				if err := s.save(); err != nil {
					return Item{}, true, err
				}
			}
			return s.d.Items[i], true, nil
		}
	}
	id := 1
	for _, it := range s.d.Items {
		if it.ID >= id {
			id = it.ID + 1
		}
	}
	it := Item{ID: id, Path: absPath, IsDir: isDir, AnyNetwork: f.AnyNetwork, NoNotes: f.NoNotes, Slug: s.uniqueSlug(filepath.Base(absPath))}
	s.d.Items = append(s.d.Items, it)
	return it, false, s.save()
}

func (s *Store) uniqueSlug(base string) string {
	taken := func(c string) bool {
		if c == ReservedSlug {
			return true
		}
		for _, it := range s.d.Items {
			if it.Slug == c {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" { // dotfile-like names such as ".md"
		stem, ext = base, ""
	}
	for n := 2; ; n++ {
		c := fmt.Sprintf("%s-%d%s", stem, n, ext)
		if !taken(c) {
			return c
		}
	}
}

// BySlug finds an item by its URL name.
func (s *Store) BySlug(slug string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.d.Items {
		if it.Slug == slug {
			return it, true
		}
	}
	return Item{}, false
}

// Find matches query by exact id, then exact path, then basename.
func (s *Store) Find(query string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, it, err := s.find(query)
	return it, err
}

func (s *Store) find(q string) (int, Item, error) {
	if id, err := strconv.Atoi(q); err == nil {
		for i, it := range s.d.Items {
			if it.ID == id {
				return i, it, nil
			}
		}
	}
	for i, it := range s.d.Items {
		if it.Path == q {
			return i, it, nil
		}
	}
	name := strings.TrimSuffix(q, "/")
	var idx []int
	for i, it := range s.d.Items {
		if filepath.Base(it.Path) == name {
			idx = append(idx, i)
		}
	}
	switch len(idx) {
	case 0:
		return -1, Item{}, ErrNotFound
	case 1:
		return idx[0], s.d.Items[idx[0]], nil
	}
	amb := &AmbiguousError{}
	for _, i := range idx {
		amb.Matches = append(amb.Matches, s.d.Items[i])
	}
	return -1, Item{}, amb
}

// Remove deletes the item matching query.
func (s *Store) Remove(query string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, it, err := s.find(query)
	if err != nil {
		return Item{}, err
	}
	s.d.Items = append(s.d.Items[:i], s.d.Items[i+1:]...)
	return it, s.save()
}

// Trust records n as trusted (updating its label if already trusted).
func (s *Store) Trust(n Network) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.d.Trusted {
		if t.Fingerprint == n.Fingerprint {
			s.d.Trusted[i].Label = n.Label
			return s.save()
		}
	}
	s.d.Trusted = append(s.d.Trusted, n)
	return s.save()
}

// Untrust removes a trusted network; it reports whether it was trusted.
func (s *Store) Untrust(fp string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.d.Trusted {
		if t.Fingerprint == fp {
			s.d.Trusted = append(s.d.Trusted[:i], s.d.Trusted[i+1:]...)
			s.save()
			return true
		}
	}
	return false
}

// IsTrusted reports whether fp is a trusted network.
func (s *Store) IsTrusted(fp string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.d.Trusted {
		if t.Fingerprint == fp {
			return true
		}
	}
	return false
}

// TrustedLabel returns the label of a trusted network, or "".
func (s *Store) TrustedLabel(fp string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.d.Trusted {
		if t.Fingerprint == fp {
			return t.Label
		}
	}
	return ""
}
