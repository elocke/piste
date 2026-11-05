// Package overlay renders a new overlay from the engine's starter template
// (templates/default): `piste init` asks for the Answers, Render
// writes overlays/<name>/ with the template's @placeholders@ filled in.
package overlay

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/zone"
)

// Answers are what `piste init` asks for. Checkout is a Nix expression (see
// CheckoutExpr); Engine a flake ref (see EngineRef); Home the user's home
// directory on a Linux host.
type Answers struct {
	Name, User, Hostname, System, Zone, GitName, GitEmail, Engine, Checkout, Home string
	Roles                                                                         []string
	Sops                                                                          bool
	// Layer is the team layer's flake ref, for a template that is a layer's
	// starter; the engine's template doesn't use it.
	Layer string
	// Extras is init's "Start minimal or with everything?" answered
	// everything: the host's roles get the extras role (Extra) after the
	// others.
	Extras bool
}

// InitRoles are the roles init asks about (the engine's home-manager roles;
// container is for devcontainer images, not hosts).
var InitRoles = []string{"base", "dev", "desktop"}

// Roles a host can take: init's, and extras (Extra) beside them.
var Roles = []string{"base", "dev", "desktop", Extra}

// Extra is the opt-in extras role, the "everything" of init's "Start minimal
// or with everything?" (modules/extras).
const Extra = "extras"

// WithExtras is roles with Extra after the others when on, and without it
// when off.
func WithExtras(roles []string, on bool) []string {
	var out []string
	for _, r := range roles {
		if r != Extra {
			out = append(out, r)
		}
	}
	if on {
		out = append(out, Extra)
	}
	return out
}

// HasExtras: roles list the extras role.
func HasExtras(roles []string) bool { return has(roles, Extra) }

// roleImports: the roles a role's module imports (modules/roles).
var roleImports = map[string][]string{"dev": {"base"}, "desktop": {"dev", "base"}}

// darwinRoles: the roles a Mac has from its platform module, whatever its
// roles say.
var darwinRoles = []string{"base", "dev", "desktop"}

// SharedRoles is the roles a new host takes from its platform's other hosts
// (piste host add): hosts maps each host to the roles it has, the
// piste.roles.<role> its evaluated home declares (so a role its own modules
// import counts, and a Mac's platform roles too). The result is the roles
// every host has, written as init writes them: a role another one imports
// left out, and on a Mac the platform's. Not ok without hosts (the defaults
// apply).
func SharedRoles(darwin bool, hosts map[string][]string) ([]string, bool) {
	if len(hosts) == 0 {
		return nil, false
	}
	var shared map[string]bool
	for _, rs := range hosts {
		got := map[string]bool{}
		if darwin {
			rs = append(append([]string{}, rs...), darwinRoles...)
		}
		for _, r := range rs {
			if has(Roles, r) {
				got[r] = true
				for _, i := range roleImports[r] {
					got[i] = true
				}
			}
		}
		if shared == nil {
			shared = got
			continue
		}
		for r := range shared {
			if !got[r] {
				delete(shared, r)
			}
		}
	}
	out := []string{}
	for _, r := range Roles {
		if !shared[r] || (darwin && has(darwinRoles, r)) {
			continue
		}
		implied := false
		for o := range shared {
			implied = implied || has(roleImports[o], r)
		}
		if !implied {
			out = append(out, r)
		}
	}
	return out, true
}

// Systems a host can be. No x86_64-darwin: the pinned nixpkgs dropped Intel
// Macs.
var Systems = []string{"aarch64-darwin", "aarch64-linux", "x86_64-linux"}

// Darwin: a nix-darwin system (else a standalone home-manager home).
func Darwin(system string) bool { return strings.HasSuffix(system, "-darwin") }

