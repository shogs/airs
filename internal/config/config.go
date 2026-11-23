// Package config loads the user-edited airs config file.
package config

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/BurntSushi/toml"
)

// DefaultPort is the LAN HTTP port used when none is configured.
const DefaultPort = 7777

// Config is the user configuration.
type Config struct {
	Port       int  `toml:"port"`
	AnyNetwork bool `toml:"any_network"`
}

// Load reads path; a missing file yields defaults.
func Load(path string) (Config, error) {
	c := Config{Port: DefaultPort}
	md, err := toml.DecodeFile(path, &c)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{Port: DefaultPort}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	if md.IsDefined("port") && (c.Port < 1 || c.Port > 65535) {
		return Config{}, fmt.Errorf("config %s: port must be 1-65535", path)
	}
	return c, nil
}
