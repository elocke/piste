package overlay

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// repoPath finds a path of the engine repo: $env (set by the Nix build, whose
// sandbox has only cli/), else relative to this package in a checkout.
func repoPath(t *testing.T, env, rel string) string {
	t.Helper()
	if p := os.Getenv(env); p != "" {
		return p
	}
	p, err := filepath.Abs(filepath.Join("..", "..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("%s: set %s (%v)", rel, env, err)
	}
	return p
}

// The answers tests/fixtures/template/{linux,darwin,nixos} were rendered with.
func instanceAnswers(kind string) Answers {
	a := Answers{
		Name: "personal", User: "jane", Zone: "personal", Engine: "github:elocke/piste/v0.0.0",
		Checkout: `"${config.home.homeDirectory}/piste"`, GitName: "Jane Doe", GitEmail: "jane@example.com",
		Hostname: "devbox", System: "x86_64-linux", Roles: []string{"dev"}, Home: "/home/jane",
	}
	switch kind {
	case "darwin":
		a.Hostname, a.System, a.Roles = "jane-mac", "aarch64-darwin", nil
	case "nixos":
		a.NixOS = true
		a.AuthorizedKeys = []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyOnlyForTests jane@example.com"}
	}
	return a
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func keys(m map[string]string) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// Render with the instances' answers is the committed instances, byte for
// byte (checks.template-instance evaluates those). PISTE_UPDATE_INSTANCES=1
// re-renders them after a template change.
func TestRenderInstances(t *testing.T) {
	inst := repoPath(t, "PISTE_TEMPLATE_INSTANCES", "tests/fixtures/template")
	engineTmpl := repoPath(t, "PISTE_TEMPLATE_DIR", "templates/default")
	// The fixture team layer's starter, rendered for jane in its
	// zone, with the layer as an input and her secrets on.
	layerTmpl := filepath.Join(repoPath(t, "PISTE_LAYER_DIR", "tests/fixtures/layer"), "template")
	// layer-linux-nosecrets: the same, with sops declined.
	// two-hosts: the linux instance with jane-mac added (piste host add).
	// extras-darwin and layer-linux-extras: init's "Start minimal or with
	// everything?" answered everything, on the engine's starter (a Mac) and
	// on the layer's (Linux).
	// nixos: a NixOS host (hosts/devbox/{default.nix,machine/configuration.nix}).
	for _, kind := range []string{"linux", "darwin", "layer-linux", "layer-darwin", "layer-linux-nosecrets", "two-hosts",
		"extras-darwin", "layer-linux-extras", "nixos"} {
		base := strings.TrimPrefix(strings.TrimPrefix(kind, "layer-"), "extras-")
		base = strings.TrimSuffix(strings.TrimSuffix(base, "-nosecrets"), "-extras")
		tmpl, ans := engineTmpl, instanceAnswers(base)
		ans.Extras = strings.Contains(kind, "extras")
		if strings.HasPrefix(kind, "layer-") {
			tmpl = layerTmpl
			ans.Name, ans.Zone, ans.Layer = "work", "work", "git+file:///layer"
			ans.Sops = !strings.HasSuffix(kind, "-nosecrets")
		}
		if kind == "two-hosts" {
			ans = instanceAnswers("linux")
		}
		dst := filepath.Join(t.TempDir(), "o")
		if err := Render(tmpl, dst, ans); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if kind == "two-hosts" {
			addHost(t, engineTmpl, dst, instanceAnswers("darwin"))
		}
		if os.Getenv("PISTE_UPDATE_INSTANCES") == "1" {
			if err := os.RemoveAll(filepath.Join(inst, kind)); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(dst, filepath.Join(inst, kind)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, want := tree(t, dst), tree(t, filepath.Join(inst, kind))
		if !reflect.DeepEqual(keys(got), keys(want)) {
			t.Fatalf("%s: files %v, want %v", kind, keys(got), keys(want))
		}
		for f := range want {
			if got[f] != want[f] {
				t.Errorf("%s: %s differs from the instance", kind, f)
			}
		}
	}
}

// The roles placeholder: the answers' roles, then extras when the person
// started with everything (once, whether or not --role named it too).
func TestRenderRolesExtras(t *testing.T) {
	for _, c := range []struct {
		roles  []string
		extras bool
		want   string
	}{
		{[]string{"dev"}, false, `[ "dev" ]`},
		{[]string{"dev"}, true, `[ "dev" "extras" ]`},
		{nil, false, `[ ]`},
		{nil, true, `[ "extras" ]`},
		{[]string{"extras", "dev"}, true, `[ "dev" "extras" ]`},
		{[]string{"dev", "extras"}, false, `[ "dev" "extras" ]`},
	} {
		got := values(Answers{Roles: c.roles, Extras: c.extras})["roles"]
		if got != c.want {
			t.Errorf("roles %v, extras %v: %s, want %s", c.roles, c.extras, got, c.want)
		}
	}
	if err := CheckRoles([]string{"dev", Extra}); err != nil {
		t.Errorf("CheckRoles(dev, extras): %v", err)
	}
	if got := WithExtras([]string{"extras", "base"}, false); !reflect.DeepEqual(got, []string{"base"}) {
		t.Errorf("WithExtras off: %v", got)
	}
}

// init's roles: extras is its own question (--extras), so a role named
// extras is refused with that flag, and the list shown leaves it out.
func TestCheckInitRoles(t *testing.T) {
	if err := CheckInitRoles([]string{"dev", "desktop"}); err != nil {
		t.Errorf("dev, desktop: %v", err)
	}
	for _, rs := range [][]string{{Extra}, {"dev", Extra}} {
		err := CheckInitRoles(rs)
		if err == nil || !strings.Contains(err.Error(), "--extras") {
			t.Errorf("%v: %v, want a refusal naming --extras", rs, err)
		}
	}
	err := CheckInitRoles([]string{"foo"})
	if err == nil || err.Error() != `role "foo": one of base, dev, desktop` {
		t.Errorf("foo: %v", err)
	}
}

// FlakeHasExtras: some host's roles in flake.nix list extras.
func TestFlakeHasExtras(t *testing.T) {
	for _, c := range []struct {
		flake string
		want  bool
	}{
		{"hosts.\"a\" = {\n  roles = [ \"dev\" \"extras\" ];\n};\n", true},
		{"hosts.\"a\" = {\n  roles = [\n    \"extras\"\n  ];\n};\n", true},
		{"hosts.\"a\" = {\n  roles = [ \"dev\" ];\n};\n# extras = \"extras\";\n", false},
		{"roles = [ ];\n", false},
	} {
		if got := FlakeHasExtras([]byte(c.flake)); got != c.want {
			t.Errorf("%q: %v, want %v", c.flake, got, c.want)
		}
	}
}

func TestRenderRejectsUnknownPlaceholder(t *testing.T) {
	tmpl := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpl, "hosts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for f, body := range map[string]string{"flake.nix": "x = @nope@;\n", "hosts/linux.nix": "{}\n", "hosts/darwin.nix": "{}\n"} {
		if err := os.WriteFile(filepath.Join(tmpl, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := Render(tmpl, filepath.Join(t.TempDir(), "o"), instanceAnswers("linux"))
	if err == nil || !strings.Contains(err.Error(), "@nope@") {
		t.Fatalf("err = %v, want one naming @nope@", err)
	}
}

func TestRenderRefusesExisting(t *testing.T) {
	tmpl := repoPath(t, "PISTE_TEMPLATE_DIR", "templates/default")
	dst := t.TempDir()
	if err := Render(tmpl, dst, instanceAnswers("linux")); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("err = %v, want exists", err)
	}
}

func TestEngineRef(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	for _, c := range []struct{ origin, tag, rev, want string }{
		{"git@github.com:o/r.git", "v1", sha, "git+ssh://git@github.com/o/r.git?ref=refs/tags/v1"},
		{"git@github.com:o/r", "", sha, "git+ssh://git@github.com/o/r.git?rev=" + sha},
		{"https://github.com/o/r", "v1", sha, "github:o/r/v1"},
		{"https://github.com/o/r.git", "", sha, "github:o/r/" + sha},
		{"https://git.example.com/r.git", "", sha, "git+https://git.example.com/r.git?rev=" + sha},
		{"ssh://git@git.example.com/r.git", "v2", sha, "git+ssh://git@git.example.com/r.git?ref=refs/tags/v2"},
	} {
		got, err := EngineRef(c.origin, c.tag, c.rev)
		if err != nil || got != c.want {
			t.Errorf("EngineRef(%q, %q) = %q, %v; want %q", c.origin, c.tag, got, err, c.want)
		}
	}
	if _, err := EngineRef("", "v1", sha); err == nil || !strings.Contains(err.Error(), "--engine") {
		t.Errorf("no origin: err = %v, want one suggesting --engine", err)
	}
}

func TestHostKey(t *testing.T) {
	if got := HostKey(instanceAnswers("linux")); got != "jane@devbox" {
		t.Errorf("linux = %q", got)
	}
	if got := HostKey(instanceAnswers("darwin")); got != "jane-mac" {
		t.Errorf("darwin = %q", got)
	}
	// nixos-rebuild picks the configuration by the hostname: no user@.
	if got := HostKey(instanceAnswers("nixos")); got != "devbox" {
		t.Errorf("nixos = %q", got)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(instanceAnswers("linux")); err != nil {
		t.Fatalf("instance answers: %v", err)
	}
	dotted := instanceAnswers("linux")
	dotted.User, dotted.Hostname = "jane.doe", "box.lan"
	if err := Validate(dotted); err != nil {
		t.Fatalf("a dotted user and host on linux (quoted everywhere): %v", err)
	}
	if err := Validate(instanceAnswers("nixos")); err != nil {
		t.Fatalf("NixOS instance answers: %v", err)
	}
	for _, c := range []struct {
		edit func(*Answers)
		want string
	}{
		{func(a *Answers) { a.Hostname = "my mac" }, "--host"},
		{func(a *Answers) { a.User = "j ane" }, "--user"},
		{func(a *Answers) { a.Roles = []string{"container"} }, "container"},
		{func(a *Answers) { a.Zone = "shared" }, "--zone"},
		{func(a *Answers) { a.GitEmail = "" }, "--git-email"},
		{func(a *Answers) { a.GitName = "" }, "--git-name"},
		{func(a *Answers) { a.System = "riscv64-linux" }, "--system"},
		{func(a *Answers) { a.Name = "../x" }, "name"},
		// darwin-rebuild indexes darwinConfigurations.<host> unquoted.
		{func(a *Answers) { a.System, a.Hostname = "aarch64-darwin", "jane-mac.lan" }, "--host"},
		// A NixOS host's name is its hostname: no dots, not a fresh
		// machine's; and it is a Linux system.
		{func(a *Answers) { a.NixOS, a.Hostname = true, "box.lan" }, "a NixOS hostname can't contain dots (--host)"},
		{func(a *Answers) { a.NixOS, a.Hostname = true, "nixos" }, `"nixos" is NixOS's default hostname`},
		{func(a *Answers) { a.NixOS, a.Hostname = true, "localhost" }, "(--host)"},
		{func(a *Answers) { a.NixOS, a.System = true, "aarch64-darwin" }, "a NixOS host is aarch64-linux or x86_64-linux (--system)"},
	} {
		a := instanceAnswers("linux")
		c.edit(&a)
		if err := Validate(a); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want an error mentioning %q, got %v", c.want, err)
		}
	}
}

func TestCheckoutExpr(t *testing.T) {
	if got := CheckoutExpr("/home/jane/piste", "/home/jane"); got != `"${config.home.homeDirectory}/piste"` {
		t.Errorf("under home = %s", got)
	}
	if got := CheckoutExpr("/srv/ck", "/home/jane"); got != `"/srv/ck"` {
		t.Errorf("elsewhere = %s", got)
	}
}

// Free text lands inside Nix strings: escaped.
func TestNixString(t *testing.T) {
	if got := NixString(`Jane "J" ${x} \ Doe`); got != `Jane \"J\" \${x} \\ Doe` {
		t.Errorf("NixString = %s", got)
	}
}

func TestRenderEscapesEngine(t *testing.T) {
	tmpl := repoPath(t, "PISTE_TEMPLATE_DIR", "templates/default")
	dst := filepath.Join(t.TempDir(), "o")
	a := instanceAnswers("linux")
	a.Engine = `github:o/r"x`
	if err := Render(tmpl, dst, a); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "flake.nix"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"github:o/r\"x"`) {
		t.Errorf("flake.nix doesn't carry the engine escaped:\n%s", b)
	}
}

func TestEngineRefLocalPath(t *testing.T) {
	for _, c := range []struct{ origin, tag, rev, want string }{
		{"/srv/piste", "v1.0.0", "", "git+file:///srv/piste?ref=refs/tags/v1.0.0"},
		{"file:///srv/piste", "", "abc", "git+file:///srv/piste?rev=abc"},
	} {
		got, err := EngineRef(c.origin, c.tag, c.rev)
		if err != nil || got != c.want {
			t.Errorf("EngineRef(%q) = %q, %v; want %q", c.origin, got, err, c.want)
		}
	}
}

func TestEngineRefScpWithoutUser(t *testing.T) {
	for _, c := range []struct{ origin, want string }{
		{"gitbox:o/piste.git", "git+ssh://gitbox/o/piste.git?ref=refs/tags/v1.0.0"},
		{"me@gitbox:/srv/piste.git", "git+ssh://me@gitbox/srv/piste.git?ref=refs/tags/v1.0.0"},
	} {
		got, err := EngineRef(c.origin, "v1.0.0", "")
		if err != nil || got != c.want {
			t.Errorf("EngineRef(%q) = %q, %v; want %q", c.origin, got, err, c.want)
		}
	}
}

func TestRenderSkipsJunk(t *testing.T) {
	tmpl := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpl, "hosts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"flake.nix", "hosts/linux.nix", "hosts/darwin.nix", ".DS_Store", "hosts/.linux.nix.swp", "README.md~", ".#flake.nix", "#flake.nix#", "x.swo"} {
		if err := os.WriteFile(filepath.Join(tmpl, f), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(t.TempDir(), "o")
	if err := Render(tmpl, dst, instanceAnswers("linux")); err != nil {
		t.Fatal(err)
	}
	got := keys(tree(t, dst))
	sort.Strings(got)
	if want := []string{"flake.nix", "hosts/devbox.nix"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rendered %v, want %v", got, want)
	}
}

// A NixOS host's name is its hostname (nixos-rebuild picks the
// configuration by it): no dots (NixOS refuses them), and not the name every
// fresh NixOS machine has.
func TestCheckNixOSName(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"devbox", ""},
		{"box-2", ""},
		{"a.b", "a NixOS hostname can't contain dots"},
		{"nixos", `"nixos" is NixOS's default hostname: give this machine its own name with --name`},
		{"localhost", `"localhost" is every machine's name for itself: give this machine its own name with --name`},
		{"", "a NixOS host needs a name: give this machine its own name with --name"},
		// NixOS's networking.hostName: a DNS label.
		{"box_", "a NixOS hostname has only letters, digits and hyphens"},
		{"my_box", "a NixOS hostname has only letters, digits and hyphens"},
		{"-box", "a NixOS hostname starts and ends with a letter or digit"},
		{"box-", "a NixOS hostname starts and ends with a letter or digit"},
		{"Box9", ""},
		{"9", ""},
		{strings.Repeat("a", 63), ""},
		{strings.Repeat("a", 64), "a NixOS hostname is at most 63 characters"},
	} {
		err := CheckNixOSName(c.name)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != c.want {
			t.Errorf("CheckNixOSName(%q) = %q, want %q", c.name, got, c.want)
		}
	}
	// The hostname check routes a NixOS host's name through it, naming no
	// flag (init's is --host, host add's --name).
	for _, h := range []string{"a.b", "nixos", "localhost", "box_", "-box"} {
		if err := CheckHostname(h, "x86_64-linux", true); err == nil || strings.Contains(err.Error(), "--") {
			t.Errorf("CheckHostname(%q, NixOS) = %v, want an error naming no flag", h, err)
		}
		if h != "a.b" {
			if err := CheckHostname(h, "x86_64-linux", false); err != nil {
				t.Errorf("CheckHostname(%q, a home) = %v, want none", h, err)
			}
		}
	}
	if err := CheckNixOSSystem("aarch64-darwin"); err == nil {
		t.Error("CheckNixOSSystem(aarch64-darwin): want an error")
	}
	for _, s := range []string{"x86_64-linux", "aarch64-linux"} {
		if err := CheckNixOSSystem(s); err != nil {
			t.Errorf("CheckNixOSSystem(%s) = %v", s, err)
		}
	}
}

func TestChecksNameNoFlag(t *testing.T) {
	err := CheckHostname("my mac", "x86_64-linux", false)
	if err == nil || strings.Contains(err.Error(), "--") {
		t.Errorf("CheckHostname = %v, want an error naming no flag", err)
	}
	for _, e := range []error{CheckUser("a b"), CheckSystem("x"), CheckRoles([]string{"container"}), CheckZone("Bad Zone"), CheckGit("git name", ""), CheckHostname("a.b", "aarch64-darwin", false), CheckNixOSSystem("aarch64-darwin")} {
		if e == nil || strings.Contains(e.Error(), "(--") {
			t.Errorf("check error %v: want an error naming no flag", e)
		}
	}
	a := instanceAnswers("linux")
	a.Hostname = "my mac"
	if err := Validate(a); err == nil || !strings.Contains(err.Error(), "(--host") {
		t.Errorf("Validate = %v, want one naming --host", err)
	}
}

// addHost adds a's host to the overlay at dst as piste host add does: its
// host file, and its entry in flake.nix.
func addHost(t *testing.T, tmpl, dst string, a Answers) {
	t.Helper()
	files, entry, err := RenderHost(tmpl, a)
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		p := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	flake, err := os.ReadFile(filepath.Join(dst, "flake.nix"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := AddHostEntry(flake, entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "flake.nix"), out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// RenderHost is what Render writes for one host: its host files, the same
// bytes, and its hosts entry from the template's flake.nix, unindented.
func TestRenderHost(t *testing.T) {
	tmpl := repoPath(t, "PISTE_TEMPLATE_DIR", "templates/default")
	inst := repoPath(t, "PISTE_TEMPLATE_INSTANCES", "tests/fixtures/template")
	files, entry, err := RenderHost(tmpl, instanceAnswers("linux"))
	if err != nil {
		t.Fatal(err)
	}
	if got := mapKeys(files); !reflect.DeepEqual(got, []string{"hosts/devbox.nix"}) {
		t.Errorf("linux files %v, want hosts/devbox.nix alone", got)
	}
	if want := readFile(t, filepath.Join(inst, "linux", "hosts", "devbox.nix")); string(files["hosts/devbox.nix"]) != want {
		t.Errorf("linux host file:\n%s\nwant the instance's:\n%s", files["hosts/devbox.nix"], want)
	}
	if want := "hosts.\"jane@devbox\" = {\n  system = \"x86_64-linux\";\n  roles = [ \"dev\" ];\n  modules = [ ./hosts/devbox.nix ];\n};\n"; entry != want {
		t.Errorf("linux entry:\n%s\nwant:\n%s", entry, want)
	}
	files, entry, err = RenderHost(tmpl, instanceAnswers("darwin"))
	if err != nil {
		t.Fatal(err)
	}
	if got := mapKeys(files); !reflect.DeepEqual(got, []string{"hosts/jane-mac.nix"}) {
		t.Errorf("darwin files %v, want hosts/jane-mac.nix alone", got)
	}
	if want := readFile(t, filepath.Join(inst, "darwin", "hosts", "jane-mac.nix")); string(files["hosts/jane-mac.nix"]) != want {
		t.Errorf("darwin host file:\n%s\nwant the instance's:\n%s", files["hosts/jane-mac.nix"], want)
	}
	if !strings.HasPrefix(entry, "hosts.\"jane-mac\" = {\n") || !strings.Contains(entry, "roles = [ ];") || strings.Contains(entry, "nixos") {
		t.Errorf("darwin entry:\n%s", entry)
	}

	// A NixOS host is a directory: default.nix and the machine's own
	// configuration beside it. Its key is the hostname.
	files, entry, err = RenderHost(tmpl, instanceAnswers("nixos"))
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"hosts/devbox/default.nix", "hosts/devbox/machine/configuration.nix"}
	if got := mapKeys(files); !reflect.DeepEqual(got, paths) {
		t.Errorf("nixos files %v, want %v", got, paths)
	}
	for _, p := range paths {
		if want := readFile(t, filepath.Join(inst, "nixos", p)); string(files[p]) != want {
			t.Errorf("nixos %s:\n%s\nwant the instance's:\n%s", p, files[p], want)
		}
	}
	for _, want := range []string{"hosts.\"devbox\" = {\n", "  nixos = true;\n", "  system = \"x86_64-linux\";\n", "  modules = [ ./hosts/devbox ];\n"} {
		if !strings.Contains(entry, want) {
			t.Errorf("nixos entry lacks %q:\n%s", want, entry)
		}
	}
	if !strings.Contains(string(files["hosts/devbox/default.nix"]), `authorizedKeys.keys = [ "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyOnlyForTests jane@example.com" ];`) {
		t.Errorf("nixos default.nix lacks the authorized key:\n%s", files["hosts/devbox/default.nix"])
	}
}

func mapKeys(m map[string][]byte) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// hostsFiles: the files of the overlay at dst under hosts/, sorted.
func hostsFiles(t *testing.T, dst string) []string {
	t.Helper()
	var out []string
	for f := range tree(t, dst) {
		if strings.HasPrefix(f, "hosts/") {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// Render places a NixOS host's two files in a directory of its own, and drops
// them (and nothing else) for any other host.
func TestRenderNixosFiles(t *testing.T) {
	tmpl := repoPath(t, "PISTE_TEMPLATE_DIR", "templates/default")
	for _, c := range []struct {
		kind string
		want []string
	}{
		{"nixos", []string{"hosts/devbox/default.nix", "hosts/devbox/machine/configuration.nix"}},
		{"linux", []string{"hosts/devbox.nix"}},
		{"darwin", []string{"hosts/jane-mac.nix"}},
	} {
		dst := filepath.Join(t.TempDir(), "o")
		if err := Render(tmpl, dst, instanceAnswers(c.kind)); err != nil {
			t.Fatal(err)
		}
		if got := hostsFiles(t, dst); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: hosts files %v, want %v", c.kind, got, c.want)
		}
	}
}

// A template with no NixOS host files (a team layer's starter) can't make
// a NixOS host: refused, not an overlay without a host file.
func TestRenderNixosWithoutTemplate(t *testing.T) {
	tmpl := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpl, "hosts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"flake.nix", "hosts/linux.nix", "hosts/darwin.nix"} {
		if err := os.WriteFile(filepath.Join(tmpl, f), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(t.TempDir(), "o")
	err := Render(tmpl, dst, instanceAnswers("nixos"))
	if err == nil || !strings.Contains(err.Error(), "NixOS") {
		t.Fatalf("err = %v, want one naming NixOS", err)
	}
	if _, serr := os.Lstat(dst); serr == nil {
		t.Errorf("%s left behind", dst)
	}
}

// A template missing one of the NixOS host files (hosts/nixos.nix,
// hosts/nixos-machine.nix) can't make a NixOS host: Render and RenderHost
// both name the file it lacks, and Render leaves nothing behind.
func TestRenderNixosMissingHostFile(t *testing.T) {
	for _, missing := range []string{"hosts/nixos.nix", "hosts/nixos-machine.nix"} {
		tmpl := t.TempDir()
		if err := os.MkdirAll(filepath.Join(tmpl, "hosts"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"flake.nix", "hosts/linux.nix", "hosts/darwin.nix", "hosts/nixos.nix", "hosts/nixos-machine.nix"} {
			if f == missing {
				continue
			}
			if err := os.WriteFile(filepath.Join(tmpl, f), []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want := "the template has no " + missing + " (a NixOS host needs hosts/nixos.nix and hosts/nixos-machine.nix)"
		if _, _, err := RenderHost(tmpl, instanceAnswers("nixos")); err == nil || err.Error() != want {
			t.Errorf("RenderHost without %s: err = %v, want %q", missing, err, want)
		}
		dst := filepath.Join(t.TempDir(), "o")
		if err := Render(tmpl, dst, instanceAnswers("nixos")); err == nil || err.Error() != want {
			t.Errorf("Render without %s: err = %v, want %q", missing, err, want)
		}
		if _, err := os.Lstat(dst); err == nil {
			t.Errorf("Render without %s: %s left behind", missing, dst)
		}
	}
}

// nixosLine and authorizedKeys: a Nix list of strings, escaped; [ ] for none.
func TestValuesNixos(t *testing.T) {
	v := values(instanceAnswers("linux"))
	if v["nixosLine"] != "" || v["authorizedKeys"] != "[ ]" || v["hostModule"] != "./hosts/devbox.nix" {
		t.Errorf("linux: nixosLine %q, authorizedKeys %q, hostModule %q", v["nixosLine"], v["authorizedKeys"], v["hostModule"])
	}
	a := instanceAnswers("nixos")
	a.AuthorizedKeys = []string{`ssh-ed25519 AAAA "x" ${y}`, "ssh-rsa BBBB"}
	v = values(a)
	if v["nixosLine"] != "nixos = true;" || v["hostModule"] != "./hosts/devbox" {
		t.Errorf("nixos: nixosLine %q, hostModule %q", v["nixosLine"], v["hostModule"])
	}
	if want := `[ "ssh-ed25519 AAAA \"x\" \${y}" "ssh-rsa BBBB" ]`; v["authorizedKeys"] != want {
		t.Errorf("authorizedKeys = %s, want %s", v["authorizedKeys"], want)
	}
	a.AuthorizedKeys = nil
	if got := values(a)["authorizedKeys"]; got != "[ ]" {
		t.Errorf("no keys: authorizedKeys = %s", got)
	}
}

// A line whose only content is a placeholder that fills to nothing is dropped
// whole (no blank line left); a placeholder among other text, or one that
// fills, stays a line.
func TestFillDropsEmptyPlaceholderLine(t *testing.T) {
	vals := map[string]string{"empty": "", "full": "nixos = true;", "x": "X"}
	for _, c := range []struct{ in, want string }{
		{"a\n  @empty@\nb\n", "a\nb\n"},
		{"a\n\t @empty@ \t\nb\n", "a\nb\n"},
		{"a\n  @full@\nb\n", "a\n  nixos = true;\nb\n"},
		{"a\n  x = @empty@;\nb\n", "a\n  x = ;\nb\n"},
		{"a\n  @empty@ @x@\nb\n", "a\n   X\nb\n"},
		{"a\n\nb\n", "a\n\nb\n"},
		{"a\n  @empty@", "a\n"},
		{"@empty@\n@empty@\n", ""},
	} {
		got, err := fill("t", c.in, vals)
		if err != nil || got != c.want {
			t.Errorf("fill(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	// A placeholder it doesn't know is still an error, alone on its line or not.
	if _, err := fill("t.nix", "a\n  @nope@\n", vals); err == nil || !strings.Contains(err.Error(), "@nope@") {
		t.Errorf("unknown placeholder alone on a line: err = %v", err)
	}
}

func TestRenderHostNoEntry(t *testing.T) {
	tmpl := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpl, "hosts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for f, body := range map[string]string{"flake.nix": "{ hosts = { }; }\n", "hosts/linux.nix": "{}\n", "hosts/darwin.nix": "{}\n"} {
		if err := os.WriteFile(filepath.Join(tmpl, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := RenderHost(tmpl, instanceAnswers("linux")); err == nil || !strings.Contains(err.Error(), "flake.nix") {
		t.Fatalf("err = %v, want one naming flake.nix", err)
	}
}

// without drops the lines at the given 0-based indexes.
func without(s string, drop ...int) string {
	lines := strings.SplitAfter(s, "\n")
	var keep []string
	for i, l := range lines {
		skip := false
		for _, d := range drop {
			skip = skip || i == d
		}
		if !skip {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "")
}

func TestAddHostEntry(t *testing.T) {
	inst := repoPath(t, "PISTE_TEMPLATE_INSTANCES", "tests/fixtures/template")
	entry := "hosts.\"jane@box\" = {\n  system = \"x86_64-linux\";\n\n  modules = [ ];\n};\n"

	// The template's shape: dotted entries.
	flake := readFile(t, filepath.Join(inst, "linux", "flake.nix"))
	out, err := AddHostEntry([]byte(flake), entry)
	if err != nil {
		t.Fatal(err)
	}
	at := strings.Index(flake, "      hosts.\"jane@devbox\" = {")
	want := flake[:at] + "      hosts.\"jane@box\" = {\n        system = \"x86_64-linux\";\n\n        modules = [ ];\n      };\n\n" + flake[at:]
	if string(out) != want {
		t.Errorf("dotted:\n%s\nwant:\n%s", out, want)
	}
	n := strings.Count(flake[:at], "\n")
	if got := without(string(out), n, n+1, n+2, n+3, n+4, n+5); got != flake {
		t.Errorf("dotted: other bytes changed:\n%s", got)
	}

	// hosts = { ... };
	set := "    {\n      hosts = {\n        \"jane@other\" = { system = \"x86_64-linux\"; };\n      };\n    }\n"
	out, err = AddHostEntry([]byte(set), entry)
	if err != nil {
		t.Fatal(err)
	}
	if want := "    {\n      hosts.\"jane@box\" = {\n        system = \"x86_64-linux\";\n\n        modules = [ ];\n      };\n\n      hosts = {\n"; !strings.HasPrefix(string(out), want) {
		t.Errorf("hosts = { }:\n%s", out)
	}

	// No hosts line: refused, the input back.
	none := "{ outputs = _: { }; }\n"
	out, err = AddHostEntry([]byte(none), entry)
	if !errors.Is(err, errNoHostsLine) || string(out) != none {
		t.Errorf("no hosts line: %q, %v", out, err)
	}
}

func TestFlakeUser(t *testing.T) {
	inst := repoPath(t, "PISTE_TEMPLATE_INSTANCES", "tests/fixtures/template")
	if u, ok := FlakeUser([]byte(readFile(t, filepath.Join(inst, "linux", "flake.nix")))); u != "jane" || !ok {
		t.Errorf("instance: %q, %v", u, ok)
	}
	if _, ok := FlakeUser([]byte("{ }\n")); ok {
		t.Error("no user line: ok")
	}
}

// A new host takes the roles its platform's other hosts have: what each one
// gets (a role brings the ones it imports, desktop dev and base; a Mac has
// desktop's from its platform; a host's own modules may import one), the
// roles all of them share, written as short as init writes them.
func TestSharedRoles(t *testing.T) {
	for _, c := range []struct {
		name   string
		darwin bool
		hosts  map[string][]string
		want   []string
	}{
		{"one linux host", false, map[string][]string{"jane@a": {"base", "dev", "extras"}}, []string{"dev", "extras"}},
		{"desktop implies dev", false, map[string][]string{"jane@a": {"base", "dev", "desktop"}}, []string{"desktop"}},
		{"base only", false, map[string][]string{"jane@a": {"base"}}, []string{"base"}},
		{"no roles", false, map[string][]string{"jane@a": nil}, []string{}},
		{"the ones they share", false, map[string][]string{
			"jane@a": {"base", "dev", "desktop", "extras"},
			"jane@b": {"base", "dev"},
		}, []string{"dev"}},
		{"extras shared", false, map[string][]string{
			"jane@a": {"base", "dev", "desktop", "extras"},
			"jane@b": {"base", "dev", "extras"},
		}, []string{"dev", "extras"}},
		{"not a host role", false, map[string][]string{"jane@a": {"base", "container", "dev"}}, []string{"dev"}},
		{"mac with extras", true, map[string][]string{"mac": {"base", "dev", "desktop", "extras"}}, []string{"extras"}},
		{"mac without", true, map[string][]string{"mac": {"base", "dev", "desktop"}}, []string{}},
		{"mac and its alias", true, map[string][]string{
			"mac": {"base", "dev", "desktop", "extras"}, "MB1": {"base", "dev", "desktop", "extras"},
		}, []string{"extras"}},
	} {
		got, ok := SharedRoles(c.darwin, c.hosts)
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: SharedRoles = %q, %v; want %q", c.name, got, ok, c.want)
		}
	}
	if got, ok := SharedRoles(false, nil); ok {
		t.Errorf("no hosts: SharedRoles = %q, true; want not ok (the defaults)", got)
	}
}