// HostKey is the host's name in mkFlake's hosts: the hostname for a darwin
// system, user@hostname for a home.
func HostKey(a Answers) string {
	if Darwin(a.System) {
		return a.Hostname
	}
	return a.User + "@" + a.Hostname
}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	dirRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ValidName: an overlay's directory name in overlays/.
func ValidName(name string) error {
	if !dirRe.MatchString(name) {
		return fmt.Errorf("overlay name %q: letters, digits, dot, dash and underscore only", name)
	}
	return nil
}

// CheckUser, CheckHostname, CheckSystem, CheckRoles, CheckZone and CheckGit
// check one answer each, as it is given; their errors name no flag (a prompt
// asks again), Validate adds the flag.
func CheckUser(u string) error {
	if !nameRe.MatchString(u) {
		return fmt.Errorf("user %q: letters, digits, dot, dash and underscore only", u)
	}
	return nil
}

func CheckHostname(h, system string) error {
	switch {
	case !nameRe.MatchString(h):
		return fmt.Errorf("hostname %q: letters, digits, dot, dash and underscore only", h)
	case Darwin(system) && strings.Contains(h, "."):
		// darwin-rebuild indexes darwinConfigurations.<host> unquoted.
		return fmt.Errorf("hostname %q: no dots on macOS, darwin-rebuild can't take them (%s would do)", h, strings.SplitN(h, ".", 2)[0])
	}
	return nil
}

func CheckSystem(s string) error {
	if !has(Systems, s) {
		return fmt.Errorf("system %q: one of %s", s, strings.Join(Systems, ", "))
	}
	return nil
}

func CheckRoles(rs []string) error {
	for _, r := range rs {
		if !has(Roles, r) {
			return fmt.Errorf("role %q: one of %s", r, strings.Join(Roles, ", "))
		}
	}
	return nil
}

// CheckInitRoles: init's roles question. extras is its own question ("Start
// minimal or with everything?", --extras), so it isn't one of the roles here:
// named alone it would leave the host without the default dev.
func CheckInitRoles(rs []string) error {
	for _, r := range rs {
		if r == Extra {
			return errors.New(`role "extras": it is the question "Start minimal or with everything?"; give --extras (or answer everything) and the roles without it`)
		}
		if !has(InitRoles, r) {
			return fmt.Errorf("role %q: one of %s", r, strings.Join(InitRoles, ", "))
		}
	}
	return nil
}

func CheckZone(z string) error { return zone.ValidMachine(z) }

// CheckGit: what is "git name" or "git email".
func CheckGit(what, v string) error {
	if v == "" {
		return errors.New(what + " is empty")
	}
	return nil
}

// Validate refuses answers the template can't carry, naming the flag to fix.
func Validate(a Answers) error {
	if err := ValidName(a.Name); err != nil {
		return err
	}
	for _, c := range []struct {
		err  error
		flag string
	}{
		{CheckUser(a.User), "--user"},
		{CheckSystem(a.System), "--system"},
		{CheckHostname(a.Hostname, a.System), "--host"},
		{CheckGit("git name", a.GitName), "--git-name"},
		{CheckGit("git email", a.GitEmail), "--git-email"},
		{CheckZone(a.Zone), "--zone"},
		{CheckRoles(a.Roles), "--role"},
	} {
		if c.err != nil {
			return fmt.Errorf("%w (%s)", c.err, c.flag)
		}
	}
	return nil
}

// NixString escapes s for the inside of a Nix "string".
func NixString(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "${", `\${`).Replace(s)
}

// CheckoutExpr is the Nix expression for piste.checkout: relative to the
// home when the checkout is under it (the overlay then works for the same
// user on other machines), else the absolute path.
func CheckoutExpr(root, home string) string {
	if rel, err := filepath.Rel(home, root); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return `"${config.home.homeDirectory}/` + NixString(rel) + `"`
	}
	return `"` + NixString(root) + `"`
}

