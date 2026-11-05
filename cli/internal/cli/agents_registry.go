package cli

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/agents/engine"
	"github.com/elocke/piste/cli/internal/agents/project"
	"github.com/elocke/piste/cli/internal/agents/registry"
	"github.com/elocke/piste/cli/internal/agents/safety"
	"github.com/elocke/piste/cli/internal/agents/zone"
)

// The registry: where it is read from, and the commands that
// build, pull and browse it.
//
// Resolution, first match wins:
//  1. --zone-root (or PISTE_AGENTS_ROOTS with --allow-env): those zone roots, live
//  2. --registry DIR: a built registry
//  3. --workdir: the zone checkouts the Nix-written manifest names, live
//  4. ~/.local/share/piste-agents/registry (Nix hosts: the piste.agents
//     derivation, or `registry pull` on a machine without Nix)
//  5. /etc/piste/agent-bundle/registry (devcontainers: the zone bundle)
//  6. the manifest's zone checkouts, as 3.

// SystemRegistry is the registry a devcontainer image carries.
const SystemRegistry = "/etc/piste/agent-bundle/registry"

// UserRegistry is the per-user registry path under home.
func UserRegistry(home string) string {
	return filepath.Join(home, ".local", "share", "piste-agents", "registry")
}

// agentsRegistryCmds adds the registry flags and commands to `piste agents`.
func (a *App) agentsRegistryCmds(cmd *cobra.Command, o *agentsOpts) {
	pf := cmd.PersistentFlags()
	pf.StringVar(&o.registryDir, "registry", "", "read this built registry (default: ~/.local/share/piste-agents/registry, then "+SystemRegistry+")")
	pf.BoolVar(&o.workdir, "workdir", false, "read the zone checkouts live instead of the built registry (authoring)")
	cmd.AddCommand(a.agentsSearch(o), a.agentsRegistry(o))
}

// survey opens the project root without a manifest (audit before init).
func (o *agentsOpts) survey() (*engine.Project, error) {
	root := o.root
	if root == "" {
		wd, _ := os.Getwd()
		r, err := project.Root(wd)
		if err != nil {
			return nil, err
		}
		root = r
	}
	reg, machine, err := o.registry()
	if err != nil {
		return nil, err
	}
	return engine.Survey(root, reg, machine)
}

// withRequires appends the registry ids the added items require (their
// frontmatter `requires`), transitively, unless already used or listed.
func withRequires(reg *registry.Registry, used func(string) bool, ids []string) []string {
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for i := 0; i < len(ids); i++ {
		it, ok := reg.Get(ids[i])
		if !ok {
			continue
		}
		for _, q := range reg.RequiredIDs(it) {
			if !seen[q] && !used(q) {
				seen[q] = true
				ids = append(ids, q)
			}
		}
	}
	return ids
}

// builtRegistry loads the registry of steps 2, 4 and 5; ok is false when
// none applies.
func (o *agentsOpts) builtRegistry() (reg *registry.Registry, ok bool, err error) {
	dir := o.registryDir
	if dir == "" {
		if o.workdir {
			return nil, false, nil
		}
		for _, d := range []string{UserRegistry(o.homeDir()), SystemRegistry} {
			if _, err := os.Stat(filepath.Join(d, registry.IndexFile)); err == nil {
				dir = d
				break
			}
		}
		if dir == "" {
			return nil, false, nil
		}
	}
	reg, err = registry.Load(dir)
	if err != nil {
		return nil, true, err
	}
	if o.zone != "" && o.zone != reg.Zone {
		return nil, true, fmt.Errorf("registry %s is for zone %s; this machine is %s", dir, reg.Zone, o.zone)
	}
	return reg, true, nil
}

// fetchedThirdParty maps locked third-party skill names to their fetched
// content: --third-party name=dir, else the store paths of the manifest's
// third-party links (what piste.agents deployed).
func (o *agentsOpts) fetchedThirdParty(links []manifestLink) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range o.thirdParty {
		name, dir, ok := strings.Cut(s, "=")
		if !ok || !filepath.IsAbs(dir) {
			return nil, fmt.Errorf("--third-party %q: want name=/absolute/dir", s)
		}
		out[name] = filepath.Clean(dir)
	}
	if len(o.thirdParty) > 0 {
		return out, nil
	}
	for _, l := range links {
		if l.Repo == "third-party" && l.Kind == "skills" && l.Store {
			out[l.Name] = l.Source
		}
	}
	return out, nil
}

