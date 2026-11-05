// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/utils/content-equivalence.ts. Copyright (c) 2024 dyoshikawa.
// MIT License; see cli/THIRD_PARTY_NOTICES.md.

// Package edit changes harness files without re-serialising them.
// Every edit is a set or delete of one owned path; bytes
// outside that path are left as they were, comments included.
//
//   - JSON and JSONC (.claude/settings.json, .mcp.json, ~/.codex/hooks.json):
//     tailscale/hujson, which keeps comments and whitespace. It handles
//     standard JSON too, so one code path serves both, with span
//     preservation, comment safety and JSON-pointer paths that match the lock.
//   - TOML (~/.codex/config.toml): a piste managed block spliced by byte
//     range, validated by parsing the whole document.
//   - Markdown (CLAUDE.md, AGENTS.md): a managed block between markers.
package edit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
)

// ErrNotFound: the pointer names nothing.
var ErrNotFound = errors.New("not found")

// JSON is a parsed JSON/JSONC document being edited.
type JSON struct {
	root hujson.Value
	unit string // one indentation step, detected from the document
}

// ParseJSON parses data; empty or whitespace-only input is an empty object.
func ParseJSON(data []byte) (*JSON, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}\n")
	}
	v, err := hujson.Parse(data)
	if err != nil {
		return nil, err
	}
	return &JSON{root: v, unit: detectUnit(data)}, nil
}

func detectUnit(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || len(trimmed) == len(line) {
			continue
		}
		ws := line[:len(line)-len(trimmed)]
		if strings.HasPrefix(ws, "\t") {
			return "\t"
		}
		return ws
	}
	return "  "
}

// Bytes is the edited document.
func (d *JSON) Bytes() []byte { return d.root.Pack() }

