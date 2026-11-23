// Package notes stores reader notes in a markdown file beside a document:
// README.md → README.notes.md. That file is the only record. Each note starts
// with a hidden marker line carrying its placement as JSON, followed by a
// human-readable heading, the quoted text and the note itself, so the file
// reads well in any editor and survives hand edits.
package notes

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Limits on what a note may hold, in bytes.
const (
	MaxText  = 10000
	MaxQuote = 2000
	maxMeta  = 1000 // block fingerprint, section id, heading
)

const (
	markerPrefix = "<!-- airs:note "
	markerSuffix = " -->"
	headingTime  = "2 Jan 2006, 15:04"
)

// Note is one saved note.
type Note struct {
	ID      string    `json:"id"`
	Block   string    `json:"block"`
	Index   int       `json:"index"`
	Section string    `json:"section"`
	Quote   string    `json:"quote"`
	Text    string    `json:"text"`
	Time    time.Time `json:"time"`
	Edited  time.Time `json:"edited,omitzero"`
}

// Input is a new note as sent by a page.
type Input struct {
	Block   string `json:"block"`
	Index   int    `json:"index"`
	Section string `json:"section"`
	Heading string `json:"heading"`
	Quote   string `json:"quote"`
	Text    string `json:"text"`
}

// marker is the JSON inside a note's marker line.
type marker struct {
	ID      string    `json:"id"`
	Block   string    `json:"block"`
	Index   int       `json:"index"`
	Section string    `json:"section"`
	Time    time.Time `json:"time"`
	Edited  time.Time `json:"edited,omitzero"`
}

// ErrNotFound is returned by Edit when no note has the given id.
var ErrNotFound = errors.New("note not found")

// ConflictError is returned by Edit when the note changed since the page loaded it.
type ConflictError struct{ Current Note }

func (e *ConflictError) Error() string { return "note changed on the Mac" }

