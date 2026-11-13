// Package outdated holds the pure parsers behind piste pkgs: flake.lock
// inputs, brew and pnpm outdated output, and the overlay's fresh list.
package outdated

import (
	"encoding/json"
	"sort"
)

type Owner int

const (
	OwnerOverlay Owner = iota
	OwnerEngine
)

type Input struct {
	Name     string
	Owner    Owner
	Modified int64  // lock lastModified, 0 when unknown
	Ref      string // the locked ref as a flake URL (for `nix flake prefetch`), "" for path inputs
	Path     string // a path: input's directory relative to the overlay, else ""
	Flake    bool   // false for flake = false inputs
}

type lockNode struct {
	Flake  *bool                      `json:"flake"`
	Inputs map[string]json.RawMessage `json:"inputs"`
	Locked map[string]any             `json:"locked"`
}

type lockFile struct {
	Nodes map[string]lockNode `json:"nodes"`
}

func parseLock(b []byte) (lockFile, error) {
	var l lockFile
	err := json.Unmarshal(b, &l)
	return l, err
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func toInput(name string, n lockNode) Input {
	in := Input{Name: name, Flake: n.Flake == nil || *n.Flake}
	if ts, ok := n.Locked["lastModified"].(float64); ok {
		in.Modified = int64(ts)
	}
	switch str(n.Locked, "type") {
	case "github":
		in.Ref = "github:" + str(n.Locked, "owner") + "/" + str(n.Locked, "repo") + "/" + str(n.Locked, "rev")
	case "gitlab":
		in.Ref = "gitlab:" + str(n.Locked, "owner") + "/" + str(n.Locked, "repo") + "/" + str(n.Locked, "rev")
	case "sourcehut":
		in.Ref = "sourcehut:" + str(n.Locked, "owner") + "/" + str(n.Locked, "repo") + "/" + str(n.Locked, "rev")
	case "git":
		in.Ref = "git+" + str(n.Locked, "url") + "?rev=" + str(n.Locked, "rev")
	case "path":
		in.Path = str(n.Locked, "path")
	}
	return in
}

// nodeName returns the node an inputs entry points at; ok is false for a
// follows (an array).
func nodeName(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// inputsOf lists the non-follows inputs of node, sorted by input key, each
// named by its key in node's inputs map.
func inputsOf(l lockFile, node string) []Input {
	var keys []string
	for k := range l.Nodes[node].Inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Input
	for _, k := range keys {
		target, ok := nodeName(l.Nodes[node].Inputs[k])
		if !ok {
			continue
		}
		out = append(out, toInput(k, l.Nodes[target]))
	}
	return out
}

// DirectInputs: the root node's direct inputs of the lock at lockJSON,
// with each one's owner, decided by engineOwned(name).
// A follows (an array in root.inputs) is skipped.
func DirectInputs(lockJSON []byte, engineOwned func(string) bool) ([]Input, error) {
	l, err := parseLock(lockJSON)
	if err != nil {
		return nil, err
	}
	out := inputsOf(l, "root")
	for i := range out {
		if engineOwned != nil && engineOwned(out[i].Name) {
			out[i].Owner = OwnerEngine
		}
	}
	return out, nil
}

// Unfollowed: the inputs of the layer at root input `name` (an Input.Name
// from DirectInputs), minus those that are follows. The root input is
// resolved through root.inputs to its lock node (nil when it is a follows or
// absent). Each result is named by the layer's own input key; Modified, Ref
// and Flake come from the node it points to.
func Unfollowed(lockJSON []byte, name string) ([]Input, error) {
	l, err := parseLock(lockJSON)
	if err != nil {
		return nil, err
	}
	raw, ok := l.Nodes["root"].Inputs[name]
	if !ok {
		return nil, nil
	}
	node, ok := nodeName(raw)
	if !ok {
		return nil, nil
	}
	return inputsOf(l, node), nil
}
