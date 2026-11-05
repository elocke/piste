package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// The command reference: docs/commands.md is piste's own help,
// rendered here and compared with the committed page. PISTE_UPDATE_DOCS=1
// rewrites the page after a command changes.

func noop(*cobra.Command, []string) {}

// A tree like piste's: a global flag, a command with a subcommand and a flag
// of its own for both, and the commands the reference leaves out.
func TestCommandsDocRendering(t *testing.T) {
	root := &cobra.Command{Use: "piste", Long: "piste: the root."}
	root.PersistentFlags().Bool("glob", false, "a global flag")
	a := &cobra.Command{Use: "a", Short: "does <a>", Long: "line one\nsee <name>", Run: noop}
	a.Flags().Bool("fa", false, "a's own flag")
	a.PersistentFlags().String("pa", "", "a flag of a and its subcommands")
	sub := &cobra.Command{Use: "sub", Short: "does sub", Run: noop}
	sub.AddCommand(&cobra.Command{Use: "deep", Short: "does deep", Run: noop})
	a.AddCommand(sub)
	root.AddCommand(a,
		&cobra.Command{Use: "h", Short: "hidden", Hidden: true, Run: noop},
		&cobra.Command{Use: "help", Short: "help", Run: noop},
		&cobra.Command{Use: "completion", Short: "completion", Run: noop})

	doc := commandsDoc(root)
	for _, want := range []string{
		"- [piste a](#piste-a): does \\<a\\>\n",
		"## piste a\n",
		"`piste a [flags]`",
		"does \\<a\\>",
		"```text\nline one\nsee <name>\n```",
		"--fa",
		"### piste a sub\n",
		"Also takes the flags of `piste a`.",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the page lacks %q:\n%s", want, doc)
		}
	}
	// sub and deep both take a's --pa.
	if n := strings.Count(doc, "Also takes the flags of `piste a`."); n != 2 {
		t.Errorf("a's flags are named under %d subcommands, want 2 (sub and sub deep):\n%s", n, doc)
	}
	for flag, n := range map[string]int{"--glob": 1, "--pa": 1} {
		if got := strings.Count(doc, flag); got != n {
			t.Errorf("%s appears %d times, want %d:\n%s", flag, got, n, doc)
		}
	}
	for _, not := range []string{"piste h", "## piste help", "## piste completion", "[piste completion]"} {
		if strings.Contains(doc, not) {
			t.Errorf("the page has %q:\n%s", not, doc)
		}
	}
}

// A plugin on the PATH of whoever regenerates the page (piste-<name>) isn't
// a piste command: the page is the same on every machine.
func TestCommandsDocIgnoresPlugins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "piste-zzz"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	withPATH := (&App{Env: Env{Getenv: os.Getenv}}).newRoot()
	if !strings.Contains(commandsDoc(withPATH), "piste zzz") {
		t.Fatal("control: piste-zzz on PATH isn't a command of the root")
	}
	if strings.Contains(commandsDoc(docsRoot()), "zzz") {
		t.Fatal("docs/commands.md would list the plugin piste-zzz from PATH")
	}
}

func TestCommandsDoc(t *testing.T) {
	path, ok := repoFile(t, "docs/commands.md")
	if !ok {
		t.Skip("no docs/ here (the Nix build's source is cli/ only)")
	}
	const fix = "PISTE_UPDATE_DOCS=1 go test ./internal/cli -run TestCommandsDoc (in cli/)"
	got := commandsDoc(docsRoot())
	if os.Getenv("PISTE_UPDATE_DOCS") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("docs/commands.md is missing: run %s", fix)
	} else if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("docs/commands.md is stale (%s): run %s", firstDiff(string(want), got), fix)
	}
}

