// Package watch turns filesystem changes into per-item live-reload events.
//
// It uses macOS FSEvents: one stream per item, recursive, path based. That
// survives editors that save by rename, folders that are deleted and
// recreated, and items whose path does not exist yet, and it needs no file
// descriptor per watched file (kqueue does, which exhausts the limit on big
// folders such as a repo with node_modules).
package watch

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsevents"
)

// Kind is the type of an Event.
type Kind string

const (
	Change Kind = "change" // the item's content changed
	Gone   Kind = "gone"   // the item is no longer served
)

// Event is delivered to subscribers of an item.
type Event struct{ Kind Kind }

const streamLatency = 50 * time.Millisecond

type entry struct {
	root    string   // canonical (symlink-resolved) file or folder path
	also    []string // canonical extra files that count as the item (e.g. its notes file)
	isDir   bool
	stream  *fsevents.EventStream
	startID uint64
	started time.Time
	done    chan struct{}
	timer   *time.Timer
}

// Hub watches served items and fans out debounced events.
type Hub struct {
	debounce time.Duration

	mu     sync.Mutex
	items  map[int]*entry
	subs   map[int]map[chan Event]struct{}
	closed bool
}

// New starts a hub.
func New(debounce time.Duration) (*Hub, error) {
	return &Hub{debounce: debounce, items: map[int]*entry{}, subs: map[int]map[chan Event]struct{}{}}, nil
}

// canonical resolves symlinks in p, or in its nearest existing ancestor when p
// does not exist yet, because FSEvents reports resolved paths
// (/private/var/... for /var/...).
func canonical(p string) string {
	p = filepath.Clean(p)
	var rest []string
	for cur := p; ; cur = filepath.Dir(cur) {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{r}, rest...)...)
		}
		if filepath.Dir(cur) == cur {
			return p
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
	}
}

// Watch starts watching an item. A missing path is fine: events start when it
// appears. Changes to the also files count as changes to the item.
func (h *Hub) Watch(id int, root string, isDir bool, also ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.watchLocked(id, root, isDir, also)
}

// Rewatch replaces what an item watches (e.g. its notes file moved). Open
// subscriptions stay connected and aren't told the item is gone.
func (h *Hub) Rewatch(id int, root string, isDir bool, also ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.items[id]; ok {
		e.release()
		delete(h.items, id)
	}
	return h.watchLocked(id, root, isDir, also)
}

func (h *Hub) watchLocked(id int, root string, isDir bool, also []string) error {
	if _, ok := h.items[id]; ok || h.closed {
		return nil
	}
	// Start from the current event ID: FSEvents can otherwise deliver changes
	// made just before the stream started (e.g. the file's own creation).
	start := fsevents.LatestEventID()
	e := &entry{root: canonical(root), isDir: isDir, done: make(chan struct{}), startID: start, started: time.Now()}
	for _, a := range also {
		e.also = append(e.also, canonical(a))
	}
	e.stream = &fsevents.EventStream{
		Paths:   append([]string{e.root}, e.also...),
		Latency: streamLatency,
		Flags:   fsevents.FileEvents | fsevents.WatchRoot,
		Resume:  true,
		EventID: start,
	}
	if err := e.stream.Start(); err != nil {
		return err
	}
	h.items[id] = e
	go h.pump(id, e)
	return nil
}

func (h *Hub) pump(id int, e *entry) {
	for {
		select {
		case <-e.done:
			return
		case msgs, ok := <-e.stream.Events:
			if !ok {
				return
			}
			for _, m := range msgs {
				if m.Flags&fsevents.HistoryDone != 0 || (m.ID != 0 && m.ID <= e.startID) {
					continue
				}
				if e.matches(m.Path) && e.changedSinceStart(m.Path, m.Flags) {
					h.schedule(id, e)
					break
				}
			}
		}
	}
}

// changedSinceStart filters out events FSEvents delivers late for changes made
// before the stream started. A missing path is a removal: always a change.
// A file must have changed (ctime) after watching began. A folder's own
// event counts only if it was created (birth time) or renamed into place after
// that; changes inside it arrive as their own events, and its ctime also moves
// when hidden files such as .DS_Store are written.
func (e *entry) changedSinceStart(p string, flags fsevents.EventFlags) bool {
	var st syscall.Stat_t
	if err := syscall.Lstat(p, &st); err != nil {
		return true
	}
	t := st.Ctimespec
	if st.Mode&syscall.S_IFMT == syscall.S_IFDIR && flags&fsevents.ItemRenamed == 0 {
		t = st.Birthtimespec
	}
	return !time.Unix(t.Unix()).Before(e.started)
}

func hidden(name string) bool { return strings.HasPrefix(name, ".") }

func (e *entry) matches(p string) bool {
	p = filepath.Clean(p)
	if p == e.root || slices.Contains(e.also, p) {
		return true
	}
	if !e.isDir {
		return false
	}
	rel, err := filepath.Rel(e.root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
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
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.items[id] != e {
		return
	}
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

func (e *entry) release() {
	if e.timer != nil {
		e.timer.Stop()
	}
	close(e.done)
	e.stream.Stop()
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
		e.release()
		delete(h.items, id)
	}
	for ch := range h.subs[id] {
		sendGone(ch)
	}
	delete(h.subs, id)
}

// Subscribe returns a channel of events for id and a cancel function. An
// unknown item yields Gone immediately.
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
		e.release()
		for ch := range h.subs[id] {
			sendGone(ch)
		}
	}
	h.items, h.subs = map[int]*entry{}, map[int]map[chan Event]struct{}{}
	return nil
}
