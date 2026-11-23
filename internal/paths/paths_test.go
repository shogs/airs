package paths

import "testing"

func TestResolveXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x")
	t.Setenv("XDG_STATE_HOME", "/y")
	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if d.Config != "/x/airs" || d.State != "/y/airs" {
		t.Fatalf("got %+v", d)
	}
	if d.Socket() != "/y/airs/airs.sock" {
		t.Fatalf("socket %s", d.Socket())
	}
	if d.StateFile() != "/y/airs/state.json" || d.Log() != "/y/airs/airs.log" || d.Lock() != "/y/airs/airs.lock" || d.ConfigFile() != "/x/airs/config.toml" {
		t.Fatalf("files %+v", d)
	}
}

func TestResolveHomeDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/h")
	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if d.Config != "/h/.config/airs" || d.State != "/h/.local/state/airs" {
		t.Fatalf("got %+v", d)
	}
}