// manifestLink is the part of a manifest link the registry reads.
type manifestLink struct {
	Kind, Name, Repo, Source string
	Store                    bool
}

// gitRevs records each live root's checkout commit (Meta's commit).
func gitRevs(reg *registry.Registry, roots []zone.Root, explicit []string) error {
	reg.Revs = map[string]string{}
	for _, s := range explicit {
		name, rev, ok := strings.Cut(s, "=")
		if !ok {
			return fmt.Errorf("--source-commit %q: want <zone-root-name>=<sha>", s)
		}
		reg.Revs[name] = rev
	}
	for _, r := range roots {
		if _, ok := reg.Revs[r.Name]; ok {
			continue
		}
		out, err := exec.Command("git", "-C", r.Dir, "rev-parse", "HEAD").Output()
		if err != nil {
			continue
		}
		rev := strings.TrimSpace(string(out))
		if st, err := exec.Command("git", "-C", r.Dir, "status", "--porcelain", "--", ".").Output(); err == nil && len(st) > 0 {
			rev += "-dirty"
		}
		reg.Revs[r.Name] = rev
	}
	return nil
}

func (a *App) agentsSearch(o *agentsOpts) *cobra.Command {
	var kind, harness, inZone string
	c := &cobra.Command{
		Use:   "search [<text>...]",
		Short: "Search the registry (every word must match the id or description)",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, _, err := o.registry()
			if err != nil {
				return fail(a.Stderr, err)
			}
			ms := reg.Search(strings.Join(args, " "), kind, harness, inZone)
			if o.asJSON {
				if ms == nil {
					ms = []registry.Meta{}
				}
				return printJSON(a.Stdout, ms)
			}
			for _, m := range ms {
				fmt.Fprintf(a.Stdout, "%s\t%s\n", safety.StripControl(m.ID), safety.StripControl(firstLine(m.Description)))
			}
			if len(ms) == 0 {
				fmt.Fprintln(a.Stderr, "piste agents: no match")
				return ExitError{1}
			}
			return nil
		},
	}
	c.Flags().StringVar(&kind, "kind", "", "only this kind (skill, 3p-skill, agent, instruction, hook, mcp, plugin, …)")
	c.Flags().StringVar(&harness, "harness", "", "only items this harness supports (claude, codex, hermes)")
	c.Flags().StringVar(&inZone, "in-zone", "", "only items of this zone (shared, personal, work)")
	c.Flags().BoolVar(&o.asJSON, "json", false, "output as JSON (the items' metadata)")
	return c
}

func printJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(b))
	return nil
}

// showMeta prints one item's metadata as text.
func showMeta(w io.Writer, m registry.Meta) {
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(w, "%-10s %s\n", k+":", safety.StripControl(v))
		}
	}
	row("id", m.ID)
	row("about", m.Description)
	trust := m.Trust
	if m.Trust == "third-party" {
		if m.AllowedBy != "" {
			trust += " (allowed by " + m.AllowedBy + ")"
		} else {
			trust += " (NOT allowed by the zone [policy])"
		}
	}
	row("trust", trust)
	row("harnesses", strings.Join(m.Harnesses, ", "))
	for _, h := range sortedKeys(m.Lossy) {
		row("lossy", h+": drops "+strings.Join(m.Lossy[h], ", "))
	}
	row("source", m.Source)
	row("version", m.Version)
	row("commit", m.Commit)
	row("sha256", m.ContentHash)
	row("requires", strings.Join(m.Requires, ", "))
	row("license", m.License)
	if names := safety.MixedScripts(m.Name); names != nil {
		fmt.Fprintf(w, "warning:   the name mixes scripts (%s)\n", strings.Join(names, ", "))
	}
}