var (
	githubSCP   = regexp.MustCompile(`^git@github\.com:([^/]+)/(.+?)(\.git)?$`)
	githubHTTPS = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+?)(\.git)?/?$`)
	// git's scp-like [user@]host:path: a colon before any slash.
	scpLike = regexp.MustCompile(`^([^/\s:]+):(.+)$`)
)

// EngineRef is the flake ref an overlay pins the engine with: the checkout's
// origin at the release tag HEAD is on (tag), else at HEAD's commit (rev).
func EngineRef(origin, tag, rev string) (string, error) {
	if origin == "" {
		return "", errors.New("the checkout has no origin remote, so there is no engine to pin: pass --engine <flake ref>")
	}
	if m := githubHTTPS.FindStringSubmatch(origin); m != nil {
		at := tag
		if at == "" {
			at = rev
		}
		return "github:" + m[1] + "/" + m[2] + "/" + at, nil
	}
	url := origin
	if m := githubSCP.FindStringSubmatch(origin); m != nil {
		url = "ssh://git@github.com/" + m[1] + "/" + m[2] + ".git"
	} else if filepath.IsAbs(origin) {
		url = "file://" + origin
	} else if m := scpLike.FindStringSubmatch(origin); m != nil && !strings.Contains(origin, "://") {
		url = "ssh://" + m[1] + "/" + strings.TrimPrefix(m[2], "/")
	}
	if tag != "" {
		// Nix's git fetcher reads a bare ref as a branch (refs/heads/<ref>).
		return "git+" + url + "?ref=refs/tags/" + tag, nil
	}
	return "git+" + url + "?rev=" + rev, nil
}

var placeholder = regexp.MustCompile(`@([a-zA-Z]+)@`)

// junk: editor and Finder droppings a template directory can pick up.
func junk(name string) bool {
	return name == ".DS_Store" || strings.HasSuffix(name, ".swp") || strings.HasSuffix(name, ".swo") ||
		strings.HasSuffix(name, "~") || strings.HasPrefix(name, ".#") ||
		strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#")
}

func values(a Answers) map[string]string {
	rs := a.Roles
	if a.Extras {
		rs = WithExtras(rs, true)
	}
	roles := "[ ]"
	if len(rs) > 0 {
		q := make([]string, len(rs))
		for i, r := range rs {
			q[i] = `"` + r + `"`
		}
		roles = "[ " + strings.Join(q, " ") + " ]"
	}
	return map[string]string{
		"engine": NixString(a.Engine), "user": a.User, "hostname": a.Hostname, "hostKey": HostKey(a),
		"system": a.System, "roles": roles, "zone": a.Zone, "name": a.Name, "checkout": a.Checkout,
		"gitName": NixString(a.GitName), "gitEmail": NixString(a.GitEmail), "home": NixString(a.Home),
		"layer": NixString(a.Layer), "secrets": strconv.FormatBool(a.Sops),
	}
}

// fill replaces s's placeholders with vals; rel names the template file in
// the error for one it doesn't know.
func fill(rel, s string, vals map[string]string) (string, error) {
	var unknown error
	out := placeholder.ReplaceAllStringFunc(s, func(m string) string {
		v, ok := vals[m[1:len(m)-1]]
		if !ok && unknown == nil {
			unknown = fmt.Errorf("template %s: unknown placeholder %s", rel, m)
		}
		return v
	})
	return out, unknown
}

// hostTemplate is the template's host file for a system.
func hostTemplate(system string) string {
	if Darwin(system) {
		return "darwin.nix"
	}
	return "linux.nix"
}

// Render writes the template at tmpl to dst (which must not exist), filling
// in the placeholders; the template's hosts/{darwin,linux}.nix becomes
// hosts/<hostname>.nix for the answers' system. A placeholder it doesn't know
// is an error, and nothing is left behind.
func Render(tmpl, dst string, a Answers) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s exists", dst)
	}
	host := hostTemplate(a.System)
	vals := values(a)
	type file struct {
		rel  string
		body []byte
	}
	var files []file
	err := filepath.WalkDir(tmpl, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || junk(d.Name()) {
			return err
		}
		rel, err := filepath.Rel(tmpl, p)
		if err != nil {
			return err
		}
		if dir, base := filepath.Split(rel); dir == "hosts"+string(filepath.Separator) {
			if base != host {
				return nil
			}
			rel = filepath.Join("hosts", a.Hostname+".nix")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out, err := fill(rel, string(b), vals)
		if err != nil {
			return err
		}
		files = append(files, file{rel, []byte(out)})
		return nil
	})
	if err != nil {
		return err
	}
	for _, f := range files {
		p := filepath.Join(dst, f.rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, f.body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// RenderHost renders one host from the template at tmpl, as Render would
// for these answers: its host file (the template's hosts/{linux,darwin}.nix
// for a.System), and its entry in flake.nix's hosts: the template flake.nix's
// lines from `hosts."@hostKey@" = {` through the `};` at that line's
// indentation, with that indentation removed (piste host add).
func RenderHost(tmpl string, a Answers) (hostFile []byte, entry string, err error) {
	vals := values(a)
	rel := filepath.Join("hosts", hostTemplate(a.System))
	b, err := os.ReadFile(filepath.Join(tmpl, rel))
	if err != nil {
		return nil, "", err
	}
	host, err := fill(rel, string(b), vals)
	if err != nil {
		return nil, "", err
	}
	b, err = os.ReadFile(filepath.Join(tmpl, "flake.nix"))
	if err != nil {
		return nil, "", err
	}
	var lines []string
	indent := ""
	for _, l := range strings.SplitAfter(string(b), "\n") {
		trimmed := strings.TrimLeft(l, " \t")
		switch {
		case lines == nil && strings.HasPrefix(trimmed, `hosts."@hostKey@" = {`):
			indent = l[:len(l)-len(trimmed)]
		case lines == nil:
			continue
		}
		lines = append(lines, strings.TrimPrefix(l, indent))
		if len(lines) > 1 && strings.TrimRight(l, "\r\n") == indent+"};" {
			out, err := fill("flake.nix", strings.Join(lines, ""), vals)
			if err != nil {
				return nil, "", err
			}
			return []byte(host), out, nil
		}
	}
	return nil, "", fmt.Errorf("template flake.nix: no hosts.\"@hostKey@\" = { … }; entry to render a host from")
}

var (
	errNoHostsLine = errors.New("flake.nix has no hosts line")
	hostsLine      = regexp.MustCompile(`^([ \t]*)hosts(\.|\s*=)`)
	flakeUser      = regexp.MustCompile(`(?m)^\s*user\s*=\s*"([^"\\]*)"\s*;`)
)