// Split turns an RFC 6901 pointer into its unescaped tokens.
func Split(ptr string) ([]string, error) {
	if ptr == "" {
		return nil, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, fmt.Errorf("json pointer %q must start with /", ptr)
	}
	toks := strings.Split(ptr[1:], "/")
	for i, t := range toks {
		toks[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}
	return toks, nil
}

// Pointer builds an RFC 6901 pointer from tokens.
func Pointer(toks ...string) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

// Get returns the standard-JSON bytes at ptr.
func (d *JSON) Get(ptr string) ([]byte, error) {
	v := d.root.Find(ptr)
	if v == nil {
		return nil, fmt.Errorf("%s: %w", ptr, ErrNotFound)
	}
	c := v.Clone()
	c.BeforeExtra, c.AfterExtra = nil, nil
	c.Standardize()
	return bytes.TrimSpace(c.Pack()), nil
}

// Has reports whether ptr names a value.
func (d *JSON) Has(ptr string) bool { return d.root.Find(ptr) != nil }

// Set puts raw (standard JSON) at ptr, creating missing parent objects.
// An existing value is replaced in place; its surrounding whitespace and
// comments stay.
func (d *JSON) Set(ptr string, raw []byte) error {
	toks, err := Split(ptr)
	if err != nil {
		return err
	}
	if len(toks) == 0 {
		return errors.New("refusing to replace the whole document")
	}
	parent, depth, err := d.ensureObject(toks[:len(toks)-1])
	if err != nil {
		return err
	}
	obj, ok := parent.Value.(*hujson.Object)
	if !ok {
		return fmt.Errorf("%s: parent is not an object", ptr)
	}
	name := toks[len(toks)-1]
	indent := d.indentFor(obj, depth)
	val, err := d.render(raw, indent)
	if err != nil {
		return err
	}
	for i := range obj.Members {
		if memberName(obj.Members[i]) == name {
			obj.Members[i].Value.Value = val.Value
			return nil
		}
	}
	appendMember(obj, name, val, indent, d.closeIndent(obj, depth))
	return nil
}

// Delete removes the member or element at ptr.
func (d *JSON) Delete(ptr string) error {
	toks, err := Split(ptr)
	if err != nil {
		return err
	}
	if len(toks) == 0 {
		return errors.New("refusing to delete the whole document")
	}
	parent := d.root.Find(Pointer(toks[:len(toks)-1]...))
	if parent == nil {
		return fmt.Errorf("%s: %w", ptr, ErrNotFound)
	}
	last := toks[len(toks)-1]
	switch c := parent.Value.(type) {
	case *hujson.Object:
		for i := range c.Members {
			if memberName(c.Members[i]) == last {
				removeMember(c, i)
				return nil
			}
		}
	case *hujson.Array:
		i, err := strconv.Atoi(last)
		if err == nil && i >= 0 && i < len(c.Elements) {
			removeElement(c, i)
			return nil
		}
	}
	return fmt.Errorf("%s: %w", ptr, ErrNotFound)
}

// Append adds raw to the array at ptr (created if missing) and returns its
// pointer.
func (d *JSON) Append(ptr string, raw []byte) (string, error) {
	toks, err := Split(ptr)
	if err != nil {
		return "", err
	}
	if !d.Has(ptr) {
		if err := d.Set(ptr, []byte("[]")); err != nil {
			return "", err
		}
	}
	v := d.root.Find(ptr)
	arr, ok := v.Value.(*hujson.Array)
	if !ok {
		return "", fmt.Errorf("%s: not an array", ptr)
	}
	depth := len(toks) + 1
	indent := d.elemIndent(arr, depth)
	val, err := d.render(raw, indent)
	if err != nil {
		return "", err
	}
	closeIndent := "\n" + strings.Repeat(d.unit, depth-1)
	if n := len(arr.Elements); n > 0 {
		val.BeforeExtra = leadingWS(arr.Elements[n-1].BeforeExtra, indent)
		if arr.Elements[n-1].AfterExtra != nil {
			val.AfterExtra = arr.Elements[n-1].AfterExtra
			arr.Elements[n-1].AfterExtra = []byte{}
		}
	} else {
		val.BeforeExtra = []byte(indent)
		if len(bytes.TrimSpace(arr.AfterExtra)) == 0 {
			arr.AfterExtra = []byte(closeIndent)
		}
	}
	arr.Elements = append(arr.Elements, val)
	return Pointer(append(toks, strconv.Itoa(len(arr.Elements)-1))...), nil
}

// FindElement returns the index of the first element of the array at ptr
// whose canonical hash is sum, or -1.
func (d *JSON) FindElement(ptr, sum string) int {
	v := d.root.Find(ptr)
	if v == nil {
		return -1
	}
	arr, ok := v.Value.(*hujson.Array)
	if !ok {
		return -1
	}
	for i := range arr.Elements {
		c := arr.Elements[i].Clone()
		c.BeforeExtra, c.AfterExtra = nil, nil
		c.Standardize()
		if h, err := Hash(c.Pack()); err == nil && h == sum {
			return i
		}
	}
	return -1
}

// Len is the length of the array or object at ptr (0 if missing).
func (d *JSON) Len(ptr string) int {
	v := d.root.Find(ptr)
	if v == nil {
		return 0
	}
	switch c := v.Value.(type) {
	case *hujson.Array:
		return len(c.Elements)
	case *hujson.Object:
		return len(c.Members)
	}
	return 0
}

// Hash is the sha256 of the canonical form of a JSON value (keys sorted,
// no insignificant whitespace), so a reformat is not a change.
func Hash(raw []byte) (string, error) {
	c, err := Canonical(raw)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(c)
	return hex.EncodeToString(s[:]), nil
}

// Canonical re-encodes a JSON (or JSONC) value with sorted keys.
func Canonical(raw []byte) ([]byte, error) {
	std, err := hujson.Standardize(append([]byte(nil), raw...))
	if err != nil {
		return nil, err
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(std))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(b.Bytes()), nil
}

// --- internals ---------------------------------------------------------------

func memberName(m hujson.ObjectMember) string {
	lit, ok := m.Name.Value.(hujson.Literal)
	if !ok {
		return ""
	}
	return lit.String()
}

// ensureObject walks toks from the root, creating empty objects, and
// returns the object value and its depth.
func (d *JSON) ensureObject(toks []string) (*hujson.Value, int, error) {
	cur := &d.root
	for depth, t := range toks {
		obj, ok := cur.Value.(*hujson.Object)
		if !ok {
			if arr, isArr := cur.Value.(*hujson.Array); isArr {
				i, err := strconv.Atoi(t)
				if err != nil || i < 0 || i >= len(arr.Elements) {
					return nil, 0, fmt.Errorf("%s: %w", Pointer(toks[:depth+1]...), ErrNotFound)
				}
				cur = &arr.Elements[i]
				continue
			}
			return nil, 0, fmt.Errorf("%s: not an object", Pointer(toks[:depth]...))
		}
		found := false
		for i := range obj.Members {
			if memberName(obj.Members[i]) == t {
				cur = &obj.Members[i].Value
				found = true
				break
			}
		}
		if !found {
			indent := d.indentFor(obj, depth+1)
			empty := hujson.Value{Value: &hujson.Object{}}
			appendMember(obj, t, empty, indent, d.closeIndent(obj, depth+1))
			cur = &obj.Members[len(obj.Members)-1].Value
		}
	}
	return cur, len(toks) + 1, nil
}

// indentFor: the whitespace before a member of obj (at nesting depth).
func (d *JSON) indentFor(obj *hujson.Object, depth int) string {
	if n := len(obj.Members); n > 0 {
		return string(leadingWS(obj.Members[n-1].Name.BeforeExtra, "\n"+strings.Repeat(d.unit, depth)))
	}
	return "\n" + strings.Repeat(d.unit, depth)
}

func (d *JSON) closeIndent(obj *hujson.Object, depth int) string {
	if len(obj.Members) > 0 || len(bytes.TrimSpace(obj.AfterExtra)) > 0 {
		return string(obj.AfterExtra)
	}
	return "\n" + strings.Repeat(d.unit, depth-1)
}

func (d *JSON) elemIndent(arr *hujson.Array, depth int) string {
	if n := len(arr.Elements); n > 0 {
		return string(leadingWS(arr.Elements[n-1].BeforeExtra, "\n"+strings.Repeat(d.unit, depth)))
	}
	return "\n" + strings.Repeat(d.unit, depth)
}

// leadingWS keeps only the whitespace after the last newline of extra (a
// sibling's indentation, without its comments); single-line siblings get a
// single space.
func leadingWS(extra []byte, fallback string) []byte {
	s := string(extra)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return []byte("\n" + s[i+1:])
	}
	if strings.TrimSpace(s) == "" && s != "" {
		return []byte(s)
	}
	if s == "" {
		return []byte("")
	}
	return []byte(fallback)
}