func sortedKeys(m map[string][]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (a *App) agentsRegistry(o *agentsOpts) *cobra.Command {
	c := &cobra.Command{
		Use:    "registry",
		Hidden: true, // plumbing for the Nix modules
		Short:  "Build, pull or describe the registry",
	}
	c.AddCommand(a.registryBuild(o), a.registryPull(o), a.registryInfo(o))
	return c
}

func (a *App) registryBuild(o *agentsOpts) *cobra.Command {
	var out string
	var revs []string
	c := &cobra.Command{
		Use:   "build --out <dir>",
		Short: "Write the registry artifact (registry.json + content/) from the zone roots",
		Long: "Builds the registry of this machine's zone from explicit zone roots (--zone-root, or the\n" +
			"Nix-written manifest's checkouts), never from another built registry. Locked third-party\n" +
			"skills are included when their fetched content is given (--third-party name=dir, or the\n" +
			"manifest's store links). The Nix module and the zone bundles run this.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if out == "" {
				return fail(a.Stderr, errors.New("--out is required"))
			}
			o.workdir = true // never build from a built registry
			o.registryDir = ""
			o.rootRevs = revs
			reg, machine, err := o.registry()
			if err != nil {
				return fail(a.Stderr, err)
			}
			for _, w := range reg.Warnings {
				fmt.Fprintln(a.Stderr, "piste agents: warning: "+safety.StripControl(w))
			}
			abs, err := filepath.Abs(out)
			if err != nil {
				return fail(a.Stderr, err)
			}
			if err := reg.Write(machine, abs); err != nil {
				return fail(a.Stderr, err)
			}
			fmt.Fprintf(a.Stdout, "wrote %s (zone %s): %s\n", out, machine, reg.Summary())
			return nil
		},
	}
	c.Flags().StringVar(&out, "out", "", "output directory (replaced whole)")
	c.Flags().StringArrayVar(&o.thirdParty, "third-party", nil, "fetched content of a locked third-party skill: name=/dir (repeatable)")
	c.Flags().StringArrayVar(&revs, "source-commit", nil, "record a zone root's git commit: <name>=<sha> (repeatable; default git rev-parse)")
	c.Flags().StringVar(&o.zone, "zone", "", zoneFlagHelp)
	return c
}

func (a *App) registryInfo(o *agentsOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "info",
		Short: "Which registry this machine reads, its zone and its contents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, machine, err := o.registry()
			if err != nil {
				return fail(a.Stderr, err)
			}
			src := reg.Dir
			if src == "" {
				var parts []string
				for _, r := range reg.Roots {
					parts = append(parts, r.Name+"="+r.Zone+":"+r.Dir)
				}
				src = "live zone roots " + strings.Join(parts, ", ")
			}
			fmt.Fprintf(a.Stdout, "registry: %s\nzone:     %s\nitems:    %s\n", src, machine, reg.Summary())
			for _, w := range reg.Warnings {
				fmt.Fprintln(a.Stdout, "warning:  "+safety.StripControl(w))
			}
			return nil
		},
	}
	c.Flags().StringVar(&o.zone, "zone", "", zoneFlagHelp)
	return c
}

func (a *App) registryPull(o *agentsOpts) *cobra.Command {
	var to, want string
	c := &cobra.Command{
		Use:   "pull <dir|file.tar.gz|https://…>",
		Short: "Install a registry artifact (a zone bundle's registry/) for a machine without Nix",
		Long: "Fetches a built registry (a directory, a .tar.gz of one, or an https URL of the tarball),\n" +
			"verifies every payload against its index, and installs it at ~/.local/share/piste-agents/\n" +
			"registry (or --to). --sha256 pins the tarball. --zone refuses a registry of another zone.\n" +
			"A Nix-managed registry (a link into /nix/store) is never replaced.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				to = UserRegistry(o.homeDir())
			}
			if t, err := os.Readlink(to); err == nil && strings.HasPrefix(t, "/nix/store/") {
				return fail(a.Stderr, fmt.Errorf("%s is managed by Nix (-> %s); piste switch updates it", to, t))
			}
			// Staged next to the destination, so the final rename is atomic.
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return fail(a.Stderr, err)
			}
			stage, err := os.MkdirTemp(filepath.Dir(to), ".registry-pull-")
			if err != nil {
				return fail(a.Stderr, err)
			}
			defer os.RemoveAll(stage)
			dir, err := fetchRegistry(args[0], want, stage)
			if err != nil {
				return fail(a.Stderr, err)
			}
			reg, err := registry.Load(dir)
			if err != nil {
				return fail(a.Stderr, err)
			}
			if o.zone != "" && reg.Zone != o.zone {
				return fail(a.Stderr, fmt.Errorf("%s is a %s registry; this machine is %s (refused)", args[0], reg.Zone, o.zone))
			}
			if err := os.RemoveAll(to); err != nil {
				return fail(a.Stderr, err)
			}
			if err := os.Rename(dir, to); err != nil {
				return fail(a.Stderr, err)
			}
			fmt.Fprintf(a.Stdout, "installed %s (zone %s): %s\n", to, reg.Zone, reg.Summary())
			return nil
		},
	}
	c.Flags().StringVar(&to, "to", "", "install here (default ~/.local/share/piste-agents/registry)")
	c.Flags().StringVar(&want, "sha256", "", "expected sha256 of the tarball")
	c.Flags().StringVar(&o.zone, "zone", "", "refuse a registry of another zone")
	return c
}

