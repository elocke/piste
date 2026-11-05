package userscope

// budget: the start-of-session context of a Claude
// transcript, measured from its first listing attachments; ≈ tokens =
// chars / 4. A structured value counts as the code points of its compact
// JSON.

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Measure is one budget measurement, in output order.
type Measure struct{ *Obj }

func jsonLen(v any) int { return runeLen(encode(v, "")) }

// MeasureTranscript measures the skill listing in a session transcript; nil
// when there is none.
func MeasureTranscript(path string) (*Obj, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	first := map[string]*Obj{}
	var firstOrder []string
	type hk struct{ event, name string }
	hooks := map[hk]int{}
	var hookOrder []hk
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			if v, perr := ParseOrdered([]byte(strings.TrimRight(line, "\r\n"))); perr == nil {
				if d, ok := v.(*Obj); ok && str(d.Get("type")) == "attachment" {
					a, _ := d.Get("attachment").(*Obj)
					if a == nil {
						a = NewObj()
					}
					t := str(a.Get("type"))
					if a.Get("type") == nil {
						t = "\x00null"
					}
					if t == "hook_additional_context" {
						k := hk{str(a.Get("hookEvent")), str(a.Get("hookName"))}
						if _, seen := hooks[k]; !seen {
							hooks[k] = jsonLen(a.Get("content"))
							hookOrder = append(hookOrder, k)
						}
					} else if _, seen := first[t]; !seen {
						first[t] = a
						firstOrder = append(firstOrder, t)
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	sl, ok := first["skill_listing"]
	if !ok {
		return nil, nil
	}
	get := func(k string) *Obj {
		if o := first[k]; o != nil {
			return o
		}
		return NewObj()
	}
	al, dt := get("agent_listing_delta"), get("deferred_tools_delta")
	content := ""
	if s, ok := sl.Get("content").(string); ok {
		content = s
	}
	nameOnly := 0
	for _, x := range strings.Split(content, "\n") {
		if strings.HasPrefix(x, "- ") && !strings.Contains(x, ": ") {
			nameOnly++
		}
	}
	joinLen := func(v any) int {
		var parts []string
		for _, x := range asAny(v) {
			parts = append(parts, str(x))
		}
		return runeLen(strings.Join(parts, "\n"))
	}
	orDefault := func(o *Obj, k string, def any) any {
		if o.Has(k) {
			return o.Get(k)
		}
		return def
	}
	dtLines := dt.Get("addedLines")
	if !truthy(dtLines) {
		dtLines = []any{}
	}
	sessionStart, userPrompt := 0, 0
	for _, k := range hookOrder {
		switch k.event {
		case "SessionStart":
			sessionStart += hooks[k]
		case "UserPromptSubmit":
			userPrompt += hooks[k]
		}
	}
	out := NewObj()
	out.Set("transcript", path)
	out.Set("agents", len(asAny(orDefault(al, "addedTypes", []any{}))))
	out.Set("agent_chars", joinLen(orDefault(al, "addedLines", []any{})))
	out.Set("skills", sl.Get("skillCount"))
	out.Set("skill_chars", runeLen(content))
	out.Set("name_only", nameOnly)
	out.Set("deferred_tools", len(asAny(orDefault(dt, "addedNames", []any{}))))
	out.Set("deferred_chars", joinLen(dtLines))
	out.Set("mcp_instr_chars", jsonLen(orDefault(get("mcp_instructions_delta"), "addedBlocks", "")))
	out.Set("instructions_chars", jsonLen(orDefault(get("instructions"), "files", "")))
	out.Set("sessionstart_hook_chars", sessionStart)
	out.Set("userprompt_hook_chars_first", userPrompt)
	total := 0
	for _, k := range []string{"agent_chars", "skill_chars", "deferred_chars", "mcp_instr_chars", "instructions_chars", "sessionstart_hook_chars"} {
		total += out.Get(k).(int)
	}
	out.Set("start_chars_total", total)
	out.Set("start_tokens_est", int(math.RoundToEven(float64(total)/4)))
	return out, nil
}

// LatestMeasure measures the newest transcripts under root until one has a
// skill listing (up to limit).
func LatestMeasure(root string, limit int) (*Obj, error) {
	files, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	type fm struct {
		p string
		t int64
	}
	var fs []fm
	for _, p := range files {
		if st, err := os.Stat(p); err == nil {
			fs = append(fs, fm{p, st.ModTime().UnixNano()})
		}
	}
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].t > fs[j].t })
	for i, f := range fs {
		if i >= limit {
			break
		}
		m, err := MeasureTranscript(f.p)
		if err != nil {
			return nil, err
		}
		if m != nil {
			return m, nil
		}
	}
	return nil, nil
}

// BudgetOutput prints a measurement and returns the exit code.
func BudgetOutput(m *Obj, threshold int, asJSON bool, out io.Writer) int {
	m.Set("threshold", threshold)
	over := m.Get("start_tokens_est").(int) > threshold
	m.Set("over", over)
	if asJSON {
		fmt.Fprintln(out, encode(m, "  "))
	} else {
		for _, k := range m.Keys {
			fmt.Fprintf(out, "%-28s %s\n", k, str(m.Vals[k]))
		}
		verdict := "OK"
		if over {
			verdict = "WARN"
		}
		fmt.Fprintf(out, "\n%s: ~%d start tokens (threshold %d)\n", verdict, m.Get("start_tokens_est").(int), threshold)
	}
	if over {
		return 1
	}
	return 0
}
