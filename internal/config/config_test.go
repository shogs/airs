package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || c != (Config{Port: 7777}) {
		t.Fatalf("got %+v %v", c, err)
	}
}

func TestLoadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("port = 9000\nany_network = true\n"), 0o600)
	c, err := Load(p)
	if err != nil || c != (Config{Port: 9000, AnyNetwork: true}) {
		t.Fatalf("got %+v %v", c, err)
	}
	os.WriteFile(p, []byte("port = 0\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for port 0")
	}
}
