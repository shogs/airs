// Package watch turns filesystem changes into per-item live-reload events.
package watch

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Kind is the type of an Event.
type Kind string

const (
	Change Kind = "change" // the item's content changed
	Gone   Kind = "gone"   // the item is no longer served
)

// Event is delivered to subscribers of an item.
type Event struct{ Kind Kind }

type entry struct {
	root  string // file path or folder path
	isDir bool
	dirs  []string // fsnotify watches held by this entry
	timer *time.Timer
}

// Hub watches served items and fans out debounced events.
type Hub struct {
	debounce time.Duration
	w        *fsnotify.Watcher

	mu     sync.Mutex
	items  map[int]*entry
	refs   map[string]int // watched dir -> number of entries using it
	subs   map[int]map[chan Event]struct{}
	closed bool
}

// New starts a hub.
func New(debounce time.Duration) (*Hub, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	h := &Hub{debounce: debounce, w: w, items: map[int]*entry{}, refs: map[string]int{}, subs: map[int]map[chan Event]struct{}{}}
	go h.loop()
	return h, nil
}

func hidden(name string) bool { return strings.HasPrefix(name, ".") }

func (h *Hub) addDir(e *entry, dir string) {
	if h.refs[dir] == 0 {
		if err := h.w.Add(dir); err != nil {
			return
		}
	}
	h.refs[dir]++
	e.dirs = append(e.dirs, dir)
}

func (h *Hub) addTree(e *entry, root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != root && hidden(d.Name()) {
			return filepath.SkipDir
		}
		h.addDir(e, p)
		return nil
	})
}

// Watch starts watching an item. Single files are watched through their parent
// directory so editors that save by rename keep working.
func (h *Hub) Watch(id int, root string, isDir bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.items[id]; ok {
		return nil
	}
	e := &entry{root: root, isDir: isDir}
	if isDir {
		if _, err := os.Stat(root); err != nil {
			return err
		}
		h.addTree(e, root)
	} else {
		h.addDir(e, filepath.Dir(root))
	}
	h.items[id] = e
	return nil
}

func (h *Hub) release(e *entry) {
	if e.timer != nil {
		e.timer.Stop()
	}
	for _, d := range e.dirs {
		h.refs[d]--
		if h.refs[d] <= 0 {
			delete(h.refs, d)
			h.w.Remove(d)
		}
	}
}

func sendGone(ch chan Event) {
	for len(ch) > 0 { // drop a pending change so gone always fits
		select {
		case <-ch:
		default:
		}
	}
	ch <- Event{Kind: Gone}
	close(ch)
}

// Unwatch stops watching id and sends Gone to its subscribers.
func (h *Hub) Unwatch(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.items[id]; ok {
		h.release(e)
		delete(h.items, id)
	}
	for ch := range h.subs[id] {
		sendGone(ch)
	}
	delete(h.subs, id)
}

// Subscribe returns a channel of events for id and a cancel function.
func (h *Hub) Subscribe(id int) (<-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan Event, 1)
	if _, ok := h.items[id]; !ok || h.closed {
		sendGone(ch)
		return ch, func() {}
	}
	if h.subs[id] == nil {
		h.subs[id] = map[chan Event]struct{}{}
	}
	h.subs[id][ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[id][ch]; ok {
			delete(h.subs[id], ch)
			close(ch)
		}
	}
}

// Close sends Gone to everyone and stops the hub.
func (h *Hub) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	for id, e := range h.items {
		h.release(e)
		for ch := range h.subs[id] {
			sendGone(ch)
		}
	}
	h.items, h.subs = map[int]*entry{}, map[int]map[chan Event]struct{}{}
	return h.w.Close()
}

func (h *Hub) loop() {
	for {
		select {
		case ev, ok := <-h.w.Events:
			if !ok {
				return
			}
			h.handle(ev)
		case _, ok := <-h.w.Errors:
			if !ok {
				return
			}
		}
	}
}

func (h *Hub) handle(ev fsnotify.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, e := range h.items {
		if !e.matches(ev.Name) {
			continue
		}
		if e.isDir && ev.Has(fsnotify.Create) {
			if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
				h.addTree(e, ev.Name)
			}
		}
		h.schedule(id, e)
	}
}

func (e *entry) matches(p string) bool {
	if !e.isDir {
		return p == e.root
	}
	rel, err := filepath.Rel(e.root, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if hidden(seg) {
			return false
		}
	}
	return true
}

func (h *Hub) schedule(id int, e *entry) {
	if e.timer != nil {
		e.timer.Stop()
	}
	e.timer = time.AfterFunc(h.debounce, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.items[id] != e {
			return
		}
		for ch := range h.subs[id] {
			select {
			case ch <- Event{Kind: Change}:
			default: // a change is already pending
			}
		}
	})
}
