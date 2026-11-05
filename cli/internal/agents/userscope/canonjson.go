package userscope

// JSON that keeps its key order (Obj), and piste's two encodings of it, both
// encoding/json without HTML escaping:
//
//	encode     key order as read (the Codex hooks file, the app-server,
//	           --json output): a file piste rewrites keeps its layout.
//	canonical  keys sorted, compact: what hashes and comparisons use, so
//	           key order and whitespace are not a change.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// Obj is a JSON object that keeps its key order.
type Obj struct {
	Keys []string
	Vals map[string]any
}

// NewObj returns an empty object.
func NewObj() *Obj { return &Obj{Vals: map[string]any{}} }

// Get returns a value (nil if absent).
func (o *Obj) Get(k string) any {
	if o == nil {
		return nil
	}
	return o.Vals[k]
}

// Has reports whether k is present.
func (o *Obj) Has(k string) bool {
	if o == nil {
		return false
	}
	_, ok := o.Vals[k]
	return ok
}

// Set adds or replaces k (a new key goes last).
func (o *Obj) Set(k string, v any) {
	if _, ok := o.Vals[k]; !ok {
		o.Keys = append(o.Keys, k)
	}
	o.Vals[k] = v
}

// Del removes k.
func (o *Obj) Del(k string) {
	if _, ok := o.Vals[k]; !ok {
		return
	}
	delete(o.Vals, k)
	for i, x := range o.Keys {
		if x == k {
			o.Keys = append(o.Keys[:i], o.Keys[i+1:]...)
			break
		}
	}
}

// MarshalJSON writes the object in its key order.
func (o *Obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.Keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := marshalIndent(k, "")
		if err != nil {
			return nil, err
		}
		vb, err := marshalIndent(o.Vals[k], "")
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func marshalIndent(v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// encode is v as JSON in its key order: compact, or indented by indent.
// Every value here was decoded from JSON or built from strings and ints, so
// it always encodes.
func encode(v any, indent string) string {
	b, err := marshalIndent(v, indent)
	if err != nil {
		panic(fmt.Sprintf("userscope: encoding %T: %v", v, err))
	}
	return string(b)
}

// canonical is v as piste's canonical JSON, the form every hash of a JSON
// value is taken over (groupID, the ledger's codex_groups, JSONSHA256). A
// reimplementation must produce these bytes:
//
//   - compact: no whitespace between tokens;
//   - object keys sorted by code point (byte order of their UTF-8); a
//     duplicate key keeps its last value (ParseOrdered);
//   - numbers exactly as written in the source (1, 1.0 and 1e0 differ;
//     -0 stays -0); numbers piste builds itself are Go ints;
//   - strings as Go's encoding/json writes them with SetEscapeHTML(false):
//     UTF-8 as is, `"` and `\` backslash-escaped, U+0008 U+0009 U+000A
//     U+000C U+000D as \b \t \n \f \r, the other controls below U+0020 as
//     \u00xx (lowercase hex), U+2028 and U+2029 as \u2028 and \u2029, and
//     `<`, `>`, `&`, `/` and U+007F as is (invalid UTF-8 becomes U+FFFD);
//   - true, false, null as such.
//
// Ledger version 1 used another layout (legacyCanonical).
func canonical(v any) string { return encode(sorted(v), "") }

// sorted turns every Obj into a map, which encoding/json writes sorted.
func sorted(v any) any {
	switch x := v.(type) {
	case *Obj:
		if x == nil {
			return nil
		}
		m := make(map[string]any, len(x.Keys))
		for _, k := range x.Keys {
			m[k] = sorted(x.Vals[k])
		}
		return m
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = sorted(e)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = sorted(e)
		}
		return out
	}
	return v
}

// ParseOrdered decodes JSON keeping object key order; numbers stay
// json.Number.
func ParseOrdered(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("extra data after JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			o := NewObj()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k := kt.(string)
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(k, v) // a duplicate key keeps its first position, last value
			}
			_, err := dec.Token()
			return o, err
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token()
			return arr, err
		}
	}
	return t, nil
}

// str is a JSON value as message text: a string as is, anything else as
// its JSON.
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return encode(v, "")
}

// runeLen is the length of s in code points.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// truncate keeps the first n code points of s.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