// AddHostEntry inserts entry (as RenderHost gives it) into a flake.nix,
// before its first hosts line (hosts."<key>" = … or hosts = …), at that
// line's indentation, with an empty line after it. Nix merges a dotted entry
// with either. No hosts line is errNoHostsLine, and flake back as it was.
func AddHostEntry(flake []byte, entry string) ([]byte, error) {
	lines := strings.SplitAfter(string(flake), "\n")
	for i, l := range lines {
		m := hostsLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		var b strings.Builder
		for _, e := range strings.SplitAfter(strings.TrimSuffix(entry, "\n"), "\n") {
			if strings.TrimSpace(e) != "" {
				b.WriteString(m[1])
			}
			b.WriteString(strings.TrimRight(e, "\n") + "\n")
		}
		b.WriteString("\n")
		return []byte(strings.Join(lines[:i], "") + b.String() + strings.Join(lines[i:], "")), nil
	}
	return flake, errNoHostsLine
}

// flakeExtras is a roles list in flake.nix naming extras.
var flakeExtras = regexp.MustCompile(`(?m)^\s*roles\s*=\s*\[[^\]]*"extras"`)

// FlakeHasExtras: one of flake.nix's hosts takes the extras role.
func FlakeHasExtras(flake []byte) bool { return flakeExtras.Match(flake) }

// FlakeUser is the overlay's user, from its flake.nix's `user = "…";`.
func FlakeUser(flake []byte) (string, bool) {
	if m := flakeUser.FindSubmatch(flake); m != nil {
		return string(m[1]), true
	}
	return "", false
}
