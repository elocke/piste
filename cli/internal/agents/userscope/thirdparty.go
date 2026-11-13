package userscope

// The user-scope third-party items: zone
// declarations (agents.base.toml + the zone's agents.toml), the zone's
// agents.lock, check rows, sync (through the harness CLIs, never their
// files), update (writes agents.lock).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/elocke/piste/cli/internal/platform"
)

// LockVersion of agents.lock.
const LockVersion = 1

type kindSpec struct {
	name string
	path []string
	key  string
}

// Kinds in lock order.
var tpKinds = []kindSpec{
	{"skill", []string{"skill"}, "name"},
	{"claude.marketplace", []string{"claude", "marketplace"}, "name"},
	{"claude.plugin", []string{"claude", "plugin"}, "id"},
	{"codex.plugin", []string{"codex", "plugin"}, "id"},
	{"mcp", []string{"mcp"}, "name"},
	{"hook", []string{"hook"}, "name"},
	{"tool", []string{"tool"}, "name"},
}

// lockFields: each kind's lock entry fields, in write order.
var lockFields = map[string][]string{
	"skill":              {"name", "source", "path", "ref", "commit", "narHash", "fetched"},
	"claude.marketplace": {"name", "source", "ref", "commit", "sha256", "fetched"},
	"claude.plugin":      {"id", "version", "gitCommitSha", "fetched"},
	"codex.plugin":       {"id", "version", "source", "fetched"},
	"mcp":                {"name", "package", "version", "integrity", "fetched"},
	"hook":               {"name", "installer", "version", "integrity", "fetched"},
	"tool":               {"name", "source", "version", "integrity", "fetched"},
}

// LockError is a malformed or unusable agents.lock.
type LockError struct{ Msg string }

func (e LockError) Error() string { return e.Msg }

func lockErrf(format string, a ...any) error { return LockError{fmt.Sprintf(format, a...)} }

// Item is one declaration or lock entry (TOML table).
type Item = map[string]any

// ByKind is kind → key → item.
type ByKind map[string]map[string]Item

func emptyByKind() ByKind {
	out := ByKind{}
	for _, k := range tpKinds {
		out[k.name] = map[string]Item{}
	}
	return out
}

func table(data map[string]any, path []string) []any {
	var cur any = data
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	l, _ := cur.([]any)
	return l
}

func byKey(data map[string]any) (ByKind, error) {
	out := emptyByKind()
	for _, ks := range tpKinds {
		for _, x := range table(data, ks.path) {
			it, _ := x.(map[string]any)
			k := tstr(it[ks.key])
			if !truthyT(it[ks.key]) {
				return nil, lockErrf("a [[%s]] entry has no %s", strings.Join(ks.path, "."), ks.key)
			}
			if _, dup := out[ks.name][k]; dup {
				return nil, lockErrf("%s '%s' is declared twice", ks.name, k)
			}
			out[ks.name][k] = it
		}
	}
	return out, nil
}

// tstr is a TOML value as text: a string as is, a missing value as
// "none", anything else as TOML writes it.
func tstr(v any) string {
	switch x := v.(type) {
	case nil:
		return "none"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return fmt.Sprint(x)
	case float64:
		return tomlFloat(x)
	}
	return fmt.Sprint(v)
}

// gs is item k as a string ("" when missing).
func gs(it Item, k string) string {
	if s, ok := it[k].(string); ok {
		return s
	}
	if it[k] == nil {
		return ""
	}
	return tstr(it[k])
}

func truthyT(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case int64:
		return x != 0
	case []any:
		return len(x) > 0
	}
	return true
}

func eqv(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return fmt.Sprintf("%#v", a) == fmt.Sprintf("%#v", b)
}

