package userscope

// update: resolve pins and write a zone's agents.lock.
// Network: git ls-remote, a Nix fetchgit prefetch for skill
// narHashes, the npm / PyPI registries for integrity, and a scratch
// CLAUDE_CONFIG_DIR for plugin versions.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Today is the fetched date (a var for tests).
var Today = func() string { return time.Now().Format("2006-01-02") }

var hex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

// LsRemote resolves a ref (tag, branch, or "" = HEAD) to a commit.
func LsRemote(url, ref string) (string, error) {
	if hex40.MatchString(ref) {
		return ref, nil
	}
	var wanted, args []string
	if ref != "" {
		wanted = []string{"refs/tags/" + ref + "^{}", "refs/tags/" + ref, "refs/heads/" + ref}
		args = []string{"refs/tags/" + ref + "*", "refs/heads/" + ref}
	} else {
		wanted, args = []string{"HEAD"}, []string{"HEAD"}
	}
	out, errs, code, err := run(append([]string{"git", "ls-remote", url}, args...), 60*time.Second, nil, false)
	if err != nil || code != 0 {
		return "", lockErrf("git ls-remote %s: %s", url, truncate(strings.TrimSpace(errs), 200))
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		sha, name, _ := strings.Cut(line, "\t")
		if line != "" {
			refs[name] = sha
		}
	}
	for _, w := range wanted {
		if s, ok := refs[w]; ok {
			return s, nil
		}
	}
	r := ref
	if r == "" {
		r = "HEAD"
	}
	return "", lockErrf("%s: ref %s not found", url, r)
}

// PrefetchSkill is the narHash of pkgs.fetchgit at commit (sparse to path),
// computed by building it with a fake hash.
func PrefetchSkill(url, commit, path, nix string) (string, error) {
	sparse := ""
	if path != "" && path != "." {
		q, _ := json.Marshal(path)
		sparse = fmt.Sprintf("sparseCheckout = [ %s ];", q)
	}
	qu, _ := json.Marshal(url)
	qc, _ := json.Marshal(commit)
	expr := fmt.Sprintf(`let pkgs = import (builtins.getFlake "nixpkgs") { }; in pkgs.fetchgit { url = %s; rev = %s; hash = pkgs.lib.fakeHash; %s }`, qu, qc, sparse)
	_, errs, _, _ := run([]string{nix, "build", "--no-link", "--impure", "--expr", expr}, 1800*time.Second, nil, false)
	for _, line := range strings.Split(errs, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "got:") {
			return strings.TrimSpace(strings.SplitN(line, ":", 2)[1]), nil
		}
	}
	tail := errs
	if r := []rune(tail); len(r) > 600 {
		tail = string(r[len(r)-600:])
	}
	return "", lockErrf("prefetch %s@%s (%s): no hash in nix output:\n%s", url, commit, path, tail)
}

// FetchURL GETs a URL (a var for tests).
var FetchURL = func(url string) ([]byte, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP Error %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return io.ReadAll(resp.Body)
}

// RegistryBase is overridable for tests.
var RegistryBase = map[string]string{"npm": "https://registry.npmjs.org", "pypi": "https://pypi.org/pypi"}