// InputError reports a note that breaks a limit or rule.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// Path is the notes file for document doc.
func Path(doc string) string {
	base := filepath.Base(doc)
	switch strings.ToLower(filepath.Ext(base)) {
	case ".md", ".markdown":
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return filepath.Join(filepath.Dir(doc), base+".notes.md")
}

// IsNotesFile reports whether doc is itself a notes file.
func IsNotesFile(doc string) bool {
	return strings.HasSuffix(strings.ToLower(filepath.Base(doc)), ".notes.md")
}

// Read returns the notes for doc. A missing notes file means no notes.
func Read(doc string) ([]Note, error) {
	b, err := os.ReadFile(Path(doc))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(b), nil
}

// Parse reads notes from a notes file. Anything it does not understand is skipped.
func Parse(src []byte) []Note {
	lines := strings.Split(normalize(string(src)), "\n")
	var out []Note
	for i := 0; i < len(lines); i++ {
		m, ok := parseMarker(lines[i])
		if !ok {
			continue
		}
		j := i + 1
		for j < len(lines) && !isMarker(lines[j]) {
			j++
		}
		quote, text, _ := parseBody(lines[i+1 : j])
		out = append(out, Note{ID: m.ID, Block: m.Block, Index: m.Index, Section: m.Section,
			Quote: quote, Text: text, Time: m.Time, Edited: m.Edited})
		i = j - 1
	}
	return out
}

func isMarker(line string) bool {
	return strings.HasPrefix(strings.TrimRight(line, "\r"), markerPrefix)
}

func parseMarker(line string) (marker, bool) {
	line = strings.TrimRight(line, "\r")
	if !strings.HasPrefix(line, markerPrefix) || !strings.HasSuffix(line, markerSuffix) {
		return marker{}, false
	}
	var m marker
	js := strings.TrimSuffix(strings.TrimPrefix(line, markerPrefix), markerSuffix)
	if err := json.Unmarshal([]byte(js), &m); err != nil || m.ID == "" {
		return marker{}, false
	}
	return m, true
}

// parseBody splits the lines after a marker into quote and text. header is the
// number of lines (blank lines, the ### line, quote lines) before the text.
func parseBody(lines []string) (quote, text string, header int) {
	i := 0
	skipBlank := func() {
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
	}
	skipBlank()
	if i < len(lines) && strings.HasPrefix(lines[i], "### ") {
		i++
		header = i
	}
	save := i
	skipBlank()
	var q []string
	for i < len(lines) && strings.HasPrefix(strings.TrimRight(lines[i], "\r"), ">") {
		l := strings.TrimPrefix(strings.TrimRight(lines[i], "\r"), ">")
		q = append(q, strings.TrimPrefix(l, " "))
		i++
		header = i
	}
	if q == nil {
		i = save
	}
	return strings.TrimSpace(strings.Join(q, "\n")), strings.TrimSpace(strings.Join(lines[i:], "\n")), header
}

func normalize(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// clean validates and normalises one field.
func clean(s string, max int, field string) (string, error) {
	if !utf8.ValidString(s) {
		return "", &InputError{field + " is not valid UTF-8"}
	}
	s = strings.TrimSpace(normalize(s))
	if len(s) > max {
		return "", &InputError{fmt.Sprintf("%s is too long (max %d bytes)", field, max)}
	}
	// A note must never start a marker of its own.
	return strings.ReplaceAll(s, "<!--", "<!‐‐"), nil
}

func cleanInput(in Input) (Input, error) {
	var err error
	fields := []struct {
		p    *string
		max  int
		name string
	}{{&in.Text, MaxText, "note"}, {&in.Quote, MaxQuote, "quote"}, {&in.Block, maxMeta, "block"},
		{&in.Section, maxMeta, "section"}, {&in.Heading, maxMeta, "heading"}}
	for _, f := range fields {
		if *f.p, err = clean(*f.p, f.max, f.name); err != nil {
			return in, err
		}
	}
	if in.Text == "" && in.Quote == "" {
		return in, &InputError{"note is empty"}
	}
	if in.Index < 0 {
		in.Index = 0
	}
	in.Block = strings.Join(strings.Fields(in.Block), " ")
	in.Heading = strings.Join(strings.Fields(in.Heading), " ")
	if in.Heading == "" {
		in.Heading = "Note"
	}
	return in, nil
}

func newID(now time.Time) string {
	b := make([]byte, 5)
	rand.Read(b)
	return now.Format("20060102-150405") + "-" + strings.ToLower(base32.StdEncoding.EncodeToString(b))[:4]
}

func markerLine(m marker) string {
	js, _ := json.Marshal(m) // escapes <, > and &, so "-->" cannot appear
	return markerPrefix + string(js) + markerSuffix
}

// Writer serialises writes to notes files.
type Writer struct{ mu sync.Mutex }

// Append adds a note to doc's notes file, creating it if needed.
func (w *Writer) Append(doc string, in Input, now time.Time) (Note, error) {
	in, err := cleanInput(in)
	if err != nil {
		return Note{}, err
	}
	now = now.Truncate(time.Second)
	m := marker{ID: newID(now), Block: in.Block, Index: in.Index, Section: in.Section, Time: now}
	var b strings.Builder
	b.WriteString(markerLine(m) + "\n")
	b.WriteString("### " + in.Heading + " · " + now.Format(headingTime) + "\n\n")
	if in.Quote != "" {
		for _, l := range strings.Split(in.Quote, "\n") {
			b.WriteString(strings.TrimRight("> "+l, " ") + "\n")
		}
		if in.Text != "" {
			b.WriteString("\n")
		}
	}
	if in.Text != "" {
		b.WriteString(in.Text + "\n")
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	f, err := os.OpenFile(Path(doc), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return Note{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Note{}, err
	}
	prefix := ""
	if fi.Size() == 0 {
		prefix = "# Notes on " + filepath.Base(doc) + "\n\n"
	} else {
		tail := make([]byte, 2)
		off := max(fi.Size()-2, 0)
		n, _ := f.ReadAt(tail, off)
		tail = tail[:n]
		switch {
		case bytes.HasSuffix(tail, []byte("\n\n")):
		case bytes.HasSuffix(tail, []byte("\n")):
			prefix = "\n"
		default:
			prefix = "\n\n"
		}
	}
	if _, err := f.WriteString(prefix + b.String()); err != nil {
		return Note{}, err
	}
	return Note{ID: m.ID, Block: m.Block, Index: m.Index, Section: m.Section, Quote: in.Quote, Text: in.Text, Time: now}, nil
}

// Edit replaces the text of note id. was is the text the editor started from;
// if the file now holds something else, Edit returns *ConflictError. Only that
// note's marker and text change; every other byte of the file is kept.
func (w *Writer) Edit(doc, id, was, text string, now time.Time) (Note, error) {
	text, err := clean(text, MaxText, "note")
	if err != nil {
		return Note{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	p := Path(doc)
	src, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, err
	}

	// Find the marker line and the end of the note's region, in raw bytes.
	var mStart, mEnd, end = -1, -1, len(src)
	var m marker
	for off := 0; off < len(src); {
		nl := bytes.IndexByte(src[off:], '\n')
		lineEnd := len(src)
		if nl >= 0 {
			lineEnd = off + nl + 1
		}
		line := strings.TrimRight(string(src[off:lineEnd]), "\n")
		if mStart >= 0 && isMarker(line) {
			end = off
			break
		}
		if mk, ok := parseMarker(line); ok && mk.ID == id && mStart < 0 {
			mStart, mEnd, m = off, lineEnd, mk
		}
		off = lineEnd
	}
	if mStart < 0 {
		return Note{}, ErrNotFound
	}
	region := string(src[mEnd:end])
	lines := strings.SplitAfter(region, "\n")
	trimmed := make([]string, len(lines))
	for i, l := range lines {
		trimmed[i] = strings.TrimRight(l, "\n")
	}
	quote, cur, header := parseBody(trimmed)
	quote, cur = normalize(quote), normalize(cur)
	current := Note{ID: m.ID, Block: m.Block, Index: m.Index, Section: m.Section, Quote: quote, Text: cur, Time: m.Time, Edited: m.Edited}
	if cur != strings.TrimSpace(normalize(was)) {
		return Note{}, &ConflictError{Current: current}
	}

	eol := string(src[mStart:mEnd])[len(strings.TrimRight(string(src[mStart:mEnd]), "\r\n")):]
	if eol == "" {
		eol = "\n"
	}
	m.Edited = now.Truncate(time.Second)
	var b strings.Builder
	b.WriteString(markerLine(m) + eol)
	hdr := strings.Join(lines[:header], "")
	if hdr != "" && !strings.HasSuffix(hdr, "\n") {
		hdr += "\n"
	}
	b.WriteString(hdr)
	if text != "" {
		if hdr != "" {
			b.WriteString("\n")
		}
		b.WriteString(text + "\n")
	}
	if end < len(src) {
		b.WriteString("\n")
	}

	out := append(append(append([]byte{}, src[:mStart]...), b.String()...), src[end:]...)
	if err := writeAtomic(p, out); err != nil {
		return Note{}, err
	}
	current.Text, current.Edited = text, m.Edited
	return current, nil
}

func writeAtomic(p string, data []byte) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(fi.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
