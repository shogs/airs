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