// A flag's usage has no backticks: pflag reads `x` in it as the name of the
// flag's value, so --help and docs/commands.md would print
// "--no-mcp piste mcp sync" for a bool flag.
func TestFlagUsagesHaveNoBackticks(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, fs := range []*pflag.FlagSet{c.LocalFlags(), c.PersistentFlags()} {
			fs.VisitAll(func(f *pflag.Flag) {
				if strings.Contains(f.Usage, "`") {
					name, _ := pflag.UnquoteUsage(f)
					t.Errorf("%s --%s: its usage %q has backticks, which pflag prints as the value's name (%q)", c.CommandPath(), f.Name, f.Usage, name)
				}
			})
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(docsRoot())
}

// docsRoot is piste's command tree as docs/commands.md shows it: built with
// an empty environment, so no plugin from anyone's PATH joins it.
func docsRoot() *cobra.Command {
	a := &App{Stdout: io.Discard, Stderr: io.Discard, Env: Env{Getenv: func(string) string { return "" }}}
	return a.newRoot()
}

// repoFile is rel in the engine repo around this package, and whether the
// repo's docs/ is there (it isn't in the Nix build, whose source is cli/).
func repoFile(t *testing.T, rel string) (string, bool) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(root, "docs"))
	return filepath.Join(root, rel), err == nil && fi.IsDir()
}

// firstDiff names the first line where two texts differ.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("line %d: the page has %q, piste's help %q", i+1, wl, gl)
		}
	}
	return "same lines"
}