// fetchRegistry stages src under stage and returns the registry directory.
func fetchRegistry(src, want, stage string) (string, error) {
	if fi, err := os.Stat(src); err == nil && fi.IsDir() {
		if want != "" {
			return "", errors.New("--sha256 applies to a tarball, not a directory")
		}
		dst := filepath.Join(stage, "registry")
		if err := copyDir(src, dst); err != nil {
			return "", err
		}
		return dst, nil
	}
	var rd io.Reader
	switch {
	case strings.HasPrefix(src, "https://"):
		cl := &http.Client{Timeout: 2 * time.Minute}
		resp, err := cl.Get(src)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%s: HTTP %d", src, resp.StatusCode)
		}
		rd = resp.Body
	case strings.HasPrefix(src, "http://"):
		return "", errors.New("refusing plain http; use https or a local file")
	default:
		f, err := os.Open(src)
		if err != nil {
			return "", err
		}
		defer f.Close()
		rd = f
	}
	h := sha256.New()
	tarball := filepath.Join(stage, "registry.tar.gz")
	f, err := os.Create(tarball)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(io.MultiWriter(f, h), rd); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	if got := hex.EncodeToString(h.Sum(nil)); want != "" && !strings.EqualFold(got, want) {
		return "", fmt.Errorf("sha256 %s != --sha256 %s", got, want)
	}
	out := filepath.Join(stage, "x")
	if err := untar(tarball, out); err != nil {
		return "", err
	}
	// The tarball holds registry.json at its top, or one directory that does.
	if _, err := os.Stat(filepath.Join(out, registry.IndexFile)); err == nil {
		return out, nil
	}
	es, _ := os.ReadDir(out)
	if len(es) == 1 && es[0].IsDir() {
		d := filepath.Join(out, es[0].Name())
		if _, err := os.Stat(filepath.Join(d, registry.IndexFile)); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("%s has no %s", src, registry.IndexFile)
}

// untar extracts regular files, directories and in-tree symlinks; anything
// else, or a path leaving out, is an error.
func untar(path, out string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(h.Name)
		if name == "." {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("tarball entry %q leaves the registry", h.Name)
		}
		dst := filepath.Join(out, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			w, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, io.LimitReader(tr, 256<<20)); err != nil {
				w.Close()
				return err
			}
			w.Close()
		case tar.TypeSymlink:
			t := filepath.Join(filepath.Dir(name), h.Linkname)
			if filepath.IsAbs(h.Linkname) || t == ".." || strings.HasPrefix(t, "../") {
				return fmt.Errorf("tarball symlink %q -> %q leaves the registry", h.Name, h.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, dst); err != nil {
				return err
			}
		default:
			return fmt.Errorf("tarball entry %q: unsupported type %c", h.Name, h.Typeflag)
		}
	}
}

// copyDir copies a registry directory (files, dirs, symlinks as links).
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		switch {
		case fi.IsDir():
			return os.MkdirAll(to, 0o755)
		case fi.Mode()&os.ModeSymlink != 0:
			t, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(t, to)
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if fi.Mode()&0o111 != 0 {
				mode = 0o755
			}
			return os.WriteFile(to, b, mode)
		}
	})
}
