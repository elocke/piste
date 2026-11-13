// Package config loads piste's own small configuration (flags + env + one
// TOML file via koanf).
//
// The file is $XDG_CONFIG_HOME/piste/config.toml (default
// ~/.config/piste/config.toml) or $PISTE_CONFIG. It holds machine data only,
// such as the remote host table. It is never a source of agent-zone content:
// the agents engine reads explicit zone roots only.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"github.com/elocke/piste/cli/internal/envvar"
)

// Host is one entry of [remote.hosts.<name>].
type Host struct {
	Name        string `koanf:"-"`
	SSH         string `koanf:"ssh"`         // ssh destination (an ~/.ssh/config alias is fine)
	Flake       string `koanf:"flake"`       // flake dir on the remote
	Description string `koanf:"description"` // for `piste remote list`
}

// Config is the decoded file.
type Config struct {
	Remote struct {
		Hosts map[string]Host `koanf:"hosts"`
	} `koanf:"remote"`
}

// Path returns the config file location for the given environment lookup.
func Path(getenv func(string) string, home string) string {
	if p := envvar.Get(getenv, "CONFIG"); p != "" {
		return p
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "piste", "config.toml")
}

// Load reads path; a missing file is an empty config.
func Load(path string) (Config, error) {
	var c Config
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), toml.Parser()); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
			return c, nil
		}
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if err := k.UnmarshalWithConf("", &c, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	for n, h := range c.Remote.Hosts {
		h.Name = n
		c.Remote.Hosts[n] = h
	}
	return c, nil
}

// HostNames returns the remote host names, sorted.
func (c Config) HostNames() []string {
	out := make([]string, 0, len(c.Remote.Hosts))
	for n := range c.Remote.Hosts {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
