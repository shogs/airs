package notes

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 23, 14, 2, 0, 0, time.Local)

func doc(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "README.md")
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPath(t *testing.T) {
	for in, want := range map[string]string{
		"/a/README.md":      "/a/README.notes.md",
		"/a/guide.markdown": "/a/guide.notes.md",
		"/x/a.b.md":         "/x/a.b.notes.md",
		"/x/Shout.MD":       "/x/Shout.notes.md",
	} {
		if got := Path(in); got != want {
			t.Errorf("Path(%q) = %q, want %q", in, got, want)
		}
	}
	if !IsNotesFile("/x/README.notes.md") || IsNotesFile("/x/README.md") {
		t.Fatal("IsNotesFile")
	}
}

var sample = Input{Block: "Needs Go 1.27+ and macOS.", Index: 4, Section: "install", Heading: "Install", Quote: "Needs Go 1.27+", Text: "Check 1.26"}

func TestAppendCreatesFile(t *testing.T) {
	d := doc(t)
	var w Writer
	n, err := w.Append(d, sample, t0)
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, Path(d))
	if !strings.HasPrefix(got, "# Notes on README.md\n\n<!-- airs:note {") {
		t.Fatalf("start:\n%s", got)
	}
	if !strings.Contains(got, "### Install · 23 Nov 2026, 14:02\n\n> Needs Go 1.27+\n\nCheck 1.26\n") {
		t.Fatalf("body:\n%s", got)
	}
	if n.ID == "" || !n.Time.Equal(t0) || n.Text != "Check 1.26" {
		t.Fatalf("%+v", n)
	}
}