// render parses raw and lays it out with the document's indentation, so an
// inserted entry looks like its neighbours.
func (d *JSON) render(raw []byte, indent string) (hujson.Value, error) {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return hujson.Value{}, fmt.Errorf("invalid JSON value: %w", err)
	}
	if strings.Contains(indent, "\n") {
		var ind bytes.Buffer
		if err := json.Indent(&ind, b.Bytes(), strings.TrimPrefix(indent, "\n"), d.unit); err != nil {
			return hujson.Value{}, err
		}
		b = ind
	}
	out, err := hujson.Parse(b.Bytes())
	if err != nil {
		return hujson.Value{}, err
	}
	out.BeforeExtra, out.AfterExtra = []byte(" "), nil
	return out, nil
}

func appendMember(obj *hujson.Object, name string, val hujson.Value, indent, closing string) {
	nameLit := hujson.Value{BeforeExtra: []byte(indent), Value: hujson.String(name)}
	val.BeforeExtra = []byte(" ")
	n := len(obj.Members)
	if n > 0 && obj.Members[n-1].Value.AfterExtra != nil {
		// JSONC with a trailing comma: keep the style.
		val.AfterExtra = obj.Members[n-1].Value.AfterExtra
		obj.Members[n-1].Value.AfterExtra = []byte{}
	} else {
		val.AfterExtra = nil
	}
	if n == 0 {
		obj.AfterExtra = []byte(closing)
	}
	obj.Members = append(obj.Members, hujson.ObjectMember{Name: nameLit, Value: val})
}

func removeMember(obj *hujson.Object, i int) {
	last := i == len(obj.Members)-1
	trailing := obj.Members[i].Value.AfterExtra
	obj.Members = append(obj.Members[:i], obj.Members[i+1:]...)
	if last && len(obj.Members) > 0 {
		obj.Members[len(obj.Members)-1].Value.AfterExtra = trailing
	}
	if len(obj.Members) == 0 && len(bytes.TrimSpace(obj.AfterExtra)) == 0 {
		obj.AfterExtra = nil
	}
}

func removeElement(arr *hujson.Array, i int) {
	last := i == len(arr.Elements)-1
	trailing := arr.Elements[i].AfterExtra
	arr.Elements = append(arr.Elements[:i], arr.Elements[i+1:]...)
	if last && len(arr.Elements) > 0 {
		arr.Elements[len(arr.Elements)-1].AfterExtra = trailing
	}
	if len(arr.Elements) == 0 && len(bytes.TrimSpace(arr.AfterExtra)) == 0 {
		arr.AfterExtra = nil
	}
}
