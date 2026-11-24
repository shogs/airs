package qr

import (
	"bytes"
	"strings"
	"testing"
)

func TestTerminalNonEmpty(t *testing.T) {
	var b bytes.Buffer
	if err := Terminal(&b, "http://192.168.1.20:7777/notes.md"); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if !strings.ContainsAny(s, "█▀▄") || strings.Count(s, "\n") < 10 {
		t.Fatalf("not a QR:\n%s", s)
	}
}

func TestPNG(t *testing.T) {
	b, err := PNG("http://192.168.1.20:7777/", 256)
	if err != nil || !bytes.HasPrefix(b, []byte("\x89PNG")) {
		t.Fatalf("%v %q", err, b[:8])
	}
}

func TestMedium(t *testing.T) {
	u := "http://192.168.1.20:7777/airreader/2026-11-24-airs-agent-projects-design.md"
	m, err := Medium(u)
	var low bytes.Buffer
	Terminal(&low, u)
	if err != nil || !strings.ContainsAny(m, "█▀▄") || strings.Count(m, "\n") <= strings.Count(low.String(), "\n") {
		t.Fatalf("%v\n%s", err, m)
	}
}
