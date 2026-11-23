package watch

import (
	"os"
	"path/filepath"
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
	expectNone(t, ch, 200*time.Millisecond)
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