// RegistryIntegrity: npm dist.integrity, or sha256 of the PyPI sdist.
func RegistryIntegrity(source, version string) (string, error) {
	kind, name, _ := strings.Cut(source, ":")
	get := func(url string) (any, error) {
		c := &http.Client{Timeout: 30 * time.Second}
		resp, err := c.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("HTTP Error %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return ParseOrdered(b)
	}
	switch kind {
	case "npm":
		v, err := get(fmt.Sprintf("%s/%s/%s", RegistryBase["npm"], name, version))
		if err != nil {
			return "", err
		}
		d, _ := v.(*Obj).Get("dist").(*Obj)
		return str(d.Get("integrity")), nil
	case "pypi":
		v, err := get(fmt.Sprintf("%s/%s/%s/json", RegistryBase["pypi"], name, version))
		if err != nil {
			return "", err
		}
		files := asAny(v.(*Obj).Get("urls"))
		var pick *Obj
		for _, f := range files {
			if fo, _ := f.(*Obj); str(fo.Get("packagetype")) == "sdist" {
				pick = fo
				break
			}
		}
		if pick == nil && len(files) > 0 {
			pick, _ = files[0].(*Obj)
		}
		if pick == nil {
			return "", lockErrf("%s==%s: no release files", source, version)
		}
		dg, _ := pick.Get("digests").(*Obj)
		return "sha256:" + str(dg.Get("sha256")), nil
	}
	return "", lockErrf("no registry for %s", source)
}

// SandboxPlugins installs the declared plugins into a scratch
// CLAUDE_CONFIG_DIR from marketplaces pinned at markets, and reads back
// version + gitCommitSha.
func SandboxPlugins(z *Zone, markets map[string]string, claude string) (map[string]Item, error) {
	tmp, err := os.MkdirTemp("", "piste-agents-update-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	env := append(childEnv(), "CLAUDE_CONFIG_DIR="+filepath.Join(tmp, "claude"))
	root := filepath.Join(tmp, "mirrors")
	for _, name := range sortedKeys(markets) {
		it := z.Decls["claude.marketplace"][name]
		var arg string
		git, _, path, rawErr := rawGitHubSource(gs(it, "source"))
		if rawErr == nil && strings.HasSuffix(path, pluginJSON) {
			// Claude refuses to install a fetched (url) marketplace's relative
			// plugin paths: read the versions from its directory in a mirror
			// at the locked commit instead.
			m, err := ensureMirror(name, git, markets[name], root)
			if err != nil {
				return nil, err
			}
			arg = filepath.Join(m, strings.TrimSuffix(path, pluginJSON))
		} else if floats(it) {
			if arg, err = sourceArg(it); err != nil {
				return nil, err
			}
		} else {
			u, err := githubURL(gs(it, "source"))
			if err != nil {
				return nil, err
			}
			if arg, err = ensureMirror(name, u, markets[name], root); err != nil {
				return nil, err
			}
		}
		_, errs, code, err := run([]string{claude, "plugin", "marketplace", "add", arg}, 300*time.Second, env, false)
		if err != nil || code != 0 {
			return nil, lockErrf("sandbox marketplace add %s: %s", name, truncate(strings.TrimSpace(errs), 300))
		}
	}
	for _, pid := range sortedKeys(z.Decls["claude.plugin"]) {
		_, m, _ := strings.Cut(pid, "@")
		if _, ok := markets[m]; !ok {
			continue
		}
		o, errs, code, err := run([]string{claude, "plugin", "install", pid, "--scope", "user"}, 600*time.Second, env, true)
		if err != nil || code != 0 {
			msg := errs
			if strings.TrimSpace(msg) == "" {
				msg = o
			}
			return nil, lockErrf("sandbox install %s: %s", pid, truncate(strings.TrimSpace(msg), 300))
		}
	}
	b, err := os.ReadFile(filepath.Join(tmp, "claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return nil, err
	}
	v, err := ParseOrdered(b)
	if err != nil {
		return nil, err
	}
	out := map[string]Item{}
	if pl, ok := v.(*Obj).Get("plugins").(*Obj); ok {
		for _, pid := range pl.Keys {
			es := asAny(pl.Vals[pid])
			if len(es) == 0 {
				continue
			}
			e0, _ := es[0].(*Obj)
			out[pid] = Item{"version": e0.Get("version"), "gitCommitSha": e0.Get("gitCommitSha")}
		}
	}
	return out, nil
}

func cloneItem(e Item) Item {
	c := Item{}
	for k, v := range e {
		c[k] = v
	}
	return c
}

// jsonToTOML converts observed JSON values into TOML-ish Go values for the
// lock (strings stay strings, numbers become strings of their literal).
func jsonToTOML(v any) any {
	switch x := v.(type) {
	case json.Number:
		return string(x)
	case nil:
		return nil
	}
	return v
}

// Update resolves the zone's pins and returns the new lock. names nil = every item
// moves (--all); otherwise only those (others keep their entries).
func Update(z *Zone, old ByKind, names map[string]bool, fromInstalled bool, obs Observer, out io.Writer, nix, claude string) (ByKind, error) {
	nw := emptyByKind()
	moving := func(k string) bool { return names == nil || names[k] }
	keep := func(kind, key string, valid func(Item) bool) bool {
		e, ok := old[kind][key]
		if ok && !moving(key) && valid(e) {
			nw[kind][key] = e
			return true
		}
		return false
	}
	pathOr := func(x Item) any {
		if v, ok := x["path"]; ok {
			return v
		}
		return "."
	}
	for _, name := range sortedKeys(z.Decls["skill"]) {
		it := z.Decls["skill"][name]
		same := func(e Item) bool {
			return eqv(e["source"], it["source"]) && eqv(e["ref"], it["ref"]) && eqv(pathOr(e), pathOr(it))
		}
		if keep("skill", name, same) {
			continue
		}
		url, err := githubURL(gs(it, "source"))
		if err != nil {
			return nil, err
		}
		commit, err := LsRemote(url, gs(it, "ref"))
		if err != nil {
			return nil, err
		}
		prev := old["skill"][name]
		if gs(prev, "commit") == commit && same(prev) && truthyT(prev["narHash"]) {
			nw["skill"][name] = prev
			continue
		}
		refS := "HEAD"
		if truthyT(it["ref"]) {
			refS = gs(it, "ref")
		}
		fmt.Fprintf(out, "skill %s: %s %s -> %s; prefetching\n", name, url, refS, c12(commit))
		p := tstr(pathOr(it))
		nh, err := PrefetchSkill(url, commit, p, nix)
		if err != nil {
			return nil, err
		}
		e := Item{"name": name, "source": it["source"], "path": pathOr(it), "commit": commit, "narHash": nh, "fetched": Today()}
		if v, ok := it["ref"]; ok {
			e["ref"] = v
		}
		nw["skill"][name] = e
	}
	installedM := map[string]Item{}
	if fromInstalled {
		m, err := obs.ClaudeMarketplaces()
		if err != nil {
			return nil, err
		}
		installedM = m
	}
	for _, name := range sortedKeys(z.Decls["claude.marketplace"]) {
		it := z.Decls["claude.marketplace"][name]
		if keep("claude.marketplace", name, func(e Item) bool { return eqv(e["source"], it["source"]) }) {
			continue
		}
		if strings.HasPrefix(gs(it, "source"), "url:") {
			// Lock the commit REF resolves to, plus the file's canonical hash
			// at that commit (what check compares; a url install has no git).
			git, ref, path, err := rawGitHubSource(gs(it, "source"))
			if err != nil {
				return nil, err
			}
			commit, err := LsRemote(git, ref)
			if err != nil {
				return nil, err
			}
			prev := old["claude.marketplace"][name]
			if gs(prev, "commit") == commit && truthyT(prev["sha256"]) && eqv(prev["source"], it["source"]) {
				nw["claude.marketplace"][name] = prev
				continue
			}
			ownerRepo := strings.TrimSuffix(strings.TrimPrefix(git, "https://github.com/"), ".git")
			body, err := FetchURL(fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", ownerRepo, commit, path))
			if err != nil {
				return nil, err
			}
			sha, err := JSONSHA256(body)
			if err != nil {
				return nil, err
			}
			nw["claude.marketplace"][name] = Item{"name": name, "source": it["source"], "commit": commit, "sha256": sha, "fetched": Today()}
			continue
		}
		var commit string
		if im, ok := installedM[name]; fromInstalled && ok && truthyAny(im["commit"]) {
			commit = gs(im, "commit")
		} else {
			u, err := githubURL(gs(it, "source"))
			if err != nil {
				return nil, err
			}
			if commit, err = LsRemote(u, gs(it, "ref")); err != nil {
				return nil, err
			}
		}
		prev := old["claude.marketplace"][name]
		if gs(prev, "commit") == commit {
			nw["claude.marketplace"][name] = prev
		} else {
			e := Item{"name": name, "source": it["source"], "commit": commit, "fetched": Today()}
			if v, ok := it["ref"]; ok {
				e["ref"] = v
			}
			nw["claude.marketplace"][name] = e
		}
	}
	var movingPlugins []string
	for _, pid := range z.Order["claude.plugin"] { // declaration order, like the dict
		_, m, _ := strings.Cut(pid, "@")
		_, locked := old["claude.plugin"][pid]
		if moving(pid) || moving(m) || !locked {
			movingPlugins = append(movingPlugins, pid)
		} else {
			nw["claude.plugin"][pid] = old["claude.plugin"][pid]
		}
	}
	if len(movingPlugins) > 0 {
		var got map[string]Item
		var err error
		if fromInstalled {
			got, err = obs.ClaudePlugins()
		} else {
			markets := map[string]string{}
			for m := range nw["claude.marketplace"] {
				for _, p := range movingPlugins {
					if _, pm, _ := strings.Cut(p, "@"); pm == m {
						markets[m] = gs(nw["claude.marketplace"][m], "commit")
					}
				}
			}
			got, err = SandboxPlugins(z, markets, claude)
		}
		if err != nil {
			return nil, err
		}
		for _, pid := range movingPlugins {
			g, ok := got[pid]
			if !ok {
				where := " in the sandbox"
				if fromInstalled {
					where = " here"
				}
				return nil, lockErrf("claude plugin %s: not installed%s", pid, where)
			}
			prev := old["claude.plugin"][pid]
			entry := Item{"id": pid, "version": jsonToTOML(g["version"]), "gitCommitSha": jsonToTOML(g["gitCommitSha"])}
			if eqv(prev["version"], entry["version"]) && eqv(prev["gitCommitSha"], entry["gitCommitSha"]) {
				nw["claude.plugin"][pid] = prev
			} else {
				entry["fetched"] = Today()
				nw["claude.plugin"][pid] = entry
			}
		}
	}
	var seen map[string]*Obj
	need := false
	for pid := range z.Decls["codex.plugin"] {
		if _, locked := old["codex.plugin"][pid]; moving(pid) || !locked {
			need = true
		}
	}
	if need {
		v, err := jsonCmd([]string{"codex", "plugin", "list", "--json"})
		if err != nil {
			fmt.Fprintf(out, "codex plugins: kept as locked (%s)\n", truncate(err.Error(), 120))
		} else {
			seen = map[string]*Obj{}
			d, _ := v.(*Obj)
			for _, x := range append(asAny(d.Get("installed")), asAny(d.Get("available"))...) {
				if p, ok := x.(*Obj); ok {
					seen[str(p.Get("pluginId"))] = p
				}
			}
		}
	}
	for _, pid := range sortedKeys(z.Decls["codex.plugin"]) {
		if keep("codex.plugin", pid, func(Item) bool { return true }) {
			continue
		}
		if prev, ok := old["codex.plugin"][pid]; seen == nil && ok {
			nw["codex.plugin"][pid] = prev
			continue
		}
		if seen == nil {
			return nil, lockErrf("codex plugin %s: not locked yet and `codex plugin list` is unavailable", pid)
		}
		p, ok := seen[pid]
		if !ok {
			return nil, lockErrf("codex plugin %s: not in `codex plugin list`", pid)
		}
		src := "local"
		if so, ok := p.Get("source").(*Obj); ok && truthy(so.Get("source")) {
			src = str(so.Get("source"))
		}
		entry := Item{"id": pid, "version": jsonToTOML(p.Get("version")), "source": src}
		prev := old["codex.plugin"][pid]
		if eqv(prev["id"], entry["id"]) && eqv(prev["version"], entry["version"]) && eqv(prev["source"], entry["source"]) {
			nw["codex.plugin"][pid] = prev
		} else {
			entry["fetched"] = Today()
			nw["codex.plugin"][pid] = entry
		}
	}
	for _, name := range sortedKeys(z.Decls["mcp"]) {
		pkg, ver, ok := mcpPackage(z.Decls["mcp"][name])
		if !ok || ver == "" {
			continue
		}
		prev := old["mcp"][name]
		if gs(prev, "package") == pkg && gs(prev, "version") == ver && truthyT(prev["integrity"]) {
			nw["mcp"][name] = prev
			continue
		}
		in, err := RegistryIntegrity(pkg, ver)
		if err != nil {
			return nil, err
		}
		nw["mcp"][name] = Item{"name": name, "package": pkg, "version": ver, "integrity": in, "fetched": Today()}
	}
	for _, name := range sortedKeys(z.Decls["hook"]) {
		it := z.Decls["hook"][name]
		if gs(it, "mode") != "managed" {
			continue
		}
		prev := old["hook"][name]
		if eqv(prev["installer"], it["installer"]) && eqv(prev["version"], it["version"]) && truthyT(prev["integrity"]) {
			nw["hook"][name] = prev
			continue
		}
		in, err := RegistryIntegrity(gs(it, "installer"), gs(it, "version"))
		if err != nil {
			return nil, err
		}
		nw["hook"][name] = Item{"name": name, "installer": it["installer"], "version": it["version"], "integrity": in, "fetched": Today()}
	}
	for _, name := range sortedKeys(z.Decls["tool"]) {
		it := z.Decls["tool"][name]
		src := gs(it, "source")
		prev := old["tool"][name]
		if strings.HasPrefix(src, "npm:") {
			if !truthyT(it["version"]) {
				return nil, lockErrf("tool %s: npm tools need an exact version (require = commit)", name)
			}
			if gs(prev, "source") == src && eqv(prev["version"], it["version"]) && truthyT(prev["integrity"]) {
				nw["tool"][name] = prev
			} else {
				in, err := RegistryIntegrity(src, gs(it, "version"))
				if err != nil {
					return nil, err
				}
				nw["tool"][name] = Item{"name": name, "source": src, "version": it["version"], "integrity": in, "fetched": Today()}
			}
		} else if gs(prev, "source") == src && prev != nil {
			nw["tool"][name] = prev
		} else {
			nw["tool"][name] = Item{"name": name, "source": src, "fetched": Today()}
		}
	}
	return nw, nil
}

func itemsEqual(a, b Item) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || !eqv(v, w) {
			return false
		}
	}
	return true
}

// LockChanges is one line per lock entry added, removed or changed.
func LockChanges(old, nw ByKind) []string {
	var lines []string
	for _, ks := range tpKinds {
		keys := map[string]bool{}
		for k := range old[ks.name] {
			keys[k] = true
		}
		for k := range nw[ks.name] {
			keys[k] = true
		}
		ks2 := sortedKeys(keys)
		for _, k := range ks2 {
			a, aok := old[ks.name][k]
			b, bok := nw[ks.name][k]
			if aok && bok && itemsEqual(a, b) {
				continue
			}
			switch {
			case !aok:
				lines = append(lines, fmt.Sprintf("+ %s %s", ks.name, k))
			case !bok:
				lines = append(lines, fmt.Sprintf("- %s %s", ks.name, k))
			default:
				var diff []string
				for _, f := range lockFields[ks.name] {
					if f != "fetched" && !eqv(a[f], b[f]) {
						diff = append(diff, fmt.Sprintf("%s: %s -> %s", f, pstr(a[f]), pstr(b[f])))
					}
				}
				if len(diff) == 0 {
					continue
				}
				link := ""
				src := gs(b, "source")
				repo := ""
				if strings.HasPrefix(src, "github:") {
					repo = src[7:]
				} else if git, _, _, err := rawGitHubSource(src); err == nil {
					repo = strings.TrimSuffix(strings.TrimPrefix(git, "https://github.com/"), ".git")
				}
				if truthyT(a["commit"]) && truthyT(b["commit"]) && repo != "" {
					link = fmt.Sprintf(" (https://github.com/%s/compare/%s...%s)", repo, c12(gs(a, "commit")), c12(gs(b, "commit")))
				}
				lines = append(lines, fmt.Sprintf("~ %s %s: %s%s", ks.name, k, strings.Join(diff, "; "), link))
			}
		}
	}
	return lines
}

var _ = sort.Strings