func TestRoundTrip(t *testing.T) {
	d := doc(t)
	var w Writer
	in2 := Input{Block: "見て 🎉 block", Index: 0, Section: "", Heading: "Doc", Quote: "a\nb", Text: "見て 🎉\n\nsecond para"}
	if _, err := w.Append(d, sample, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(d, in2, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	ns, err := Read(d)
	if err != nil || len(ns) != 2 {
		t.Fatalf("%v %+v", err, ns)
	}
	for i, in := range []Input{sample, in2} {
		n := ns[i]
		if n.Block != in.Block || n.Index != in.Index || n.Section != in.Section || n.Quote != in.Quote || n.Text != in.Text {
			t.Fatalf("note %d: %+v vs %+v", i, n, in)
		}
	}
	if ns[0].ID == ns[1].ID {
		t.Fatal("ids not unique")
	}
}

func TestReadMissing(t *testing.T) {
	ns, err := Read(doc(t))
	if err != nil || ns != nil {
		t.Fatal(ns, err)
	}
}

func TestEscapesMarkers(t *testing.T) {
	d := doc(t)
	var w Writer
	in := sample
	in.Text = "x <!-- airs:note {} --> y"
	in.Quote = "<!-- q"
	if _, err := w.Append(d, in, t0); err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(read(t, Path(d)), "<!-- airs:note"); c != 1 {
		t.Fatalf("%d markers", c)
	}
	ns, _ := Read(d)
	if len(ns) != 1 || !strings.Contains(ns[0].Text, "<!‐‐") || !strings.HasPrefix(ns[0].Quote, "<!‐‐") {
		t.Fatalf("%+v", ns)
	}
}

func TestParseHandEdited(t *testing.T) {
	src := "junk before\r\n\r\n" +
		"<!-- airs:note {\"id\":\"a\",\"block\":\"b\",\"index\":1,\"section\":\"s\",\"time\":\"2026-11-23T14:02:00Z\"} -->\r\n" +
		"### S · 23 Nov 2026, 14:02\r\n\r\n> quoted\r\n\r\nhand edited text\r\n\r\n" +
		"<!-- airs:note {not json} -->\r\n### X\r\n\r\nlost\r\n\r\n" +
		"<!-- airs:note {\"id\":\"c\",\"block\":\"\",\"index\":0,\"section\":\"\",\"time\":\"2026-11-23T14:03:00Z\"} -->\r\n" +
		"### Y\r\n\r\nlast one\r\n\r\norphan whose marker was deleted"
	ns := Parse([]byte(src))
	if len(ns) != 2 {
		t.Fatalf("%+v", ns)
	}
	if ns[0].ID != "a" || ns[0].Quote != "quoted" || ns[0].Text != "hand edited text" {
		t.Fatalf("%+v", ns[0])
	}
	if ns[1].ID != "c" || ns[1].Text != "last one\n\norphan whose marker was deleted" {
		t.Fatalf("%+v", ns[1])
	}
}

func TestLimits(t *testing.T) {
	d := doc(t)
	var w Writer
	bad := []Input{
		{Text: strings.Repeat("x", 10001)},
		{Text: "ok", Quote: strings.Repeat("q", 2001)},
		{Text: "bad \xff utf8"},
		{Text: "  ", Quote: " \n"},
	}
	for i, in := range bad {
		var ie *InputError
		if _, err := w.Append(d, in, t0); !errors.As(err, &ie) {
			t.Errorf("%d: want InputError, got %v", i, err)
		}
	}
	if _, err := w.Append(d, Input{Text: strings.Repeat("x", 10000)}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(d, Input{Quote: "only a quote"}, t0); err != nil {
		t.Fatal(err)
	}
}

// threeNotes writes a hand-made header plus three notes and returns their ids.
func threeNotes(t *testing.T) (string, []Note) {
	t.Helper()
	d := doc(t)
	os.WriteFile(Path(d), []byte("my own header\nkeep me\n"), 0o644)
	var w Writer
	for i, txt := range []string{"one", "two", "three"} {
		in := sample
		in.Text = txt
		if _, err := w.Append(d, in, t0.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	ns, _ := Read(d)
	if len(ns) != 3 {
		t.Fatalf("%+v", ns)
	}
	return d, ns
}

func TestEditKeepsRestByteIdentical(t *testing.T) {
	d, ns := threeNotes(t)
	before := []byte(read(t, Path(d)))
	start := bytes.Index(before, []byte(`"id":"`+ns[1].ID))
	start = bytes.LastIndex(before[:start], []byte("<!-- airs:note"))
	next := bytes.Index(before, []byte(`"id":"`+ns[2].ID))
	next = bytes.LastIndex(before[:next], []byte("<!-- airs:note"))
	var w Writer
	n, err := w.Edit(d, ns[1].ID, "two", "TWO edited", t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	after := []byte(read(t, Path(d)))
	if !bytes.Equal(after[:start], before[:start]) {
		t.Fatal("prefix changed")
	}
	if !bytes.HasSuffix(after, before[next:]) {
		t.Fatalf("suffix changed:\n%s", after)
	}
	got, _ := Read(d)
	if got[1].Text != "TWO edited" || got[1].Edited.IsZero() || got[1].Quote != sample.Quote || n.Text != "TWO edited" {
		t.Fatalf("%+v", got[1])
	}
	if got[0].Text != "one" || got[2].Text != "three" {
		t.Fatalf("%+v", got)
	}
}

func TestEditLastNote(t *testing.T) {
	d, ns := threeNotes(t)
	var w Writer
	if _, err := w.Edit(d, ns[2].ID, "three", "3", t0); err != nil {
		t.Fatal(err)
	}
	got, _ := Read(d)
	if got[2].Text != "3" || !strings.HasSuffix(read(t, Path(d)), "\n3\n") {
		t.Fatalf("%+v\n%s", got[2], read(t, Path(d)))
	}
}

func TestEditEmptyText(t *testing.T) {
	d, ns := threeNotes(t)
	var w Writer
	if _, err := w.Edit(d, ns[0].ID, "one", "", t0); err != nil {
		t.Fatal(err)
	}
	got, _ := Read(d)
	if len(got) != 3 || got[0].Text != "" || got[0].Quote != sample.Quote || got[1].Text != "two" {
		t.Fatalf("%+v", got)
	}
}

func TestEditErrors(t *testing.T) {
	d, ns := threeNotes(t)
	before := read(t, Path(d))
	var w Writer
	if _, err := w.Edit(d, "nope", "", "x", t0); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var ce *ConflictError
	if _, err := w.Edit(d, ns[0].ID, "stale", "x", t0); !errors.As(err, &ce) || ce.Current.Text != "one" {
		t.Fatal(err)
	}
	if _, err := w.Edit(d, ns[0].ID, "one", strings.Repeat("x", 10001), t0); err == nil {
		t.Fatal("limit not enforced on edit")
	}
	if read(t, Path(d)) != before {
		t.Fatal("file changed")
	}
	if _, err := w.Edit(filepath.Join(t.TempDir(), "none.md"), "x", "", "y", t0); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestEditKeepsMode(t *testing.T) {
	d, ns := threeNotes(t)
	os.Chmod(Path(d), 0o640)
	var w Writer
	if _, err := w.Edit(d, ns[0].ID, "one", "x", t0); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(Path(d))
	if fi.Mode().Perm() != 0o640 {
		t.Fatal(fi.Mode())
	}
}

func TestConcurrentWrites(t *testing.T) {
	d := doc(t)
	var w Writer
	first, err := w.Append(d, sample, t0)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	was := first.Text
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := w.Append(d, sample, t0); err != nil {
				t.Error(err)
			}
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			txt := strings.Repeat("e", i+1)
			if _, err := w.Edit(d, first.ID, was, txt, t0); err != nil {
				t.Error(err)
			}
			was = txt
		}(i)
	}
	wg.Wait()
	ns, _ := Read(d)
	if len(ns) != 21 {
		t.Fatalf("%d notes", len(ns))
	}
	for _, n := range ns[1:] {
		if n.Text != sample.Text {
			t.Fatalf("%+v", n)
		}
	}
}

func TestAppendToForeignFile(t *testing.T) {
	d := doc(t)
	os.WriteFile(Path(d), []byte("hello"), 0o644)
	var w Writer
	if _, err := w.Append(d, sample, t0); err != nil {
		t.Fatal(err)
	}
	got := read(t, Path(d))
	if !strings.HasPrefix(got, "hello\n\n<!-- airs:note") || strings.Contains(got, "# Notes on") {
		t.Fatalf("%q", got)
	}
}

func TestEditCRLFFile(t *testing.T) {
	d := doc(t)
	src := "<!-- airs:note {\"id\":\"a\",\"block\":\"b\",\"index\":1,\"section\":\"s\",\"time\":\"2026-11-23T14:02:00Z\"} -->\r\n" +
		"### S\r\n\r\n> q\r\n\r\nline one\r\nline two\r\n\r\n" +
		"<!-- airs:note {\"id\":\"c\",\"block\":\"\",\"index\":0,\"section\":\"\",\"time\":\"2026-11-23T14:03:00Z\"} -->\r\nlast\r\n"
	os.WriteFile(Path(d), []byte(src), 0o644)
	var w Writer
	n, err := w.Edit(d, "a", "line one\nline two", "new", t0)
	if err != nil {
		t.Fatal(err)
	}
	if n.Quote != "q" {
		t.Fatalf("%+v", n)
	}
	got := read(t, Path(d))
	if !strings.HasSuffix(got, "<!-- airs:note {\"id\":\"c\",\"block\":\"\",\"index\":0,\"section\":\"\",\"time\":\"2026-11-23T14:03:00Z\"} -->\r\nlast\r\n") {
		t.Fatalf("%q", got)
	}
	ns := Parse([]byte(got))
	if len(ns) != 2 || ns[0].Text != "new" || ns[1].Text != "last" {
		t.Fatalf("%+v", ns)
	}
}

func TestRefusesSymlinkedNotesFile(t *testing.T) {
	d := doc(t)
	target := filepath.Join(t.TempDir(), "authorized_keys")
	os.WriteFile(target, []byte("keep\n"), 0o600)
	if err := os.Symlink(target, Path(d)); err != nil {
		t.Fatal(err)
	}
	var w Writer
	if _, err := w.Append(d, sample, t0); err == nil {
		t.Fatal("append through a symlink succeeded")
	}
	if _, err := w.Edit(d, "x", "", "y", t0); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("edit through a symlink: %v", err)
	}
	if got := read(t, target); got != "keep\n" {
		t.Fatalf("target changed: %q", got)
	}
}

func TestTextStartingWithQuoteMark(t *testing.T) {
	d := doc(t)
	var w Writer
	in := Input{Block: "b", Text: "> I disagree\nsecond line"}
	n, err := w.Append(d, in, t0)
	if err != nil {
		t.Fatal(err)
	}
	ns, _ := Read(d)
	if len(ns) != 1 || ns[0].Quote != "" || ns[0].Text != in.Text {
		t.Fatalf("%+v", ns)
	}
	if _, err := w.Edit(d, n.ID, in.Text, "> still mine", t0); err != nil {
		t.Fatal(err)
	}
	ns, _ = Read(d)
	if ns[0].Quote != "" || ns[0].Text != "> still mine" {
		t.Fatalf("after edit: %+v", ns[0])
	}
}

func TestDeleteKeepsRestByteIdentical(t *testing.T) {
	for i, txt := range []string{"one", "two", "three"} {
		t.Run(txt, func(t *testing.T) {
			d, ns := threeNotes(t)
			before := []byte(read(t, Path(d)))
			start := bytes.LastIndex(before[:bytes.Index(before, []byte(`"id":"`+ns[i].ID))], []byte("<!-- airs:note"))
			end := len(before)
			if i < 2 {
				end = bytes.LastIndex(before[:bytes.Index(before, []byte(`"id":"`+ns[i+1].ID))], []byte("<!-- airs:note"))
			}
			var w Writer
			if err := w.Delete(d, ns[i].ID, txt); err != nil {
				t.Fatal(err)
			}
			after := []byte(read(t, Path(d)))
			want := append(append([]byte{}, before[:start]...), before[end:]...)
			if !bytes.Equal(after, want) {
				t.Fatalf("got:\n%s\nwant:\n%s", after, want)
			}
			got, _ := Read(d)
			if len(got) != 2 {
				t.Fatalf("%+v", got)
			}
			for _, n := range got {
				if n.ID == ns[i].ID {
					t.Fatal("still there")
				}
			}
		})
	}
}

func TestDeleteErrors(t *testing.T) {
	d, ns := threeNotes(t)
	before := read(t, Path(d))
	var w Writer
	if err := w.Delete(d, "nope", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var ce *ConflictError
	if err := w.Delete(d, ns[0].ID, "stale"); !errors.As(err, &ce) || ce.Current.Text != "one" {
		t.Fatal(err)
	}
	if read(t, Path(d)) != before {
		t.Fatal("file changed")
	}
}

func TestDeleteCRLFAndSymlink(t *testing.T) {
	d := doc(t)
	src := "<!-- airs:note {\"id\":\"a\",\"block\":\"\",\"index\":0,\"section\":\"\",\"time\":\"2026-11-23T14:02:00Z\"} -->\r\n### S\r\n\r\nfirst\r\n\r\n" +
		"<!-- airs:note {\"id\":\"c\",\"block\":\"\",\"index\":0,\"section\":\"\",\"time\":\"2026-11-23T14:03:00Z\"} -->\r\n### T\r\n\r\nlast\r\n"
	os.WriteFile(Path(d), []byte(src), 0o644)
	var w Writer
	if err := w.Delete(d, "a", "first"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, Path(d)); !strings.HasPrefix(got, "<!-- airs:note {\"id\":\"c\"") {
		t.Fatalf("%q", got)
	}
	d2 := doc(t)
	target := filepath.Join(t.TempDir(), "x")
	os.WriteFile(target, []byte(src), 0o644)
	os.Symlink(target, Path(d2))
	if err := w.Delete(d2, "a", "first"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("delete through symlink: %v", err)
	}
}
