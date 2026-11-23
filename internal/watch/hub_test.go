package watch

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const deb = 50 * time.Millisecond

func newHub(t *testing.T) *Hub {
	t.Helper()
	h, err := New(deb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func expect(t *testing.T, ch <-chan Event, kind Kind) {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatalf("channel closed, want %s", kind)
		}
		if e.Kind != kind {
			t.Fatalf("got %s want %s", e.Kind, kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for %s", kind)
	}
}

func expectNone(t *testing.T, ch <-chan Event, d time.Duration) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("unexpected event %v", e)
	case <-time.After(d):
	}
}

func TestFileWriteEmitsChange(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "notes.md")
	write(t, f, "a")
	h := newHub(t)
	if err := h.Watch(1, f, false); err != nil {
		t.Fatal(err)
	}
	ch, cancel := h.Subscribe(1)
	defer cancel()
	write(t, f, "b")
	write(t, f, "c")
	write(t, f, "d")
	expect(t, ch, Change)
	// A burst is coalesced; FSEvents may deliver a straggler in a later batch,
	// which costs one extra re-render at most.
	n := 0
	deadline := time.After(500 * time.Millisecond)
	for done := false; !done; {
		select {
		case <-ch:
			n++
		case <-deadline:
			done = true
		}
	}
	if n > 1 {
		t.Fatalf("burst of 3 writes produced %d extra events", n)
	}
}

func TestAtomicRenameSaveRepeated(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "notes.md")
	write(t, f, "a")
	h := newHub(t)
	h.Watch(1, f, false)
	ch, cancel := h.Subscribe(1)
	defer cancel()
	for i := 0; i < 2; i++ {
		tmp := filepath.Join(dir, ".notes.md.swp")
		write(t, tmp, "v")
		if err := os.Rename(tmp, f); err != nil {
			t.Fatal(err)
		}
		expect(t, ch, Change)
		time.Sleep(200 * time.Millisecond)
	}
}

func TestSiblingChangeIgnored(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "notes.md")
	write(t, f, "a")
	h := newHub(t)
	h.Watch(1, f, false)
	ch, cancel := h.Subscribe(1)
	defer cancel()
	write(t, filepath.Join(dir, "other.md"), "x")
	expectNone(t, ch, 200*time.Millisecond)
}

func TestDirRecursiveAndNewSubdir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	h := newHub(t)
	h.Watch(2, dir, true)
	ch, cancel := h.Subscribe(2)
	defer cancel()
	write(t, filepath.Join(dir, "sub", "a.md"), "x")
	expect(t, ch, Change)
	os.Mkdir(filepath.Join(dir, "new"), 0o755)
	expect(t, ch, Change)
	time.Sleep(100 * time.Millisecond)
	write(t, filepath.Join(dir, "new", "b.md"), "x")
	expect(t, ch, Change)
}

func TestHiddenIgnored(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	h := newHub(t)
	h.Watch(2, dir, true)
	ch, cancel := h.Subscribe(2)
	defer cancel()
	write(t, filepath.Join(dir, ".git", "x"), "x")
	write(t, filepath.Join(dir, ".DS_Store"), "x")
	expectNone(t, ch, 200*time.Millisecond)
}

func TestUnwatchSendsGone(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "notes.md")
	write(t, f, "a")
	h := newHub(t)
	h.Watch(1, f, false)
	ch, _ := h.Subscribe(1)
	h.Unwatch(1)
	expect(t, ch, Gone)
	if _, ok := <-ch; ok {
		t.Fatal("channel not closed")
	}
}

func TestCloseSendsGone(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "notes.md")
	write(t, f, "a")
	h, _ := New(deb)
	h.Watch(1, f, false)
	ch, _ := h.Subscribe(1)
	h.Close()
	expect(t, ch, Gone)
}

func TestMissingDirStillSubscribable(t *testing.T) {
	h := newHub(t)
	if err := h.Watch(3, filepath.Join(t.TempDir(), "nope"), true); err != nil {
		t.Fatal(err)
	}
	ch, cancel := h.Subscribe(3)
	defer cancel()
	expectNone(t, ch, 100*time.Millisecond)
}

func TestSubdirRecreated(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	h := newHub(t)
	h.Watch(2, dir, true)
	ch, cancel := h.Subscribe(2)
	defer cancel()
	os.RemoveAll(filepath.Join(dir, "sub"))
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	expect(t, ch, Change)
	time.Sleep(200 * time.Millisecond)
	for len(ch) > 0 {
		<-ch
	}
	write(t, filepath.Join(dir, "sub", "a.md"), "x")
	expect(t, ch, Change)
}

func TestMissingRootAppears(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	h := newHub(t)
	h.Watch(3, root, true)
	ch, cancel := h.Subscribe(3)
	defer cancel()
	os.MkdirAll(root, 0o755)
	write(t, filepath.Join(root, "a.md"), "x")
	expect(t, ch, Change)
	time.Sleep(200 * time.Millisecond)
	for len(ch) > 0 {
		<-ch
	}
	write(t, filepath.Join(root, "a.md"), "y")
	expect(t, ch, Change)
}

func TestRootRecreated(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	os.MkdirAll(root, 0o755)
	h := newHub(t)
	h.Watch(3, root, true)
	ch, cancel := h.Subscribe(3)
	defer cancel()
	os.RemoveAll(root)
	os.MkdirAll(root, 0o755)
	time.Sleep(300 * time.Millisecond)
	for len(ch) > 0 {
		<-ch
	}
	write(t, filepath.Join(root, "a.md"), "x")
	expect(t, ch, Change)
}

func openFDs(t *testing.T) int {
	ents, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Skip("no /dev/fd")
	}
	return len(ents)
}

// Watching a big folder (a repo with node_modules) must not use a file
// descriptor per file.
func TestLargeFolderUsesFewFDs(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		sub := filepath.Join(dir, "d"+strconv.Itoa(i))
		os.Mkdir(sub, 0o755)
		for j := 0; j < 100; j++ {
			os.WriteFile(filepath.Join(sub, strconv.Itoa(j)), nil, 0o644)
		}
	}
	before := openFDs(t)
	h := newHub(t)
	if err := h.Watch(4, dir, true); err != nil {
		t.Fatal(err)
	}
	if after := openFDs(t); after-before > 50 {
		t.Fatalf("watching 5000 files opened %d descriptors", after-before)
	}
}

func TestAlsoPathEmitsChange(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.md")
	side := filepath.Join(dir, "doc.notes.md")
	write(t, f, "a")
	h := newHub(t)
	if err := h.Watch(1, f, false, side); err != nil {
		t.Fatal(err)
	}
	ch, cancel := h.Subscribe(1)
	defer cancel()
	write(t, side, "first note")
	expect(t, ch, Change)
	time.Sleep(200 * time.Millisecond)
	write(t, side, "second note")
	expect(t, ch, Change)
	drain(ch, 600*time.Millisecond) // FSEvents may send a late duplicate of the last write
	write(t, filepath.Join(dir, "other.md"), "x")
	expectNone(t, ch, 300*time.Millisecond)
}

func drain(ch <-chan Event, d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case <-ch:
		case <-deadline:
			return
		}
	}
}
