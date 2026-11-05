// Package layer reads a team layer: a flake of shared modules and
// agents that each person's own overlay is built on. A layer is marked by
// piste-layer.toml at its root, which names its zone and the secrets its
// modules export, and it ships a starter overlay in template/.
package layer

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/elocke/piste/cli/internal/agents/zone"
)

// Marker is the file at a layer's root; Template is its starter overlay.
const (
	Marker   = "piste-layer.toml"
	Template = "template"
)

// Meta is what piste-layer.toml says: the layer's zone, and its secrets as
// env var -> sops key path ("terraform/token" is the key token under
// terraform).
type Meta struct {
	Zone    string            `toml:"zone"`
	Secrets map[string]string `toml:"secrets"`
}

// IsLayer: dir holds a layer's marker.
func IsLayer(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, Marker))
	return err == nil
}

// Read loads dir's marker; a missing or invalid zone is an error.
func Read(dir string) (Meta, error) {
	var m Meta
	b, err := os.ReadFile(filepath.Join(dir, Marker))
	if err != nil {
		return m, err
	}
	if err := toml.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s: %w", Marker, err)
	}
	if m.Zone == "" {
		return m, fmt.Errorf("%s: no zone", Marker)
	}
	if err := zone.ValidMachine(m.Zone); err != nil {
		return m, fmt.Errorf("%s: %w", Marker, err)
	}
	return m, nil
}

// Ref is url as a git flake ref. Never github:, which fetches a tarball
// without the person's git credentials, so a private layer would fail.
func Ref(url string) (string, error) {
	switch {
	case strings.HasPrefix(url, "git+"):
		return url, nil
	case strings.HasPrefix(url, "ssh://"), strings.HasPrefix(url, "https://"),
		strings.HasPrefix(url, "http://"), strings.HasPrefix(url, "file://"):
		return "git+" + url, nil
	case filepath.IsAbs(url):
		return "git+file://" + url, nil
	}
	// scp-like: user@host:path
	if at, colon := strings.Index(url, "@"), strings.Index(url, ":"); at > 0 && colon > at && !strings.Contains(url[:colon], "/") {
		return "git+ssh://" + url[:colon] + "/" + strings.TrimPrefix(url[colon+1:], "/"), nil
	}
	return "", fmt.Errorf("%q: want a git URL (git@host:org/repo.git, ssh://, https://, file://) or an absolute path", url)
}

// Skeleton is a sops YAML file holding every secret's key, empty: key paths
// split on "/" into nested maps, sorted, two-space indents.
func Skeleton(secrets map[string]string) []byte {
	type node map[string]node
	root := node{}
	for _, path := range secrets {
		n := root
		for _, k := range strings.Split(path, "/") {
			if n[k] == nil {
				n[k] = node{}
			}
			n = n[k]
		}
	}
	var b strings.Builder
	var walk func(n node, indent string)
	walk = func(n node, indent string) {
		keys := make([]string, 0, len(n))
		for k := range n {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if len(n[k]) == 0 {
				fmt.Fprintf(&b, "%s%s: \"\"\n", indent, k)
				continue
			}
			fmt.Fprintf(&b, "%s%s:\n", indent, k)
			walk(n[k], indent+"  ")
		}
	}
	walk(root, "")
	return []byte(b.String())
}
