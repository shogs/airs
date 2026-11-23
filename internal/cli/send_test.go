package cli

import (
	"strings"
	"testing"

	"airs/internal/airdrop"
)

func fakeSend(t *testing.T, err error) (*[]string, *[]string) {
	t.Helper()
	var sent, copied []string
	oldS, oldC := sendURL, copyURL
	sendURL = func(u string) error { sent = append(sent, u); return err }
	copyURL = func(u string) error { copied = append(copied, u); return nil }
	t.Cleanup(func() { sendURL, copyURL = oldS, oldC })
	return &sent, &copied
}

func TestSendUsesAirDrop(t *testing.T) {
	e := setup(t, true)
	e.run(t, e.file(t, "notes.md"))
	sent, copied := fakeSend(t, nil)
	if _, errs, code := e.run(t, "send", "notes.md"); code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	if len(*sent) != 1 || !strings.HasSuffix((*sent)[0], "/notes.md") || !strings.HasPrefix((*sent)[0], "http://") || len(*copied) != 0 {
		t.Fatalf("sent %v copied %v", *sent, *copied)
	}
}

func TestSendFallsBackToClipboard(t *testing.T) {
	e := setup(t, true)
	e.run(t, e.file(t, "notes.md"))
	_, copied := fakeSend(t, airdrop.ErrUnavailable)
	out, _, code := e.run(t, "send", "1")
	if code != 0 || !strings.Contains(out, "copied to clipboard") || len(*copied) != 1 {
		t.Fatalf("%d %s %v", code, out, *copied)
	}
}

func TestSendUnknownItem(t *testing.T) {
	e := setup(t, true)
	e.run(t, e.file(t, "notes.md"))
	sent, _ := fakeSend(t, nil)
	if _, _, code := e.run(t, "send", "nope.md"); code != 1 || len(*sent) != 0 {
		t.Fatalf("%d %v", code, *sent)
	}
}

func TestSendWarnsWhenBlocked(t *testing.T) {
	e := setup(t, false)
	e.run(t, e.file(t, "notes.md"))
	sent, _ := fakeSend(t, nil)
	out, _, code := e.run(t, "send", "notes.md")
	if code != 0 || len(*sent) != 1 || !strings.Contains(out, "isn't trusted") {
		t.Fatalf("%d %s", code, out)
	}
}