// commandsDoc renders root as docs/commands.md: an index of the top-level
// commands, the global flags once, then every command and subcommand with
// its use line, Short, Long (fenced: it holds <name>, which GitHub would
// drop as HTML) and its own flags. Hidden commands, help and completion are
// left out.
func commandsDoc(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString("# Command reference\n\n" +
		"- What can piste do, and which command does it?\n" +
		"- Which arguments and flags does a command take?\n\n" +
		"Generated from piste's own help by `cli/internal/cli/docs_test.go`; don't\n" +
		"edit it by hand. After changing a command, run this in `cli/`:\n" +
		"`PISTE_UPDATE_DOCS=1 go test ./internal/cli -run TestCommandsDoc`.\n" +
		"Plugins (`piste-<name>` on your PATH, see [customising.md](customising.md))\n" +
		"aren't listed: `piste --help` shows yours.\n\n")
	for _, c := range docCommands(root) {
		fmt.Fprintf(&b, "- [%s](#%s): %s\n", c.CommandPath(), strings.ReplaceAll(c.CommandPath(), " ", "-"), mdText(c.Short))
	}
	b.WriteString("\n## Global flags\n\n")
	if root.Long != "" {
		fence(&b, root.Long)
	}
	b.WriteString("These go with every command:\n\n")
	fence(&b, root.PersistentFlags().FlagUsages())
	b.WriteString("`piste --version` prints piste's version. `piste completion bash|zsh|fish`\n" +
		"prints a shell's completion script (piste's package installs them).\n\n")
	var walk func(c *cobra.Command, depth int)
	walk = func(c *cobra.Command, depth int) {
		fmt.Fprintf(&b, "%s %s\n\n`%s`", strings.Repeat("#", depth+1), c.CommandPath(), c.UseLine())
		if c.Short != "" {
			fmt.Fprintf(&b, ": %s", mdText(c.Short))
		}
		b.WriteString("\n\n")
		if c.Long != "" {
			fence(&b, c.Long)
		}
		if f := c.LocalFlags().FlagUsages(); f != "" {
			b.WriteString("Flags:\n\n")
			fence(&b, f)
		}
		var from []string
		for p := c.Parent(); p != nil && p != root; p = p.Parent() {
			if p.PersistentFlags().HasAvailableFlags() {
				from = append(from, "`"+p.CommandPath()+"`")
			}
		}
		if len(from) > 0 {
			fmt.Fprintf(&b, "Also takes the flags of %s.\n\n", strings.Join(from, " and "))
		}
		for _, s := range docCommands(c) {
			walk(s, depth+1)
		}
	}
	for _, c := range docCommands(root) {
		walk(c, 1)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// docCommands are c's subcommands the reference shows, in cobra's order.
func docCommands(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, s := range c.Commands() {
		if s.Hidden || s.Deprecated != "" || s.Name() == "help" || s.Name() == "completion" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// mdText escapes what Markdown would read as HTML in a Short.
func mdText(s string) string {
	return strings.NewReplacer("<", `\<`, ">", `\>`).Replace(s)
}

func fence(b *strings.Builder, s string) {
	fmt.Fprintf(b, "```text\n%s\n```\n\n", strings.TrimRight(s, "\n"))
}

// The public docs: README.md, LICENSE and the pages directly in
// docs/. A stranger has nobody to ask, so a dead link, a link into
// the planning documents or a page that doesn't say what it answers is a bug.
func TestPublicDocs(t *testing.T) {
	root, ok := repoFile(t, "")
	if !ok {
		t.Skip("no docs/ here (the Nix build's source is cli/ only)")
	}
	pages, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range append([]string{filepath.Join(root, "README.md")}, pages...) {
		rel, _ := filepath.Rel(root, page)
		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		// Generated pages too: their text is piste's help and the options'
		// descriptions.
		for i, line := range strings.Split(string(b), "\n") {
			if m := privateRefRE.FindString(line); m != "" {
				t.Errorf("%s:%d cites a planning document (%q), which a stranger can't read: %s", rel, i+1, m, strings.TrimSpace(line))
			}
		}
		for _, target := range mdLinks(string(b)) {
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			p := filepath.Join(filepath.Dir(page), strings.SplitN(target, "#", 2)[0])
			to, _ := filepath.Rel(root, p)
			if strings.HasPrefix(to, filepath.Join("docs", "plans")) || strings.HasPrefix(to, filepath.Join("docs", "superpowers")) {
				t.Errorf("%s links %s, which isn't published", rel, target)
			} else if _, err := os.Stat(p); err != nil {
				t.Errorf("%s links %s, which doesn't exist", rel, target)
			}
		}
		if filepath.Dir(rel) == "docs" {
			if title, next := openingLines(string(b)); !strings.HasPrefix(title, "# ") || !strings.HasPrefix(next, "- ") || !strings.HasSuffix(next, "?") {
				t.Errorf("%s doesn't open with a title and the questions it answers (- …?): %q, %q", rel, title, next)
			}
		}
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		t.Errorf("no LICENSE: %v", err)
	}
	for _, want := range []string{"BSD 3-Clause License", "Copyright (c) 2021, Dustin Lyons", "Copyright (c) 2026, elocke", "Neither the name of the copyright holder"} {
		if !strings.Contains(string(license), want) {
			t.Errorf("LICENSE lacks %q", want)
		}
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	links := mdLinks(string(readme))
	for _, want := range []string{"docs/concepts.md", "docs/customising.md", "docs/upgrading.md", "docs/commands.md", "docs/options.md", "LICENSE"} {
		if !slices.Contains(links, want) {
			t.Errorf("README.md doesn't link %s", want)
		}
	}
}

func TestMdLinks(t *testing.T) {
	md := "See [a](a.md) and ![img](i/x.png \"title\"), [b](b.md#part).\n" +
		"Not `[c](c.md)`, nor\n```\n[d](d.md)\n```\n[e](https://example.com) [f](#here)\n"
	got := mdLinks(md)
	want := []string{"a.md", "i/x.png", "b.md#part", "https://example.com", "#here"}
	if !slices.Equal(got, want) {
		t.Fatalf("mdLinks = %q, want %q", got, want)
	}
}

var (
	mdLinkRE = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	mdCodeRE = regexp.MustCompile("`[^`]*`")
	// privateRefRE: a citation of a planning document: a section sign, a
	// decision, phase or wave tag, "plan 4a".
	privateRefRE = regexp.MustCompile(`§|\b[DFPW]\d+\b|\b(?:plan|spec) \d`)
)

// handPages are README.md and the docs/ pages written by hand (not the
// generated commands.md and options.md), relative to root.
func handPages(t *testing.T, root string) []string {
	t.Helper()
	pages, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	out := []string{"README.md"}
	for _, p := range pages {
		if b := filepath.Base(p); b != "commands.md" && b != "options.md" {
			out = append(out, filepath.Join("docs", b))
		}
	}
	return out
}

func readRepo(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// roleTogglesThatWork are the piste.roles.<role>.<part>.enable a hand page
// may teach: each is read in its role, and checks.role-toggles evaluates the
// starters with it off and finds its program off. A page teaching any other
// sends a stranger to a setting that does nothing. Wire a toggle in
// modules/roles/, add it to that check and here.
var roleTogglesThatWork = []string{
	"base.shell", "base.git", "base.ssh", "base.starship",
	"dev.editor", "dev.tmux",
	"desktop.alacritty", "desktop.ghostty",
}

// extrasPartsThatWork are the extras role's parts (modules/extras/<part>.nix,
// each under lib.mkIf cfg.enable; checks.role-toggles turns each off): a hand
// page may teach piste.roles.extras.<part>.enable for these.
var extrasPartsThatWork = []string{"shell", "editor", "tmux", "ghostty", "casks", "macos"}

var roleToggleRE = regexp.MustCompile(`piste\.roles\.([a-z]+)\.([a-z]+)\.enable`)

func TestPublicDocsRoleToggles(t *testing.T) {
	root, ok := repoFile(t, "")
	if !ok {
		t.Skip("no docs/ here (the Nix build's source is cli/ only)")
	}
	// The project skills too: an agent at the checkout follows them.
	skills, err := filepath.Glob(filepath.Join(root, ".agents", "skills", "*", "SKILL.md"))
	if err != nil || len(skills) == 0 {
		t.Fatalf("no project skills (.agents/skills/*/SKILL.md): %v", err)
	}
	pages := handPages(t, root)
	for _, s := range skills {
		rel, _ := filepath.Rel(root, s)
		pages = append(pages, rel)
	}
	for _, rel := range pages {
		for _, m := range roleToggleRE.FindAllStringSubmatch(readRepo(t, root, rel), -1) {
			if !slices.Contains(roleTogglesThatWork, m[1]+"."+m[2]) && !(m[1] == "extras" && slices.Contains(extrasPartsThatWork, m[2])) {
				t.Errorf("%s teaches %s, which changes nothing (only %v do): show lib.mkForce instead", rel, m[0], roleTogglesThatWork)
			}
		}
	}
	// The list can't outlive the wiring: each one is read in its role.
	for _, tg := range roleTogglesThatWork {
		role, part, _ := strings.Cut(tg, ".")
		src := readRepo(t, root, filepath.Join("modules", "roles", role+".nix"))
		if !strings.Contains(src, "cfg."+part+".enable") {
			t.Errorf("modules/roles/%s.nix no longer reads cfg.%s.enable: drop %s from roleTogglesThatWork", role, part, tg)
		}
	}
	for _, part := range extrasPartsThatWork {
		src := readRepo(t, root, filepath.Join("modules", "extras", part+".nix"))
		if !strings.Contains(src, "options.piste.roles.extras."+part+".enable") || !strings.Contains(src, "lib.mkIf cfg.enable") {
			t.Errorf("modules/extras/%s.nix no longer declares and reads its toggle: drop %s from extrasPartsThatWork", part, part)
		}
	}
}

// docs/devcontainer.md's list of what the image leaves out is the container
// role's piste.packages.exclude, name for name.
func TestDevcontainerDocExcludes(t *testing.T) {
	root, ok := repoFile(t, "")
	if !ok {
		t.Skip("no docs/ here (the Nix build's source is cli/ only)")
	}
	block := regexp.MustCompile(`(?s)\bexclude = \[(.*?)\];`).FindStringSubmatch(readRepo(t, root, "modules/roles/container.nix"))
	if block == nil {
		t.Fatal("modules/roles/container.nix has no `exclude = [ … ];`")
	}
	var want []string
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(block[1], -1) {
		want = append(want, m[1])
	}
	list := regexp.MustCompile(`Left out of the package set\*\*[^:]*:([^.]*)\.`).FindStringSubmatch(readRepo(t, root, "docs/devcontainer.md"))
	if list == nil {
		t.Fatal(`docs/devcontainer.md has no "**Left out of the package set** …: <names>."`)
	}
	var got []string
	for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(list[1], -1) {
		got = append(got, m[1])
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("docs/devcontainer.md lists %q as left out; container.nix's exclude is %q", got, want)
	}
}

// options.md's default for resolved.homePackages names the categories it
// collects (resolvedPkgs) and no other: fonts and darwinOnly go elsewhere.
func TestOptionsDocHomePackages(t *testing.T) {
	root, ok := repoFile(t, "")
	if !ok {
		t.Skip("no docs/ here (the Nix build's source is cli/ only)")
	}
	src := readRepo(t, root, "modules/packages-module.nix")
	var all, in []string
	for _, m := range regexp.MustCompile(`(?m)^\s*(\w+)\.enable = mkCategoryEnable`).FindAllStringSubmatch(src, -1) {
		all = append(all, m[1])
	}
	resolved := regexp.MustCompile(`(?s)resolvedPkgs =(.*?)\+\+ cfg\.extraPackages;`).FindStringSubmatch(src)
	text := regexp.MustCompile(`(?s)homePackages = mkOption \{.*?defaultText = lib\.literalMD "([^"]*)"`).FindStringSubmatch(src)
	if len(all) == 0 || resolved == nil || text == nil {
		t.Fatalf("modules/packages-module.nix: categories %d, resolvedPkgs found %v, homePackages defaultText found %v", len(all), resolved != nil, text != nil)
	}
	for _, m := range regexp.MustCompile(`collectIf cfg\.(\w+)\.enable`).FindAllStringSubmatch(resolved[1], -1) {
		in = append(in, m[1])
	}
	words := regexp.MustCompile(`\w+`).FindAllString(text[1], -1)
	for _, c := range all {
		if collected, named := slices.Contains(in, c), slices.Contains(words, c); collected != named {
			t.Errorf("resolved.homePackages' defaultText %q: category %s collected %v, named %v", text[1], c, collected, named)
		}
	}
}

// What the reviews of the hand pages, the project skills and the changelog
// corrected: each entry is a claim a page (or one of its sections) got wrong
// and the fact it states instead (whitespace folded, so a wrapped line
// matches); "" for either is no check.
func TestPublicDocsFacts(t *testing.T) {
	root, ok := repoFile(t, "")
	if !ok {
		t.Skip("no docs/ here (the Nix build's source is cli/ only)")
	}
	ws := regexp.MustCompile(`\s+`)
	for _, f := range []struct{ page, section, wrong, right, why string }{
		{"docs/upgrading.md", "## Several machines",
			"`piste update` (which moves that machine's checkout",
			"git -C ~/piste checkout v",
			"piste update on another machine moves every overlay input, a layer too, and to the newest release: past the lock the first machine committed"},
		{"docs/concepts.md", "",
			"Without a checkout, the pin wins",
			"Inside a checkout, every build is against the checkout",
			"the build-uses-checkout item: piste build, check and dry build the checkout's engine, as switch does"},
		{"docs/agents-and-mcp.md", "## Searching Nix from an agent",
			"It's a per-machine server, so its tool names are unprefixed",
			`name = "nixos"`,
			"the mcp__nixos__… names follow from the server's name in [[mcp]]"},

		// piste fix's session reads the piste-fix skill.
		{".agents/skills/piste-fix/SKILL.md", "",
			"(its hosts, e.g. `jane-mac`, `\"jane@devbox\"`) and `hosts/<host>.nix`.",
			"`home.nix` (what's theirs on every machine)",
			"an overlay keeps what's on every machine in home.nix"},
		{".agents/skills/piste-fix/SKILL.md", "## Never", "", "`piste update`",
			"piste update moves the checkout to another release and rewrites every overlay's flake.nix and flake.lock"},
		{".agents/skills/piste-fix/SKILL.md", "## Never", "", "`piste host add`",
			"piste host add makes an age key, edits .sops.yaml and stages files"},
		{".agents/skills/piste-fix/SKILL.md", "## 1. Get the error",
			"undid itself** after it broke the shell left its output in",
			"The new generation fails the check:",
			"after an undo at a terminal, over ssh or on a Mac, the reason is only on the person's screen"},
		{".agents/skills/piste-fix/SKILL.md", "## 1. Get the error", "",
			"Only a switch run from a systemd user service",
			"switch.log is only a detached switch's (Linux, inside a user service), and the next one empties it"},
		{".agents/skills/piste-fix/SKILL.md", "## Common causes",
			"`$XDG_STATE_HOME/piste/switch.log`; `piste switch --no-undo` for the person",
			"The new generation fails the check:",
			"switch.log is only a detached switch's"},
		{".agents/skills/piste-fix/SKILL.md", "## 4. Check it, and hand it back", "",
			"only a switch runs that check",
			"piste fix only builds: after an undo it passes before any edit"},
		{".agents/skills/piste-config/SKILL.md", "",
			"| `home.packages = [ pkgs.<attr> ];` |",
			"Add it to the list that's already there",
			"the starter's files bind these lists already, and a second binding is an eval error"},
		{".agents/skills/piste-config/SKILL.md", "", "", "`home.nix`",
			"what's on every machine goes in home.nix"},
		{".agents/skills/piste-config/SKILL.md", "",
			"If it's the engine's `modules/programs/<x>.nix`, use `programs.<x>.enable = lib.mkForce false;`",
			"`piste.roles.<role>.<toggle>.enable = false;`",
			"a role's program has a toggle (docs/customising.md); mkForce is for the ones without"},
		{".agents/skills/piste-config/SKILL.md", "",
			"the fix is `programs.tmux.enable = lib.mkForce false;`",
			"`piste.roles.dev.tmux.enable = false;`",
			"tmux has dev's toggle"},
		{".agents/skills/piste-config/SKILL.md", "", "", "`piste update`",
			"the agent never moves the checkout or the overlays' pins"},
		{".agents/skills/piste-config/SKILL.md", "", "", "`piste host add`",
			"the agent never makes an age key or edits .sops.yaml"},

		// The launch docs (the docs sweep): what a stranger needs and the
		// promises piste makes.
		{"docs/upgrading.md", "", "updated weekly", "## What a release promises",
			"the compat promise: what a release keeps working, and what it doesn't (the lock pipeline isn't proven, so no \"weekly\")"},
		{"docs/upgrading.md", "## What a release promises", "", "team layers, which are a preview",
			"team layers are outside the promise in v0.1.0"},
		{"docs/upgrading.md", "", "", "homebrew.onActivation.upgrade",
			"a Mac's casks aren't upgraded on a switch, and the page says how to"},
		{"docs/concepts.md", "", "never reach your work laptop", "isn't a control over what an agent does",
			"the zone limit is agent content at build time only; MDM is the real control"},
		{"docs/customising.md", "## Drop a default", "", "brew uninstall --cask",
			"an excluded cask stays installed until it is uninstalled by hand"},
		{"docs/customising.md", "## Secrets", "", "readable by every user",
			"a secret's value never goes in a .nix file: the store is world-readable"},
		{"README.md", "", "", "isn't a nixpkgs overlay",
			"overlay here is the person's repo, not a nixpkgs overlay"},
		{"README.md", "", "", "downloads a few GB",
			"the first switch is big, and the reader should know"},
		{"README.md", "## Your first change", "", "piste switch",
			"the README shows a first change end to end"},

		// The changelog: the notes of the first public release, which a
		// stranger reads without the private history before it.
		{"CHANGELOG.md", "",
			"runs it on every push and blocks the push on a hit",
			"git config core.hooksPath .githooks",
			"nothing turns the hook on, and it needs a marker list"},
		{"CHANGELOG.md", "", "Engine inputs such as nixpkgs now move only with releases", "",
			"a first release has no before"},
		{"CHANGELOG.md", "", "replace `nix-add`, `nix-fix` and `nix-option`", "",
			"those skills were never public"},
		{"CHANGELOG.md", "", "`--fix` is gone", "",
			"drift's --fix was never public"},
		{"CHANGELOG.md", "", "before, only `desktop.zellij` did anything", "",
			"a first release has no before"},
		{"CHANGELOG.md", "", "it no longer reports the engine's own casks", "",
			"a first release has no before"},
		{"CHANGELOG.md", "", "The Mac starter gains", "",
			"a first release has no before"},
		{"CHANGELOG.md", "", "once the repo is public, on macOS", "",
			"the release is public"},
	} {
		text := readRepo(t, root, f.page)
		if f.section != "" {
			_, after, found := strings.Cut(text, "\n"+f.section+"\n")
			if !found {
				t.Errorf("%s has no %q", f.page, f.section)
				continue
			}
			text, _, _ = strings.Cut(after, "\n## ")
		}
		text = ws.ReplaceAllString(text, " ")
		where := strings.TrimSpace(f.page + " " + f.section)
		if f.wrong != "" && strings.Contains(text, f.wrong) {
			t.Errorf("%s still says %q (%s)", where, f.wrong, f.why)
		}
		if f.right != "" && !strings.Contains(text, f.right) {
			t.Errorf("%s doesn't say %q (%s)", where, f.right, f.why)
		}
	}
}

// mdLinks are the targets of md's inline links and images, outside code.
func mdLinks(md string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range mdLinkRE.FindAllStringSubmatch(mdCodeRE.ReplaceAllString(line, ""), -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// openingLines are md's first two non-blank lines.
func openingLines(md string) (string, string) {
	var got []string
	for _, line := range strings.Split(md, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if got = append(got, line); len(got) == 2 {
				return got[0], got[1]
			}
		}
	}
	got = append(got, "", "")
	return got[0], got[1]
}

// fang's help renderer title-cases the first word of every command and flag
// description ("Garbage-collect" prints as "Garbage-Collect", "aarch64-darwin"
// as "Aarch64-Darwin"), and gives its Styles to no one to change. So the first
// word of each visible Short and flag usage is written the way the renderer
// leaves it; this fails on one it would mangle. (An error's text is capitalised
// by App.Main's handler instead of title-cased.)
func TestHelpFirstWordsSurviveTitleCasing(t *testing.T) {
	first := func(s string) string {
		s = strings.TrimLeft(s, " \t")
		w, _, _ := strings.Cut(s, " ")
		return w
	}
	check := func(where, s string) {
		w, _, _ := strings.Cut(first(s), "\n")
		// Capitalising the first letter is wanted; the rest of the word is not
		// to change.
		want := capitalizeFirst(w)
		if got := cases.Title(language.AmericanEnglish).String(w); got != want {
			t.Errorf("%s: %q would print as %q in --help: start it with another word", where, s, got)
		}
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden {
			return
		}
		if c.Short != "" {
			check(c.CommandPath()+" (Short)", c.Short)
		}
		for _, fs := range []*pflag.FlagSet{c.LocalFlags(), c.PersistentFlags()} {
			fs.VisitAll(func(f *pflag.Flag) {
				if f.Usage != "" && !f.Hidden {
					check(c.CommandPath()+" --"+f.Name, f.Usage)
				}
			})
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(docsRoot())
}

func TestCapitalizeFirst(t *testing.T) {
	for in, want := range map[string]string{
		"--host takes a hostname": "--host takes a hostname",
		"flake check failed":      "Flake check failed",
		"éa-b":                    "Éa-b",
		"":                        "",
	} {
		if got := capitalizeFirst(in); got != want {
			t.Errorf("capitalizeFirst(%q) = %q, want %q", in, got, want)
		}
	}
}
