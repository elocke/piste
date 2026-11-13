package cli

// .sops.yaml editing for piste host add: a new machine's age key goes
// into keys: and into every creation rule's key_groups age list. goccy/go-yaml
// only finds where each list ends; the new lines are inserted into the
// original bytes, so comments and every other rule stay byte for byte.

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// recipientShapeError: a .sops.yaml addRecipient doesn't edit (the person
// adds the lines by hand, recipientLines).
type recipientShapeError struct{ why string }

func (e *recipientShapeError) Error() string { return e.why }

// listEnd is where a block list ends: its last item's last line (1-based),
// and the column of its items' dash (1-based).
type listEnd struct{ line, col int }

// sopsShape is what addRecipient reads from a .sops.yaml.
type sopsShape struct {
	keys    listEnd
	ages    []listEnd
	pubs    []string        // the keys: list's values
	anchors map[string]bool // every anchor in the file
}

// mappingOf is n's key/value pairs when it is a block mapping.
func mappingOf(n ast.Node) ([]*ast.MappingValueNode, bool) {
	switch m := n.(type) {
	case *ast.MappingNode:
		return m.Values, !m.IsFlowStyle
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{m}, !m.IsFlowStyle
	}
	return nil, false
}

func fieldOf(vs []*ast.MappingValueNode, key string) ast.Node {
	for _, v := range vs {
		if v.Key != nil && v.Key.GetToken() != nil && v.Key.GetToken().Value == key {
			return v.Value
		}
	}
	return nil
}

// blockList is n when it is a block list with items.
func blockList(n ast.Node) (*ast.SequenceNode, bool) {
	s, ok := n.(*ast.SequenceNode)
	if !ok || s.IsFlowStyle || len(s.Entries) == 0 {
		return nil, false
	}
	return s, true
}

// lastLine is the last line any node under n starts on.
type lastLine int

func (l *lastLine) Visit(n ast.Node) ast.Visitor {
	if n != nil && n.GetToken() != nil && n.GetToken().Position.Line > int(*l) {
		*l = lastLine(n.GetToken().Position.Line)
	}
	return l
}

func endOf(s *ast.SequenceNode) listEnd {
	e := s.Entries[len(s.Entries)-1]
	end := lastLine(e.Start.Position.Line)
	if e.Value != nil {
		ast.Walk(&end, e.Value)
	}
	return listEnd{line: int(end), col: e.Start.Position.Column}
}

type anchorNames map[string]bool

func (a anchorNames) Visit(n ast.Node) ast.Visitor {
	if an, ok := n.(*ast.AnchorNode); ok && an.Name != nil {
		a[an.Name.GetToken().Value] = true
	}
	return a
}

// readSopsShape finds keys: and every creation rule's key_groups age lists,
// or says why the file has a shape addRecipient doesn't edit.
func readSopsShape(src []byte) (sopsShape, error) {
	shape := func(why string) (sopsShape, error) { return sopsShape{}, &recipientShapeError{why} }
	f, err := parser.ParseBytes(src, 0)
	if err != nil {
		return shape("piste can't read it as YAML: " + strings.TrimSpace(err.Error()))
	}
	if len(f.Docs) != 1 || f.Docs[0].Body == nil {
		return shape("it isn't one YAML document")
	}
	root, ok := mappingOf(f.Docs[0].Body)
	if !ok {
		return shape("its top level isn't a mapping")
	}
	var sh sopsShape
	keys, ok := blockList(fieldOf(root, "keys"))
	if !ok {
		return shape("it has no keys: list in block style (- one per line)")
	}
	sh.keys = endOf(keys)
	for _, e := range keys.Entries {
		v := e.Value
		if an, ok := v.(*ast.AnchorNode); ok {
			v = an.Value
		}
		if s, ok := v.(*ast.StringNode); ok {
			sh.pubs = append(sh.pubs, s.Value)
		}
	}
	rules, ok := blockList(fieldOf(root, "creation_rules"))
	if !ok {
		return shape("it has no creation_rules list in block style")
	}
	for _, r := range rules.Entries {
		rule, ok := mappingOf(r.Value)
		if !ok {
			return shape("a creation rule isn't a mapping")
		}
		groups, ok := blockList(fieldOf(rule, "key_groups"))
		if !ok {
			return shape("a creation rule has no key_groups list (its keys are on the rule itself)")
		}
		n := len(sh.ages)
		for _, g := range groups.Entries {
			group, ok := mappingOf(g.Value)
			if !ok {
				return shape("a key group isn't a mapping")
			}
			age := fieldOf(group, "age")
			if age == nil {
				continue
			}
			l, ok := blockList(age)
			if !ok {
				return shape("a key group's age: isn't a list in block style")
			}
			sh.ages = append(sh.ages, endOf(l))
		}
		if len(sh.ages) == n {
			return shape("a creation rule has no age list in its key_groups")
		}
	}
	sh.anchors = anchorNames{}
	ast.Walk(anchorNames(sh.anchors), f.Docs[0].Body)
	return sh, nil
}

// checkRecipientShape: nil when addRecipient can edit src.
func checkRecipientShape(src []byte) error {
	_, err := readSopsShape(src)
	return err
}

// recipientLines are the lines to add by hand to a .sops.yaml addRecipient
// refused.
func recipientLines(anchor, pub string) string {
	return "  under keys:\n" +
		"      - &" + anchor + " " + pub + "\n" +
		"  and in every creation rule's key_groups, in its age list:\n" +
		"      - *" + anchor + "\n"
}

// addRecipient adds pub to the .sops.yaml at path: `- &<anchor> <pub>` at the
// end of keys:, and `- *<anchor>` at the end of every key_groups age list.
// The anchor is host, or host-2, host-3… when that is taken. A pub already in
// keys: leaves the file alone (changed false). A shape it doesn't edit is a
// *recipientShapeError, the file untouched.
func addRecipient(path, host, pub string) (anchor string, changed bool, err error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	sh, err := readSopsShape(src)
	if err != nil {
		return "", false, err
	}
	if slices.Contains(sh.pubs, pub) {
		return "", false, nil
	}
	anchor = host
	for i := 2; sh.anchors[anchor]; i++ {
		anchor = fmt.Sprintf("%s-%d", host, i)
	}
	after := map[int][]string{}
	add := func(e listEnd, item string) {
		after[e.line] = append(after[e.line], strings.Repeat(" ", e.col-1)+"- "+item+"\n")
	}
	add(sh.keys, "&"+anchor+" "+pub)
	for _, e := range sh.ages {
		add(e, "*"+anchor)
	}
	text := string(src)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	var b strings.Builder
	for i, l := range strings.SplitAfter(strings.TrimSuffix(text, "\n"), "\n") {
		b.WriteString(strings.TrimSuffix(l, "\n") + "\n")
		for _, ins := range after[i+1] {
			b.WriteString(ins)
		}
	}
	// WriteFile keeps an existing file's mode.
	return anchor, true, os.WriteFile(path, []byte(b.String()), 0o644)
}