func readTOMLMap(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := toml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Policy is a zone's [policy].
type Policy struct {
	Allow, Tolerate   []string
	Require, Unlisted string
}

// Zone is the merged declarations of a zone: base manifest + zone manifest
// (+ the manifests of the zone's other roots: an overlay's own agents/ beside
// its team layer's).
type Zone struct {
	// Order is each kind's keys in declaration order.
	Order  map[string][]string
	Owned  map[string]bool
	Files  []string
	Decls  ByKind
	Policy Policy
	// extends: the manifests whose [policy] says extends = true. Such a root
	// adds to a zone whose policy another root sets: allow entries, and a
	// lock of its own holding only what it declares.
	extends map[string]bool
}

// Extends: manifest f is a root that extends its zone ([policy] extends = true).
func (z *Zone) Extends(f string) bool { return z.extends[f] }

// NewZone loads the manifests.
func NewZone(manifests []string, owned []string) (*Zone, error) {
	z := &Zone{Order: map[string][]string{}, Owned: map[string]bool{}, Files: manifests, Decls: emptyByKind(),
		Policy: Policy{Require: "commit", Unlisted: "warn"}, extends: map[string]bool{}}
	var policyRoots []string
	for _, o := range owned {
		z.Owned[o] = true
	}
	for _, f := range manifests {
		data, err := readTOMLMap(f)
		if err != nil {
			return nil, err
		}
		bk, err := byKey(data)
		if err != nil {
			return nil, err
		}
		for _, ks := range tpKinds {
			for _, x := range table(data, ks.path) {
				k := tstr(x.(map[string]any)[ks.key])
				it := bk[ks.name][k]
				if _, dup := z.Decls[ks.name][k]; dup {
					return nil, lockErrf("%s '%s' is declared in more than one manifest (%s)", ks.name, k, f)
				}
				c := Item{}
				for kk, v := range it {
					c[kk] = v
				}
				c["_file"] = f
				z.Decls[ks.name][k] = c
				z.Order[ks.name] = append(z.Order[ks.name], k)
			}
		}
		pol, _ := data["policy"].(map[string]any)
		// One root of the zone sets its policy; another (extends = true)
		// only adds allow entries. The shared catalog's [policy] is neither.
		if ext, _ := pol["extends"].(bool); ext {
			for k := range pol {
				if k != "extends" && k != "allow" {
					return nil, lockErrf("%s: [policy] extends = true only adds allow entries to the zone's policy; %s is for the root that sets it", f, k)
				}
			}
			z.extends[f] = true
		} else if pol != nil && filepath.Base(f) != baseManifest {
			policyRoots = append(policyRoots, f)
		}
		for _, x := range asList(pol["allow"]) {
			z.Policy.Allow = append(z.Policy.Allow, tstr(x))
		}
		for _, x := range asList(pol["tolerate"]) {
			z.Policy.Tolerate = append(z.Policy.Tolerate, tstr(x))
		}
		if v, ok := pol["require"]; ok {
			z.Policy.Require = tstr(v)
		}
		if v, ok := pol["unlisted"]; ok {
			z.Policy.Unlisted = tstr(v)
		}
	}
	if len(policyRoots) > 1 {
		return nil, lockErrf("more than one manifest sets the zone's [policy] (%s): another root of the zone adds to it with [policy] extends = true", strings.Join(policyRoots, ", "))
	}
	z.dropUnusedCatalogMarkets()
	used, err := presetSkills(manifests)
	if err != nil {
		return nil, err
	}
	z.dropUnusedCatalogSkills(used)
	return z, nil
}

// presetSkills: every skill a preset beside the manifests names (each
// manifest's directory is a zone root: <root>/presets/*.toml). That is the
// zone-wide closure of what lib/agents.nix calls usedSkills (one host's
// presets): the lock is per zone, so it keeps what any host selects.
func presetSkills(manifests []string) (map[string]bool, error) {
	used := map[string]bool{}
	seen := map[string]bool{}
	for _, f := range manifests {
		dir := filepath.Join(filepath.Dir(f), "presets")
		if seen[dir] {
			continue
		}
		seen[dir] = true
		files, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
		sort.Strings(files)
		for _, p := range files {
			data, err := readTOMLMap(p)
			if err != nil {
				return nil, err
			}
			for _, x := range asList(data["skills"]) {
				used[tstr(x)] = true
			}
		}
	}
	return used, nil
}

// dropUnusedCatalogSkills: a [[skill]] of the shared catalog
// (agents.base.toml) is the zone's only if `used` names it; one the zone
// declares itself always is. An unused catalog skill is not policy-checked,
// checked or locked for the zone (lib/agents.nix gates it the same way:
// usedSkills, the third-party skills the host's presets select).
func (z *Zone) dropUnusedCatalogSkills(used map[string]bool) {
	var order []string
	for _, name := range z.Order["skill"] {
		if filepath.Base(gs(z.Decls["skill"][name], "_file")) == baseManifest && !used[name] {
			delete(z.Decls["skill"], name)
			continue
		}
		order = append(order, name)
	}
	z.Order["skill"] = order
}

// baseManifest is the shared catalog every zone reads.
const baseManifest = "agents.base.toml"

// dropUnusedCatalogMarkets: a [[claude.marketplace]] of the shared catalog
// (agents.base.toml) is the zone's only if one of its [[claude.plugin]]s
// names it (x@<name>); one the zone declares itself always is. An unused
// catalog marketplace is not checked, locked or installed for the zone, like
// an unused catalog skill in lib/agents.nix (usedMarketplaces, the same rule).
func (z *Zone) dropUnusedCatalogMarkets() {
	named := map[string]bool{}
	for pid := range z.Decls["claude.plugin"] {
		if _, m, ok := strings.Cut(pid, "@"); ok {
			named[m] = true
		}
	}
	var order []string
	for _, name := range z.Order["claude.marketplace"] {
		it := z.Decls["claude.marketplace"][name]
		if filepath.Base(gs(it, "_file")) == baseManifest && !named[name] {
			delete(z.Decls["claude.marketplace"], name)
			continue
		}
		order = append(order, name)
	}
	z.Order["claude.marketplace"] = order
}

func asList(v any) []any { l, _ := v.([]any); return l }

// LoadLock reads a zone lock (empty when absent).
func LoadLock(path string) (ByKind, error) {
	if path == "" {
		return emptyByKind(), nil
	}
	if _, err := os.Stat(path); err != nil {
		return emptyByKind(), nil
	}
	data, err := readTOMLMap(path)
	if err != nil {
		return nil, err
	}
	if v, ok := data["version"].(int64); !ok || v != LockVersion {
		got := "none"
		if data["version"] != nil {
			got = tomlValue(data["version"])
		}
		return nil, lockErrf("%s: lock version %s, expected %d", path, got, LockVersion)
	}
	return byKey(data)
}

// LoadLocks reads every lock of a zone's roots into one: a layer's and an
// overlay root's beside it. An item locked in two of them is an error, not a
// choice between two pins.
func LoadLocks(paths []string) (ByKind, error) {
	out := emptyByKind()
	from := map[string]string{}
	for _, p := range paths {
		lk, err := LoadLock(p)
		if err != nil {
			return nil, err
		}
		for _, ks := range tpKinds {
			for _, key := range sortedKeys(lk[ks.name]) {
				id := ks.name + " " + key
				if prev, dup := from[id]; dup {
					return nil, lockErrf("%s '%s' is locked in more than one agents.lock (%s, %s)", ks.name, key, prev, p)
				}
				from[id] = p
				out[ks.name][key] = lk[ks.name][key]
			}
		}
	}
	return out, nil
}

// Own is the zone narrowed to what manifest f declares, for `update` in a
// root that extends its zone, plus the marketplaces f's plugins name (their
// versions are read from them); OwnLock then keeps only f's own items.
func (z *Zone) Own(f string) *Zone {
	n := &Zone{Order: map[string][]string{}, Owned: z.Owned, Files: z.Files, Decls: emptyByKind(), Policy: z.Policy, extends: z.extends}
	named := map[string]bool{}
	for _, pid := range z.Order["claude.plugin"] {
		if gs(z.Decls["claude.plugin"][pid], "_file") == f {
			if _, m, ok := strings.Cut(pid, "@"); ok {
				named[m] = true
			}
		}
	}
	for _, ks := range tpKinds {
		for _, key := range z.Order[ks.name] {
			it := z.Decls[ks.name][key]
			if gs(it, "_file") == f || (ks.name == "claude.marketplace" && named[key]) {
				n.Decls[ks.name][key] = it
				n.Order[ks.name] = append(n.Order[ks.name], key)
			}
		}
	}
	return n
}

// OwnLock is lk with only the items manifest f declares, plus the shared
// catalog's marketplaces that f's plugins name and no other lock of the zone
// pins (nobody else locks those, and a used marketplace without a pin fails
// check). taken is the lock of the root that sets the zone's policy
// (PolicyRootLock): a catalog marketplace stays here unless taken pins it.
// With taken nil (that lock unknown), one stays only if old, f's lock
// before update, already pinned it.
func OwnLock(z *Zone, lk, old ByKind, f string, taken ByKind) ByKind {
	named := map[string]bool{}
	for key, it := range z.Decls["claude.plugin"] {
		if gs(it, "_file") == f {
			if _, m, ok := strings.Cut(key, "@"); ok {
				named[m] = true
			}
		}
	}
	keepCatalog := func(key string) bool {
		if !named[key] {
			return false
		}
		if taken == nil {
			_, had := old["claude.marketplace"][key]
			return had
		}
		_, pinned := taken["claude.marketplace"][key]
		return !pinned
	}
	out := emptyByKind()
	for _, ks := range tpKinds {
		for key, e := range lk[ks.name] {
			it, ok := z.Decls[ks.name][key]
			if !ok {
				continue
			}
			if gs(it, "_file") == f || (ks.name == "claude.marketplace" && keepCatalog(key)) {
				out[ks.name][key] = e
			}
		}
	}
	return out
}

// PolicyRootLock is the pins of the root that sets the policy of rootDir's
// zone (its agents.toml has no [policy] extends = true), read from manifest m.
// It is nil when m is not of rootDir's zone: no source of m has rootDir as
// its workdir (an overlay edited on a host of another zone, or outside its
// checkout).
func PolicyRootLock(m *Manifest, rootDir string) (ByKind, error) {
	resolve := func(p string) string {
		if a, err := filepath.Abs(p); err == nil {
			p = a
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return p
	}
	root, ours := resolve(rootDir), false
	for _, e := range asAny(m.Raw.Get("sources")) {
		o, _ := e.(*Obj)
		if w, _ := o.Get("workdir").(string); w != "" && resolve(w) == root {
			ours = true
		}
	}
	if !ours {
		return nil, nil
	}
	tomls, locks := map[string]string{}, map[string]string{}
	var sources []string
	for _, e := range asAny(m.Raw.Get("thirdParty")) {
		o, _ := e.(*Obj)
		src := str(o.Get("source"))
		switch str(o.Get("file")) {
		case "agents.toml":
			tomls[src] = str(o.Get("path"))
			sources = append(sources, src)
		case "agents.lock":
			locks[src] = str(o.Get("path"))
		}
	}
	var paths []string
	for _, src := range sources {
		if locks[src] == "" {
			continue
		}
		data, err := readTOMLMap(tomls[src])
		if err != nil {
			return nil, err
		}
		pol, _ := data["policy"].(map[string]any)
		if ext, _ := pol["extends"].(bool); !ext {
			paths = append(paths, locks[src])
		}
	}
	return LoadLocks(paths)
}

func tomlValue(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int64:
		return fmt.Sprint(x)
	case float64:
		return tomlFloat(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = tomlValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return tomlQuote(tstr(v))
}

// tomlQuote is s as a TOML basic string.
func tomlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlFloat is f as a TOML float (always with a fraction or exponent).
func tomlFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

// DumpLock renders a zone's agents.lock.
func DumpLock(l ByKind, zoneName string) string {
	lines := []string{
		"# agents.lock: machine-written by `piste agents update`; do not edit by hand.",
		"# Pins for every third-party item declared in agents.toml (+ the shared",
		"# agents.base.toml items this zone uses).",
		fmt.Sprintf("version = %d", LockVersion),
	}
	if zoneName != "" {
		lines = append(lines, "zone = "+tomlValue(zoneName))
	}
	for _, ks := range tpKinds {
		keys := sortedKeys(l[ks.name])
		for _, k := range keys {
			e := l[ks.name][k]
			lines = append(lines, "", "[["+strings.Join(ks.path, ".")+"]]")
			width := 0
			for _, f := range lockFields[ks.name] {
				if len(f) > width {
					width = len(f)
				}
			}
			for _, f := range lockFields[ks.name] {
				if v, ok := e[f]; ok && v != nil && v != "" {
					lines = append(lines, fmt.Sprintf("%-*s = %s", width, f, tomlValue(v)))
				}
			}
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Hostname is this machine's host name without ".local".
func Hostname() string { return platform.ShortHostname() }

func applies(it Item, host string) bool {
	hs, ok := it["hosts"]
	if !ok || hs == nil {
		return true
	}
	for _, h := range asList(hs) {
		if tstr(h) == host {
			return true
		}
	}
	return false
}

// --- sources and policy ---------------------------------------------------------

func npmSpec(spec string) (string, string, bool) {
	i := strings.LastIndex(spec, "@")
	if i <= 0 {
		return spec, "", false
	}
	return spec[:i], spec[i+1:], true
}

// mcpPackage: the package a stdio MCP server runs through npx/uvx.
func mcpPackage(it Item) (string, string, bool) {
	cmd := gs(it, "command")
	if i := strings.LastIndex(cmd, "/"); i >= 0 {
		cmd = cmd[i+1:]
	}
	var args []string
	for _, a := range asList(it["args"]) {
		args = append(args, tstr(a))
	}
	switch cmd {
	case "npx", "pnpx", "bunx":
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				name, ver, ok := npmSpec(a)
				if !ok {
					return "npm:" + name, "", true
				}
				return "npm:" + name, ver, true
			}
		}
	case "uvx":
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch a {
			case "--with", "--from", "--python", "-p", "--index", "--index-url", "--extra-index-url":
				i++
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			name, ver, _ := strings.Cut(a, "==")
			return "pypi:" + name, ver, true
		}
	}
	return "", "", false
}

func itemSource(kind string, it Item) (string, bool) {
	switch kind {
	case "skill", "claude.marketplace", "tool":
		v, ok := it["source"].(string)
		return v, ok
	case "hook":
		v, ok := it["installer"].(string)
		return v, ok
	case "codex.plugin":
		_, after, _ := strings.Cut(gs(it, "id"), "@")
		return "codex:" + after, true
	case "mcp":
		p, _, ok := mcpPackage(it)
		return p, ok
	}
	return "", false
}

func allowed(src string, has bool, p Policy) bool {
	if !has || strings.HasPrefix(src, "nix:") {
		return true
	}
	for _, pat := range p.Allow {
		if fnmatchCase(src, pat) {
			return true
		}
	}
	return false
}

// codexAppTolerate: the Codex app's own plugins (browser, computer-use,
// documents, pdf, spreadsheets, …), from marketplaces it ships and installs
// itself. No agents.toml declares them and they aren't the person's to
// remove, so every zone tolerates them, as a [policy] tolerate entry would.
var codexAppTolerate = []string{"codex.plugin:*@openai-bundled", "codex.plugin:*@openai-primary-runtime"}

func tolerated(kind, key string, p Policy) bool {
	for _, pat := range append(append([]string{}, codexAppTolerate...), p.Tolerate...) {
		if fnmatchCase(kind+":"+key, pat) {
			return true
		}
	}
	return false
}

// fnmatchCase is a case-sensitive shell glob match (*, ?, [...]); *
// matches '/', too.
func fnmatchCase(name, pat string) bool {
	var b strings.Builder
	b.WriteString(`(?s)\A`)
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		switch c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := i + 1
			if j < len(pat) && pat[j] == '!' {
				j++
			}
			if j < len(pat) && pat[j] == ']' {
				j++
			}
			for j < len(pat) && pat[j] != ']' {
				j++
			}
			if j >= len(pat) {
				b.WriteString(`\[`)
				continue
			}
			stuff := pat[i+1 : j]
			if strings.HasPrefix(stuff, "!") {
				stuff = "^" + stuff[1:]
			} else if strings.HasPrefix(stuff, "^") {
				stuff = `\` + stuff
			}
			b.WriteString("[" + strings.ReplaceAll(stuff, `\`, `\\`) + "]")
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`\z`)
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(name)
}

// --- observing the harnesses ----------------------------------------------------

// Observer is what the harnesses have now (tests use fakes on PATH).
type Observer interface {
	ClaudeMarketplaces() (map[string]Item, error)
	ClaudePlugins() (map[string]Item, error)
	CodexPlugins() (map[string]Item, error)
	PnpmGlobals() map[string]string
	WhichAll(name string) []string
	FileVersion(path string) (string, bool)
}

// RealObserver runs the harness CLIs.
type RealObserver struct{ Home string }

func run(argv []string, timeout time.Duration, env []string, stdinNull bool) (string, string, int, error) {
	c := exec.Command(argv[0], argv[1:]...)
	if env == nil {
		env = childEnv()
	}
	c.Env = env
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if stdinNull {
		c.Stdin = nil
	}
	done := make(chan error, 1)
	if err := c.Start(); err != nil {
		return "", "", -1, err
	}
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			return out.String(), errb.String(), -1, err
		}
		return out.String(), errb.String(), code, nil
	case <-time.After(timeout):
		c.Process.Kill()
		return out.String(), errb.String(), -1, fmt.Errorf("timed out after %s", timeout)
	}
}

func jsonCmd(argv []string) (any, error) {
	out, errs, code, err := run(argv, 180*time.Second, nil, false)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, lockErrf("%s failed: %s", strings.Join(argv, " "), truncate(strings.TrimSpace(errs), 300))
	}
	return ParseOrdered([]byte(out))
}

func claudeConfigDir(home string) string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".claude")
}

const rawGitHub = "url:https://raw.githubusercontent.com/"

const pluginJSON = ".claude-plugin/marketplace.json"

// rawGitHubSource splits a url: marketplace source into (git url, ref, path in
// the repo). Only raw.githubusercontent.com URLs are supported: their
// OWNER/REPO/REF resolves to a commit like a github: source does.
func rawGitHubSource(source string) (string, string, string, error) {
	var parts []string
	if strings.HasPrefix(source, rawGitHub) {
		parts = strings.SplitN(source[len(rawGitHub):], "/", 4)
	}
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return "", "", "", lockErrf("unsupported source %s: url: needs https://raw.githubusercontent.com/OWNER/REPO/REF/PATH", strconv.Quote(source))
	}
	return "https://github.com/" + parts[0] + "/" + parts[1] + ".git", parts[2], parts[3], nil
}

// floats: a marketplace added from its own source rather than a pinned
// mirror: pin = "source", or any url: source (Claude fetches the URL itself).
func floats(market Item) bool {
	return gs(market, "pin") == "source" || strings.HasPrefix(gs(market, "source"), "url:")
}

// JSONSHA256 is the sha256 of a JSON document in canonical form
// (canonical), so key order and whitespace are not a change: Claude
// re-serialises a url marketplace's file.
func JSONSHA256(data []byte) (string, error) {
	v, err := ParseOrdered(data)
	if err != nil {
		return "", err
	}
	return sha(canonical(v)), nil
}

// marketplaceJSONSHA256 hashes an installed url marketplace: Claude stores
// the file at installLocation itself, or (older installs) in .claude-plugin/.
func marketplaceJSONSHA256(loc string) (string, bool) {
	f := loc
	if st, err := os.Stat(loc); err == nil && st.IsDir() {
		f = filepath.Join(loc, ".claude-plugin", "marketplace.json")
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return "", false
	}
	h, err := JSONSHA256(b)
	return h, err == nil
}

func checkoutCommit(path string) (string, bool) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		out, _, code, err := run([]string{"git", "-C", path, "rev-parse", "HEAD"}, 30*time.Second, nil, false)
		if err == nil && code == 0 {
			return strings.TrimSpace(out), true
		}
	}
	if b, err := os.ReadFile(filepath.Join(path, ".gcs-sha")); err == nil {
		return strings.TrimSpace(string(b)), true
	}
	return "", false
}

func objStr(o *Obj, k string) (string, bool) {
	v := o.Get(k)
	if v == nil {
		return "", false
	}
	return str(v), true
}

func (r RealObserver) ClaudeMarketplaces() (map[string]Item, error) {
	v, err := jsonCmd([]string{"claude", "plugin", "marketplace", "list", "--json"})
	if err != nil {
		return nil, err
	}
	out := map[string]Item{}
	for _, x := range asAny(v) {
		m, _ := x.(*Obj)
		loc, ok := objStr(m, "installLocation")
		if !ok || loc == "" {
			loc, ok = objStr(m, "path")
		}
		var src string
		switch str(m.Get("source")) {
		case "github":
			src = "github:" + str(m.Get("repo"))
		case "directory":
			src = "directory:" + str(m.Get("path"))
		default:
			var alt any
			for _, k := range []string{"url", "repo", "path"} {
				if truthy(m.Get(k)) {
					alt = m.Get(k)
					break
				}
			}
			if alt == nil {
				alt = m.Get("path")
			}
			src = str(m.Get("source")) + ":" + str(alt)
		}
		it := Item{"source": src}
		if ok && loc != "" {
			it["path"] = loc
			if c, ok := checkoutCommit(loc); ok {
				it["commit"] = c
			}
		}
		if strings.HasPrefix(src, "url:") {
			it["sha256"] = nil
			if ok && loc != "" {
				if h, ok := marketplaceJSONSHA256(loc); ok {
					it["sha256"] = h
				}
			}
		}
		out[str(m.Get("name"))] = it
	}
	return out, nil
}

func asAny(v any) []any { l, _ := v.([]any); return l }

func (r RealObserver) ClaudePlugins() (map[string]Item, error) {
	shas := map[string]any{}
	installed := filepath.Join(claudeConfigDir(r.Home), "plugins", "installed_plugins.json")
	if b, err := os.ReadFile(installed); err == nil {
		v, err := ParseOrdered(b)
		if err != nil {
			return nil, err
		}
		root, _ := v.(*Obj)
		pl, _ := root.Get("plugins").(*Obj)
		if pl != nil {
			for _, pid := range pl.Keys {
				for _, e := range asAny(pl.Vals[pid]) {
					eo, _ := e.(*Obj)
					if str(eo.Get("scope")) == "user" {
						shas[pid] = eo.Get("gitCommitSha")
					}
				}
			}
		}
	}
	v, err := jsonCmd([]string{"claude", "plugin", "list", "--json"})
	if err != nil {
		return nil, err
	}
	out := map[string]Item{}
	for _, x := range asAny(v) {
		p, _ := x.(*Obj)
		if str(p.Get("scope")) != "user" {
			continue
		}
		en := any(true)
		if p.Has("enabled") {
			en = p.Get("enabled")
		}
		out[str(p.Get("id"))] = Item{"version": p.Get("version"), "enabled": truthy(en), "gitCommitSha": shas[str(p.Get("id"))]}
	}
	return out, nil
}

func (r RealObserver) CodexPlugins() (map[string]Item, error) {
	v, err := jsonCmd([]string{"codex", "plugin", "list", "--json"})
	if err != nil {
		return nil, err
	}
	d, _ := v.(*Obj)
	out := map[string]Item{}
	for _, x := range asAny(d.Get("installed")) {
		p, _ := x.(*Obj)
		en := any(true)
		if p.Has("enabled") {
			en = p.Get("enabled")
		}
		var src any
		if so, ok := p.Get("source").(*Obj); ok {
			src = so.Get("source")
		}
		out[str(p.Get("pluginId"))] = Item{"version": p.Get("version"), "enabled": truthy(en), "source": src}
	}
	return out, nil
}

func (r RealObserver) PnpmGlobals() map[string]string {
	out, _, code, err := run([]string{"pnpm", "list", "-g", "--depth=0", "--json"}, 60*time.Second, nil, false)
	if err != nil || code != 0 {
		return map[string]string{}
	}
	if out == "" {
		out = "[]"
	}
	v, err := ParseOrdered([]byte(out))
	if err != nil {
		return map[string]string{}
	}
	res := map[string]string{}
	arr := asAny(v)
	if len(arr) == 0 {
		return res
	}
	first, _ := arr[0].(*Obj)
	deps, _ := first.Get("dependencies").(*Obj)
	if deps == nil {
		return res
	}
	for _, n := range deps.Keys {
		d, _ := deps.Vals[n].(*Obj)
		res[n] = str(d.Get("version"))
	}
	return res
}

func (r RealObserver) WhichAll(name string) []string {
	var hits []string
	seen := map[string]bool{}
	for _, d := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		p := filepath.Join(d, name)
		if d == "" {
			p = name
		}
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() || fi.Mode()&0o111 == 0 || seen[p] {
			continue
		}
		seen[p] = true
		hits = append(hits, p)
	}
	return hits
}

func (r RealObserver) FileVersion(path string) (string, bool) {
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(r.Home, path[2:])
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	v, err := ParseOrdered(b)
	if err != nil {
		return "", false
	}
	o, _ := v.(*Obj)
	if o == nil || o.Get("version") == nil {
		return "", false
	}
	return str(o.Get("version")), true
}

func isNix(path string) bool {
	r, err := filepath.EvalSymlinks(path)
	if err != nil {
		r = path
	}
	return strings.HasPrefix(r, "/nix/store/")
}

func toolBins(name string, it Item) []string {
	if b := asList(it["bins"]); len(b) > 0 {
		out := make([]string, len(b))
		for i, x := range b {
			out[i] = tstr(x)
		}
		return out
	}
	if b, ok := it["bin"]; ok {
		return []string{tstr(b)}
	}
	return []string{name}
}

func shadows(hits []string) []string {
	for i, h := range hits {
		if isNix(h) {
			if i == 0 {
				return nil
			}
			return hits[:i]
		}
	}
	return nil
}

func c12(s string) string { return truncate(s, 12) }

func orQ(v any) string {
	if !truthyAny(v) {
		return "?"
	}
	return fmt.Sprint(v)
}

func truthyAny(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	}
	return truthy(v)
}

// pstr is a value from a lock, a manifest or a harness's JSON as message
// text: tstr, with JSON objects and arrays as their JSON.
func pstr(v any) string {
	switch v.(type) {
	case *Obj, []any:
		return str(v)
	}
	return tstr(v)
}

// ThirdPartyRows is the check rows of the zone's third-party items: each
// declared item against the lock and what this machine has, and what the
// machine has that nothing declares.
func ThirdPartyRows(z *Zone, lk ByKind, obs Observer, host string) ([]Row, error) {
	var rows []Row
	d := ByKind{}
	for k, v := range z.Decls {
		d[k] = map[string]Item{}
		for n, it := range v {
			if applies(it, host) {
				d[k][n] = it
			}
		}
	}
	for _, ks := range tpKinds {
		for _, key := range sortedKeys(z.Decls[ks.name]) {
			it := z.Decls[ks.name][key]
			src, has := itemSource(ks.name, it)
			if !allowed(src, has, z.Policy) {
				detail := fmt.Sprintf("source %s is not in [policy] allow", src)
				if !has {
					detail = "it has no source for [policy] allow to name"
				}
				rows = append(rows, Row{"policy", ks.name, key, detail})
			}
		}
	}
	for _, name := range sortedKeys(d["skill"]) {
		it := d["skill"][name]
		e, ok := lk["skill"][name]
		pathOr := func(x Item) any {
			if v, ok := x["path"]; ok {
				return v
			}
			return "."
		}
		switch {
		case !ok:
			rows = append(rows, Row{"drift", "skill", name, "declared but not in agents.lock (run piste agents update)"})
		case !eqv(e["source"], it["source"]) || !eqv(e["ref"], it["ref"]) || !eqv(pathOr(e), pathOr(it)):
			rows = append(rows, Row{"drift", "skill", name, "agents.lock entry is stale (source/path/ref changed)"})
		}
	}
	mk, err := obs.ClaudeMarketplaces()
	if err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(d["claude.marketplace"]) {
		it := d["claude.marketplace"][name]
		e := lk["claude.marketplace"][name]
		o, ok := mk[name]
		switch {
		case !ok:
			rows = append(rows, Row{"missing", "claude.marketplace", name, gs(it, "source")})
		case !truthyT(e["commit"]):
			rows = append(rows, Row{"drift", "claude.marketplace", name, "not in agents.lock (run piste agents update)"})
		case strings.HasPrefix(gs(it, "source"), "url:"):
			st := "ok"
			if !eqv(o["sha256"], e["sha256"]) {
				st = "floating"
			}
			rows = append(rows, Row{st, "claude.marketplace", name, fmt.Sprintf("marketplace.json %s, lock %s @ %s (url source: added from its URL, not mirrored)", c12(orQ(o["sha256"])), c12(orQ(e["sha256"])), c12(gs(e, "commit")))})
		case gs(it, "pin") == "source":
			st := "ok"
			if !eqv(o["commit"], e["commit"]) {
				st = "floating"
			}
			rows = append(rows, Row{st, "claude.marketplace", name, fmt.Sprintf("at %s, lock %s (pin = source: added from its source, not mirrored)", c12(orQ(o["commit"])), c12(gs(e, "commit")))})
		case !eqv(o["commit"], e["commit"]):
			rows = append(rows, Row{"drift", "claude.marketplace", name, fmt.Sprintf("at %s, lock %s", c12(orQ(o["commit"])), c12(gs(e, "commit")))})
		default:
			rows = append(rows, Row{"ok", "claude.marketplace", name, fmt.Sprintf("%s (%s)", c12(gs(e, "commit")), gs(o, "source"))})
		}
	}
	for _, name := range sortedKeys(mk) {
		if _, ok := z.Decls["claude.marketplace"][name]; !ok {
			rows = append(rows, undeclared(z, "claude.marketplace", name, gs(mk[name], "source")))
		}
	}
	cp, err := obs.ClaudePlugins()
	if err != nil {
		return nil, err
	}
	for _, pid := range sortedKeys(d["claude.plugin"]) {
		e := lk["claude.plugin"][pid]
		o, ok := cp[pid]
		if !ok {
			rows = append(rows, Row{"missing", "claude.plugin", pid, ""})
			continue
		}
		var problems []string
		_, mkt, _ := strings.Cut(pid, "@")
		market := z.Decls["claude.marketplace"][mkt]
		want := wantEnabled(d["claude.plugin"][pid])
		if en, _ := o["enabled"].(bool); want && !en {
			problems = append(problems, "disabled")
		} else if !want && en {
			problems = append(problems, "enabled (declared enabled = false)")
		}
		if len(e) == 0 {
			problems = append(problems, "not in agents.lock")
		} else if floats(market) {
			if !eqv(o["version"], e["version"]) || (truthyT(e["gitCommitSha"]) && !eqv(o["gitCommitSha"], e["gitCommitSha"])) {
				if len(problems) == 0 {
					rows = append(rows, Row{"floating", "claude.plugin", pid, fmt.Sprintf("%s (lock %s); marketplace pin = source", pstr(o["version"]), pstr(e["version"]))})
					continue
				}
			}
		} else {
			if truthyT(e["version"]) && !eqv(o["version"], e["version"]) {
				problems = append(problems, fmt.Sprintf("version %s != lock %s", pstr(o["version"]), pstr(e["version"])))
			}
			if truthyT(e["gitCommitSha"]) && !eqv(o["gitCommitSha"], e["gitCommitSha"]) {
				problems = append(problems, fmt.Sprintf("commit %s != lock %s", c12(orQ(o["gitCommitSha"])), c12(gs(e, "gitCommitSha"))))
			}
		}
		st, det := "ok", strings.TrimSpace(fmt.Sprintf("%s %s", pstr(o["version"]), c12(orEmpty(o["gitCommitSha"]))))
		if len(problems) > 0 {
			st, det = "drift", strings.Join(problems, "; ")
		}
		rows = append(rows, Row{st, "claude.plugin", pid, det})
	}
	for _, pid := range sortedKeys(cp) {
		if _, ok := z.Decls["claude.plugin"][pid]; !ok {
			rows = append(rows, undeclared(z, "claude.plugin", pid, pstr(cp[pid]["version"])))
		}
	}
	xp, err := obs.CodexPlugins()
	if err != nil {
		return nil, err
	}
	for _, pid := range sortedKeys(d["codex.plugin"]) {
		e := lk["codex.plugin"][pid]
		o, ok := xp[pid]
		switch {
		case !ok:
			rows = append(rows, Row{"missing", "codex.plugin", pid, ""})
		case o["enabled"] == false:
			rows = append(rows, Row{"drift", "codex.plugin", pid, "disabled"})
		case !eqv(o["source"], "remote") && truthyT(e["version"]) && !eqv(o["version"], e["version"]):
			rows = append(rows, Row{"drift", "codex.plugin", pid, fmt.Sprintf("version %s != lock %s", pstr(o["version"]), pstr(e["version"]))})
		default:
			src := "local"
			if truthyAny(o["source"]) {
				src = pstr(o["source"])
			}
			rows = append(rows, Row{"ok", "codex.plugin", pid, fmt.Sprintf("%s (%s)", pstr(o["version"]), src)})
		}
	}
	for _, pid := range sortedKeys(xp) {
		if _, ok := z.Decls["codex.plugin"][pid]; !ok {
			rows = append(rows, undeclared(z, "codex.plugin", pid, pstr(xp[pid]["version"])))
		}
	}
	for _, name := range sortedKeys(d["mcp"]) {
		pkg, ver, ok := mcpPackage(d["mcp"][name])
		if !ok {
			continue
		}
		e := lk["mcp"][name]
		var verAny any
		if ver != "" {
			verAny = ver
		}
		if gs(e, "package") == pkg && eqv(e["version"], verAny) && truthyT(e["integrity"]) {
			rows = append(rows, Row{"ok", "mcp", name, pkg + "@" + pstr(verAny)})
		} else {
			rows = append(rows, Row{"drift", "mcp", name, fmt.Sprintf("%s@%s not locked with integrity (run piste agents update)", pkg, pstr(verAny))})
		}
	}
	for _, name := range sortedKeys(d["hook"]) {
		it := d["hook"][name]
		if gs(it, "mode") == "tolerated" {
			rows = append(rows, Row{"tolerated", "hook", name, "follows its binary"})
			continue
		}
		e := lk["hook"][name]
		if !eqv(e["version"], it["version"]) {
			rows = append(rows, Row{"drift", "hook", name, fmt.Sprintf("installer %s not locked (run piste agents update)", pstr(it["version"]))})
			continue
		}
		var have string
		var hasHave bool
		if vf := gs(it, "versionFile"); vf != "" {
			have, hasHave = obs.FileVersion(vf)
		}
		switch {
		case truthyT(it["versionFile"]) && !hasHave:
			rows = append(rows, Row{"missing", "hook", name, gs(it, "versionFile") + " not found"})
		case hasHave && have != pstr(e["version"]):
			rows = append(rows, Row{"drift", "hook", name, fmt.Sprintf("installed %s != lock %s", have, pstr(e["version"]))})
		default:
			rows = append(rows, Row{"ok", "hook", name, fmt.Sprintf("%s@%s", pstr(it["installer"]), pstr(e["version"]))})
		}
	}
	var pnpm map[string]string
	for _, name := range sortedKeys(d["tool"]) {
		it := d["tool"][name]
		src := gs(it, "source")
		bins := toolBins(name, it)
		hits := obs.WhichAll(bins[0])
		switch {
		case strings.HasPrefix(src, "npm:"):
			if pnpm == nil {
				pnpm = obs.PnpmGlobals()
			}
			e := lk["tool"][name]
			have, ok := pnpm[src[4:]]
			switch {
			case !ok:
				rows = append(rows, Row{"missing", "tool", name, src + " not in the pnpm global bucket"})
			case !eqv(e["version"], it["version"]):
				rows = append(rows, Row{"drift", "tool", name, fmt.Sprintf("%s not locked (run piste agents update)", pstr(it["version"]))})
			case have != pstr(e["version"]):
				rows = append(rows, Row{"drift", "tool", name, fmt.Sprintf("installed %s != lock %s", have, pstr(e["version"]))})
			default:
				rows = append(rows, Row{"ok", "tool", name, src + "@" + have})
			}
		case strings.HasPrefix(src, "nix:"):
			var sh []string
			for _, b := range bins {
				sh = append(sh, shadows(obs.WhichAll(b))...)
			}
			anyNix := false
			for _, h := range hits {
				anyNix = anyNix || isNix(h)
			}
			switch {
			case len(hits) == 0:
				rows = append(rows, Row{"missing", "tool", name, src + " not on PATH (piste switch)"})
			case !anyNix:
				rows = append(rows, Row{"missing", "tool", name, fmt.Sprintf("no Nix copy on PATH; only %s (not ours)", strings.Join(hits, ", "))})
			case len(sh) > 0:
				rows = append(rows, Row{"drift", "tool", name, fmt.Sprintf("shadowed by %s (the Nix copy is later on PATH)", strings.Join(sh, ", "))})
			default:
				rows = append(rows, Row{"ok", "tool", name, hits[0]})
			}
		default:
			rows = append(rows, Row{"policy", "tool", name, fmt.Sprintf("source %s: tools come from nix: or npm:", strconv.Quote(src))})
		}
	}
	return rows, nil
}

func orEmpty(v any) string {
	if !truthyAny(v) {
		return ""
	}
	return fmt.Sprint(v)
}

func undeclared(z *Zone, kind, key, detail string) Row {
	if z.Owned[kind+":"+key] {
		return Row{"ok", kind, key, "owned by piste (hook carrier; `piste agents check` hook rows)"}
	}
	if tolerated(kind, key, z.Policy) {
		return Row{"tolerated", kind, key, detail}
	}
	return Row{"undeclared", kind, key, detail}
}

// HealthyStates is the row states that are not a problem in zone z;
// "undeclared" is one unless the zone's policy says unlisted = "deny".
func HealthyStates(z *Zone) map[string]bool {
	s := map[string]bool{"ok": true, "deferred": true, "unmanaged": true, "foreign": true, "tolerated": true, "floating": true}
	if z == nil || z.Policy.Unlisted != "deny" {
		s["undeclared"] = true
	}
	return s
}

// ZoneFromManifest: the zone manifests + locks this generation was built
// from (one per root that has one; LoadLocks reads them as one).
func ZoneFromManifest(m *Manifest) (*Zone, []string, error) {
	var manifests, locks []string
	for _, e := range asAny(m.Raw.Get("thirdParty")) {
		o, _ := e.(*Obj)
		switch str(o.Get("file")) {
		case "agents.base.toml", "agents.toml":
			manifests = append(manifests, str(o.Get("path")))
		case "agents.lock":
			locks = append(locks, str(o.Get("path")))
		}
	}
	if len(manifests) == 0 {
		return nil, nil, nil
	}
	var owned []string
	hooks, _ := m.Raw.Get("hooks").(*Obj)
	carrier, _ := hooks.Get("claude").(*Obj)
	if p := getS(carrier, "plugin"); p != "" {
		owned = append(owned, "claude.plugin:"+p)
	}
	if p := getS(carrier, "marketplace_name"); p != "" {
		owned = append(owned, "claude.marketplace:"+p)
	}
	z, err := NewZone(manifests, owned)
	if err != nil {
		return nil, nil, err
	}
	// This host's use, exactly lib/agents.nix usedSkills: the third-party
	// skills its presets select, linked from the lock or deferred.
	used := map[string]bool{}
	for _, l := range m.Links {
		if l.Kind == "skills" && l.Repo == "third-party" {
			used[l.Name] = true
		}
	}
	for _, n := range m.Deferred {
		used[n] = true
	}
	z.dropUnusedCatalogSkills(used)
	return z, locks, nil
}

// --- sync ---------------------------------------------------------------------

// Step is one sync action: an argv, a mirror pin, or a move to TRASH.
type Step struct {
	Desc   string
	Argv   []string
	Mirror *mirrorSpec
	Trash  string
}

// mirrorSpec pins a marketplace mirror.
type mirrorSpec struct{ name, url, commit string }

func (m *mirrorSpec) dir(mirrorRoot string) string {
	return filepath.Join(mirrorRoot, m.name)
}

func githubURL(source string) (string, error) {
	if !strings.HasPrefix(source, "github:") {
		return "", lockErrf("unsupported source %s: only github:owner/repo", strconv.Quote(source))
	}
	return "https://github.com/" + source[7:] + ".git", nil
}

// wantEnabled: a plugin is enabled unless declared enabled = false.
func wantEnabled(it Item) bool {
	v, ok := it["enabled"]
	if !ok {
		return true
	}
	b, isBool := v.(bool)
	return !isBool || b
}

func sourceArg(it Item) (string, error) {
	src := gs(it, "source")
	if strings.HasPrefix(src, "url:") {
		if _, _, _, err := rawGitHubSource(src); err != nil {
			return "", err
		}
		return src[4:], nil
	}
	if !strings.HasPrefix(src, "github:") {
		return "", lockErrf("unsupported source %s", strconv.Quote(src))
	}
	out := src[7:]
	if truthyT(it["ref"]) {
		out += "#" + gs(it, "ref")
	}
	return out, nil
}

// PlanSync plans a sync: (steps, removals, notes); nothing runs here.
func PlanSync(z *Zone, lk ByKind, obs Observer, prune, repin bool, host, mirrorRoot string) ([]Step, []Step, []string, error) {
	d := ByKind{}
	for k, v := range z.Decls {
		d[k] = map[string]Item{}
		for n, it := range v {
			if applies(it, host) {
				d[k][n] = it
			}
		}
	}
	var steps, removals []Step
	var notes []string
	mk, err := obs.ClaudeMarketplaces()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, name := range sortedKeys(d["claude.marketplace"]) {
		it := d["claude.marketplace"][name]
		e, ok := lk["claude.marketplace"][name]
		if !ok || !truthyT(e["commit"]) {
			notes = append(notes, fmt.Sprintf("claude.marketplace %s: not locked; run `piste agents update` first", name))
			continue
		}
		mirror := filepath.Join(mirrorRoot, name)
		o, have := mk[name]
		if have && eqv(o["commit"], e["commit"]) {
			continue
		}
		if floats(it) {
			if !have {
				arg, err := sourceArg(it)
				if err != nil {
					return nil, nil, nil, err
				}
				why := "pin = source"
				if strings.HasPrefix(gs(it, "source"), "url:") {
					why = "url source"
				}
				steps = append(steps, Step{Desc: fmt.Sprintf("add marketplace %s from its source (%s)", name, why), Argv: []string{"claude", "plugin", "marketplace", "add", arg}})
			}
			continue
		}
		url, err := githubURL(gs(it, "source"))
		if err != nil {
			return nil, nil, nil, err
		}
		ms := Step{Desc: fmt.Sprintf("pin mirror %s at %s", mirror, c12(gs(e, "commit"))), Mirror: &mirrorSpec{name, url, gs(e, "commit")}}
		switch {
		case !have:
			steps = append(steps, ms, Step{Desc: "add marketplace " + name, Argv: []string{"claude", "plugin", "marketplace", "add", mirror}})
		case gs(o, "source") == "directory:"+mirror:
			steps = append(steps, ms, Step{Desc: "refresh marketplace " + name, Argv: []string{"claude", "plugin", "marketplace", "update", name}})
		case repin:
			steps = append(steps,
				Step{Desc: fmt.Sprintf("remove marketplace %s (%s); Claude uninstalls its plugins and deletes their saved options/data", name, gs(o, "source")), Argv: []string{"claude", "plugin", "marketplace", "remove", name}},
				ms,
				Step{Desc: fmt.Sprintf("add marketplace %s (pinned mirror)", name), Argv: []string{"claude", "plugin", "marketplace", "add", mirror}})
			delete(mk, name)
		default:
			notes = append(notes, fmt.Sprintf("claude.marketplace %s: at %s, lock %s (%s); `piste agents sync --user --repin` switches it to a pinned mirror", name, c12(orQ(o["commit"])), c12(gs(e, "commit")), gs(o, "source")))
		}
	}
	cp, err := obs.ClaudePlugins()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, pid := range sortedKeys(d["claude.plugin"]) {
		o, have := cp[pid]
		_, market, _ := strings.Cut(pid, "@")
		want := wantEnabled(d["claude.plugin"][pid])
		disable := Step{Desc: fmt.Sprintf("disable %s (declared enabled = false)", pid), Argv: []string{"claude", "plugin", "disable", pid, "--scope", "user"}}
		if _, mkHave := mk[market]; !have || !mkHave {
			steps = append(steps, Step{Desc: "install " + pid, Argv: []string{"claude", "plugin", "install", pid, "--scope", "user"}})
			if !want {
				steps = append(steps, disable)
			}
			continue
		}
		e := lk["claude.plugin"][pid]
		floating := floats(z.Decls["claude.marketplace"][market])
		if !floating && ((truthyT(e["version"]) && !eqv(o["version"], e["version"])) || (truthyT(e["gitCommitSha"]) && !eqv(o["gitCommitSha"], e["gitCommitSha"]))) {
			steps = append(steps, Step{Desc: fmt.Sprintf("update %s to the locked marketplace", pid), Argv: []string{"claude", "plugin", "update", pid, "--scope", "user"}})
		}
		if en, _ := o["enabled"].(bool); want && !en {
			steps = append(steps, Step{Desc: "enable " + pid, Argv: []string{"claude", "plugin", "enable", pid, "--scope", "user"}})
		} else if !want && en {
			steps = append(steps, disable)
		}
	}
	xp, err := obs.CodexPlugins()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, pid := range sortedKeys(d["codex.plugin"]) {
		if _, ok := xp[pid]; !ok {
			steps = append(steps, Step{Desc: "add codex plugin " + pid, Argv: []string{"codex", "plugin", "add", pid}})
		}
	}
	var pnpm map[string]string
	for _, name := range sortedKeys(d["tool"]) {
		it := d["tool"][name]
		src := gs(it, "source")
		e := lk["tool"][name]
		if strings.HasPrefix(src, "npm:") && truthyT(e["version"]) {
			if pnpm == nil {
				pnpm = obs.PnpmGlobals()
			}
			if have, ok := pnpm[src[4:]]; !ok || have != pstr(e["version"]) {
				spec := src[4:] + "@" + pstr(e["version"])
				steps = append(steps, Step{Desc: fmt.Sprintf("pin %s in the pnpm bucket", spec), Argv: []string{"pnpm", "add", "-g", spec}})
			}
		}
	}
	for _, name := range sortedKeys(d["hook"]) {
		it := d["hook"][name]
		if gs(it, "mode") != "managed" {
			continue
		}
		e := lk["hook"][name]
		var have any
		if vf := gs(it, "versionFile"); vf != "" {
			if v, ok := obs.FileVersion(vf); ok {
				have = v
			}
		}
		if truthyT(it["versionFile"]) && !eqv(have, e["version"]) {
			installed := "not installed"
			if have != nil {
				installed = "installed " + pstr(have)
			}
			notes = append(notes, fmt.Sprintf("hook %s: %s, lock %s; run its installer (%s@%s) by hand", name, installed, pstr(e["version"]), pstr(it["installer"]), pstr(e["version"])))
		}
	}
	if prune {
		declaredMarkets := map[string]bool{}
		for pid := range z.Decls["claude.plugin"] {
			_, m, _ := strings.Cut(pid, "@")
			declaredMarkets[m] = true
		}
		for _, pid := range sortedKeys(cp) {
			if _, ok := z.Decls["claude.plugin"][pid]; ok {
				continue
			}
			if !tolerated("claude.plugin", pid, z.Policy) && !z.Owned["claude.plugin:"+pid] {
				removals = append(removals, Step{Desc: "uninstall undeclared claude plugin " + pid, Argv: []string{"claude", "plugin", "uninstall", pid, "--scope", "user"}})
			}
		}
		for _, name := range sortedKeys(mk) {
			if _, ok := z.Decls["claude.marketplace"][name]; ok {
				continue
			}
			if declaredMarkets[name] || tolerated("claude.marketplace", name, z.Policy) || z.Owned["claude.marketplace:"+name] {
				continue
			}
			removals = append(removals, Step{Desc: fmt.Sprintf("remove undeclared marketplace %s (%s) and any plugins still installed from it", name, gs(mk[name], "source")), Argv: []string{"claude", "plugin", "marketplace", "remove", name}})
		}
		for _, pid := range sortedKeys(xp) {
			if _, ok := z.Decls["codex.plugin"][pid]; ok {
				continue
			}
			if !tolerated("codex.plugin", pid, z.Policy) {
				removals = append(removals, Step{Desc: "remove undeclared codex plugin " + pid, Argv: []string{"codex", "plugin", "remove", pid}})
			}
		}
		for _, name := range sortedKeys(d["tool"]) {
			it := d["tool"][name]
			if strings.HasPrefix(gs(it, "source"), "nix:") {
				for _, b := range toolBins(name, it) {
					for _, h := range shadows(obs.WhichAll(b)) {
						removals = append(removals, Step{Desc: fmt.Sprintf("move %s (shadows the Nix %s) to ~/TRASH", h, name), Trash: h})
					}
				}
			}
		}
	}
	return steps, removals, notes, nil
}

func ensureMirror(name, url, commit, root string) (string, error) {
	mirror := filepath.Join(root, name)
	if _, err := os.Stat(filepath.Join(mirror, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
			return "", err
		}
		_, errs, code, err := run([]string{"git", "clone", "--quiet", url, mirror}, 600*time.Second, nil, false)
		if err != nil || code != 0 {
			return "", lockErrf("clone %s: %s", url, truncate(strings.TrimSpace(errs), 300))
		}
	}
	if c, _ := checkoutCommit(mirror); c != commit {
		run([]string{"git", "-C", mirror, "fetch", "--quiet", "origin", commit}, 600*time.Second, nil, false)
		_, errs, code, err := run([]string{"git", "-C", mirror, "-c", "advice.detachedHead=false", "checkout", "--quiet", commit}, 120*time.Second, nil, false)
		if err != nil || code != 0 {
			return "", lockErrf("checkout %s in %s: %s", commit, mirror, truncate(strings.TrimSpace(errs), 300))
		}
	}
	return mirror, nil
}

func runStep(s Step, out io.Writer, home, mirrorRoot string) (bool, error) {
	switch {
	case s.Mirror != nil:
		_, err := ensureMirror(s.Mirror.name, s.Mirror.url, s.Mirror.commit, filepath.Dir(s.Mirror.dir(mirrorRoot)))
		return err == nil, err
	case s.Trash != "":
		dest := filepath.Join(home, "TRASH", "piste-agents-"+time.Now().Format("2006-01-02"))
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return false, err
		}
		return true, os.Rename(s.Trash, filepath.Join(dest, filepath.Base(s.Trash)))
	}
	o, e, code, err := run(s.Argv, 600*time.Second, nil, true)
	if err != nil || code != 0 {
		msg := e
		if strings.TrimSpace(msg) == "" {
			msg = o
		}
		if err != nil {
			msg = err.Error()
		}
		fmt.Fprintf(out, "  failed: %s\n", truncate(strings.TrimSpace(msg), 400))
		return false, nil
	}
	return true, nil
}

func showAction(s Step, mirrorRoot string) string {
	switch {
	case s.Mirror != nil:
		return fmt.Sprintf("git clone/checkout %s @ %s -> %s", s.Mirror.url, s.Mirror.commit, s.Mirror.dir(mirrorRoot))
	case s.Trash != "":
		return fmt.Sprintf("mv %s ~/TRASH/", s.Trash)
	}
	return strings.Join(s.Argv, " ")
}

// CmdSync is `piste agents sync`: it returns the exit code.
func CmdSync(z *Zone, lk ByKind, obs Observer, prune, apply, dryRun, repin bool, out io.Writer, home string) (int, error) {
	mirrorRoot := filepath.Join(home, ".local", "share", "piste-agents", "marketplaces")
	steps, removals, notes, err := PlanSync(z, lk, obs, prune, repin, Hostname(), mirrorRoot)
	if err != nil {
		return 0, err
	}
	for _, n := range notes {
		fmt.Fprintf(out, "note: %s\n", n)
	}
	failed := false
	if len(steps) == 0 {
		fmt.Fprintln(out, "sync: nothing to install or pin")
	}
	for _, s := range steps {
		if dryRun {
			fmt.Fprintf(out, "would %s\n  $ %s\n", s.Desc, showAction(s, mirrorRoot))
			continue
		}
		fmt.Fprintln(out, s.Desc)
		ok, err := runStep(s, out, home, mirrorRoot)
		if err != nil {
			var le LockError
			if !errors.As(err, &le) {
				return 0, err
			}
			fmt.Fprintf(out, "  failed: %s\n", le.Msg)
			ok = false
		}
		failed = failed || !ok
	}
	if prune {
		switch {
		case len(removals) == 0:
			fmt.Fprintln(out, "prune: nothing undeclared to remove")
		case !apply:
			fmt.Fprintf(out, "prune (dry run; add --apply to remove these %d):\n", len(removals))
			for _, r := range removals {
				fmt.Fprintf(out, "  - %s\n    $ %s\n", r.Desc, showAction(r, mirrorRoot))
			}
		default:
			for _, r := range removals {
				fmt.Fprintln(out, r.Desc)
				ok, err := runStep(r, out, home, mirrorRoot)
				if err != nil {
					return 0, err
				}
				if !ok {
					failed = true
				}
			}
		}
	}
	if failed {
		return 1, nil
	}
	return 0, nil
}
