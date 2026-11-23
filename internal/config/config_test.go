package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || c != (Config{Port: 7777, Notes: true}) {
		t.Fatalf("got %+v %v", c, err)
	}
}

func TestLoadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("port = 9000\nany_network = true\n"), 0o600)
	c, err := Load(p)
	if err != nil || c != (Config{Port: 9000, AnyNetwork: true, Notes: true}) {
		t.Fatalf("got %+v %v", c, err)
	}
	os.WriteFile(p, []byte("port = 0\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for port 0")
	}
}

func TestNotesDefault(t *testing.T) {
	c, _ := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if !c.Notes {
		t.Fatal("missing file: notes should default to true")
	}
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("port = 9000\n"), 0o600)
	if c, _ := Load(p); !c.Notes {
		t.Fatal("key absent: notes should default to true")
	}
	os.WriteFile(p, []byte("notes = false\n"), 0o600)
	if c, _ := Load(p); c.Notes {
		t.Fatal("notes = false ignored")
	}
}
