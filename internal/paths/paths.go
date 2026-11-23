// Package paths locates airs' config and state directories.
package paths

import (
	"errors"
	"os"
	"path/filepath"
)

// Dirs holds the config and state directories for airs.
type Dirs struct {
	Config string
	State  string
}

// Resolve returns the directories, honoring XDG_CONFIG_HOME and XDG_STATE_HOME.
func Resolve() (Dirs, error) {
	home := os.Getenv("HOME")
	cfg := os.Getenv("XDG_CONFIG_HOME")
	st := os.Getenv("XDG_STATE_HOME")
	if (cfg == "" || st == "") && home == "" {
		return Dirs{}, errors.New("HOME is not set")
	}
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	if st == "" {
		st = filepath.Join(home, ".local", "state")
	}
	return Dirs{Config: filepath.Join(cfg, "airs"), State: filepath.Join(st, "airs")}, nil
}

func (d Dirs) ConfigFile() string { return filepath.Join(d.Config, "config.toml") }
func (d Dirs) StateFile() string  { return filepath.Join(d.State, "state.json") }
func (d Dirs) Socket() string     { return filepath.Join(d.State, "airs.sock") }
func (d Dirs) Log() string        { return filepath.Join(d.State, "airs.log") }
func (d Dirs) Lock() string       { return filepath.Join(d.State, "airs.lock") }

// EnsureState creates the state directory.
func (d Dirs) EnsureState() error { return os.MkdirAll(d.State, 0o700) }
