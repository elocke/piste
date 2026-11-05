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

// The answers tests/fixtures/template/{linux,darwin} were rendered with.
func instanceAnswers(kind string) Answers {
	a := Answers{
		Name: "personal", User: "jane", Zone: "personal", Engine: "github:elocke/piste/v0.0.0",
		Checkout: `"${config.home.homeDirectory}/piste"`, GitName: "Jane Doe", GitEmail: "jane@example.com",
		Hostname: "devbox", System: "x86_64-linux", Roles: []string{"dev"}, Home: "/home/jane",
	}
	if kind == "darwin" {
		a.Hostname, a.System, a.Roles = "jane-mac", "aarch64-darwin", nil
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
	for _, kind := range []string{"linux", "darwin", "layer-linux", "layer-darwin", "layer-linux-nosecrets", "two-hosts",
		"extras-darwin", "layer-linux-extras"} {
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

func TestChecksNameNoFlag(t *testing.T) {
	err := CheckHostname("my mac", "x86_64-linux")
	if err == nil || strings.Contains(err.Error(), "--") {
		t.Errorf("CheckHostname = %v, want an error naming no flag", err)
	}
	for _, e := range []error{CheckUser("a b"), CheckSystem("x"), CheckRoles([]string{"container"}), CheckZone("Bad Zone"), CheckGit("git name", ""), CheckHostname("a.b", "aarch64-darwin")} {
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
	host, entry, err := RenderHost(tmpl, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "hosts", a.Hostname+".nix"), host, 0o644); err != nil {
		t.Fatal(err)
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

// RenderHost is what Render writes for one host: its host file, the same
// bytes, and its hosts entry from the template's flake.nix, unindented.
func TestRenderHost(t *testing.T) {
	tmpl := repoPath(t, "PISTE_TEMPLATE_DIR", "templates/default")
	inst := repoPath(t, "PISTE_TEMPLATE_INSTANCES", "tests/fixtures/template")
	host, entry, err := RenderHost(tmpl, instanceAnswers("linux"))
	if err != nil {
		t.Fatal(err)
	}
	if want := readFile(t, filepath.Join(inst, "linux", "hosts", "devbox.nix")); string(host) != want {
		t.Errorf("linux host file:\n%s\nwant the instance's:\n%s", host, want)
	}
	if want := "hosts.\"jane@devbox\" = {\n  system = \"x86_64-linux\";\n  roles = [ \"dev\" ];\n  modules = [ ./hosts/devbox.nix ];\n};\n"; entry != want {
		t.Errorf("linux entry:\n%s\nwant:\n%s", entry, want)
	}
	host, entry, err = RenderHost(tmpl, instanceAnswers("darwin"))
	if err != nil {
		t.Fatal(err)
	}
	if want := readFile(t, filepath.Join(inst, "darwin", "hosts", "jane-mac.nix")); string(host) != want {
		t.Errorf("darwin host file:\n%s\nwant the instance's:\n%s", host, want)
	}
	if !strings.HasPrefix(entry, "hosts.\"jane-mac\" = {\n") || !strings.Contains(entry, "roles = [ ];") {
		t.Errorf("darwin entry:\n%s", entry)
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
