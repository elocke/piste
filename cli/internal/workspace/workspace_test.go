package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// tree creates files (and their directories) under a fresh temp dir and
// returns its physical path.
func tree(t *testing.T, files ...string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		p := filepath.Join(root, f)
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFindCheckout(t *testing.T) {
	root := tree(t, "piste/.piste", "piste/flake.nix", "piste/overlays/a/flake.nix", "piste/overlays/a/sub/", "elsewhere/")
	ck := filepath.Join(root, "piste")
	for _, from := range []string{ck, filepath.Join(ck, "overlays/a/sub")} {
		if got, ok := FindCheckout(from); !ok || got != ck {
			t.Errorf("FindCheckout(%s) = %q, %v; want %q", from, got, ok, ck)
		}
	}
	if got, ok := FindCheckout(filepath.Join(root, "elsewhere")); ok {
		t.Errorf("FindCheckout(elsewhere) = %q, found", got)
	}
}

func TestOverlays(t *testing.T) {
	root := tree(t, ".piste", "overlays/b/flake.nix", "overlays/a/flake.nix", "overlays/c/", "overlays/x.nix")
	got, err := Overlays(root)
	if err != nil || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("Overlays = %v, %v", got, err)
	}
	if got, err := Overlays(tree(t, ".piste")); err != nil || len(got) != 0 {
		t.Fatalf("no overlays/: %v, %v", got, err)
	}
}

func TestOverlayOf(t *testing.T) {
	root := tree(t, ".piste", "overlays/a/flake.nix", "overlays/a/sub/")
	for dir, want := range map[string]string{
		filepath.Join(root, "overlays/a"):     "a",
		filepath.Join(root, "overlays/a/sub"): "a",
	} {
		if got, ok := OverlayOf(root, dir); !ok || got != want {
			t.Errorf("OverlayOf(%s) = %q, %v", dir, got, ok)
		}
	}
	for _, dir := range []string{root, filepath.Join(root, "overlays"), filepath.Dir(root)} {
		if got, ok := OverlayOf(root, dir); ok {
			t.Errorf("OverlayOf(%s) = %q, want none", dir, got)
		}
	}
}

func TestMatches(t *testing.T) {
	mac := Query{Hostname: "mac", User: "u", Darwin: true}
	box := Query{Hostname: "box", User: "u"}
	if !Matches(Hosts{Darwin: []string{"mac"}}, mac) {
		t.Error("darwin host not matched")
	}
	if Matches(Hosts{Home: []string{"u@mac"}}, mac) {
		t.Error("darwin matched a home configuration")
	}
	if !Matches(Hosts{Home: []string{"u@box"}}, box) {
		t.Error("linux user@host not matched")
	}
	if Matches(Hosts{Home: []string{"v@box"}}, box) {
		t.Error("linux matched another user's host")
	}
}

// hostsOf answers from a table; an overlay missing from it fails the test.
func hostsOf(t *testing.T, table map[string]Hosts, fails map[string]error) HostsFunc {
	return func(dir string) (Hosts, error) {
		name := filepath.Base(dir)
		if err, ok := fails[name]; ok {
			return Hosts{}, err
		}
		h, ok := table[name]
		if !ok {
			t.Errorf("hosts called for %s", name)
		}
		return h, nil
	}
}

func TestResolve(t *testing.T) {
	two := tree(t, ".piste", "overlays/a/flake.nix", "overlays/b/flake.nix", "overlays/b/sub/")
	box := Query{Cwd: two, Hostname: "box", User: "u"}

	t.Run("--overlay", func(t *testing.T) {
		q := box
		q.Overlay = "b"
		r, err := Resolve(two, q, hostsOf(t, nil, nil))
		if err != nil || r.Overlay != "b" || r.Dir != filepath.Join(two, "overlays/b") || r.Reason != "--overlay" || r.Root != two {
			t.Fatalf("%+v, %v", r, err)
		}
		q.Overlay = "nope"
		_, err = Resolve(two, q, hostsOf(t, nil, nil))
		if err == nil || !strings.Contains(err.Error(), "no overlay nope in "+two+"/overlays (have: a, b)") {
			t.Fatalf("missing overlay: %v", err)
		}
	})

	t.Run("cwd", func(t *testing.T) {
		q := box
		q.Cwd = filepath.Join(two, "overlays/b/sub")
		r, err := Resolve(two, q, hostsOf(t, nil, nil))
		if err != nil || r.Overlay != "b" || r.Reason != "cwd" {
			t.Fatalf("%+v, %v", r, err)
		}
	})

	t.Run("no overlays: the engine", func(t *testing.T) {
		bare := tree(t, ".piste", "flake.nix")
		r, err := Resolve(bare, Query{Cwd: bare, Hostname: "box", User: "u"}, hostsOf(t, nil, nil))
		if err != nil || r.Dir != bare || r.Overlay != "" || r.Reason != "no overlays: the engine" {
			t.Fatalf("%+v, %v", r, err)
		}
		if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "no overlay in "+bare+"/overlays: clone your overlay repo there") {
			t.Fatalf("warnings %v", r.Warnings)
		}
	})

	t.Run("the only overlay, without evaluating", func(t *testing.T) {
		one := tree(t, ".piste", "overlays/solo/flake.nix")
		r, err := Resolve(one, Query{Cwd: one, Hostname: "box", User: "u"}, func(string) (Hosts, error) {
			t.Error("hosts evaluated for the only overlay")
			return Hosts{}, nil
		})
		if err != nil || r.Overlay != "solo" || r.Reason != "the only overlay" {
			t.Fatalf("%+v, %v", r, err)
		}
	})

	t.Run("by host", func(t *testing.T) {
		r, err := Resolve(two, box, hostsOf(t, map[string]Hosts{"a": {Home: []string{"u@box"}}, "b": {Home: []string{"u@other"}}}, nil))
		if err != nil || r.Overlay != "a" || r.Reason != "hosts include u@box" {
			t.Fatalf("%+v, %v", r, err)
		}
		mac := Query{Cwd: two, Hostname: "mac", User: "u", Darwin: true}
		r, err = Resolve(two, mac, hostsOf(t, map[string]Hosts{"a": {Darwin: []string{"pc"}}, "b": {Darwin: []string{"mac"}}}, nil))
		if err != nil || r.Overlay != "b" || r.Reason != "hosts include mac" {
			t.Fatalf("%+v, %v", r, err)
		}
	})

	t.Run("no overlay has this host", func(t *testing.T) {
		_, err := Resolve(two, box, hostsOf(t, map[string]Hosts{"a": {}, "b": {}}, nil))
		if err == nil || !strings.Contains(err.Error(), "no overlay in "+two+"/overlays has this host (box); pass --overlay <name>") {
			t.Fatalf("%v", err)
		}
	})

	t.Run("two overlays have this host", func(t *testing.T) {
		both := Hosts{Home: []string{"u@box"}}
		_, err := Resolve(two, box, hostsOf(t, map[string]Hosts{"a": both, "b": both}, nil))
		if err == nil || !strings.Contains(err.Error(), "overlays a, b both have this host (box): pass --overlay <name>") {
			t.Fatalf("%v", err)
		}
	})

	t.Run("a failing overlay does not hide a match", func(t *testing.T) {
		boom := errors.New("boom")
		r, err := Resolve(two, box, hostsOf(t, map[string]Hosts{"b": {Home: []string{"u@box"}}}, map[string]error{"a": boom}))
		if err != nil || r.Overlay != "b" || !reflect.DeepEqual(r.Warnings, []string{"overlay a: boom"}) {
			t.Fatalf("%+v, %v", r, err)
		}
		_, err = Resolve(two, box, hostsOf(t, map[string]Hosts{"b": {}}, map[string]error{"a": boom}))
		if err == nil || !strings.HasPrefix(err.Error(), "could not evaluate overlay a: boom") ||
			!strings.Contains(err.Error(), "no other overlay in "+two+"/overlays has this host (box)") {
			t.Fatalf("failure not leading: %v", err)
		}
	})
}

func TestOverrideRef(t *testing.T) {
	root := tree(t, "repo/.git/HEAD", "worktree/.git", "plain/")
	for dir, want := range map[string]string{
		filepath.Join(root, "repo"):     "git+file://" + filepath.Join(root, "repo"),
		filepath.Join(root, "worktree"): "git+file://" + filepath.Join(root, "worktree"),
		filepath.Join(root, "plain"):    "path:" + filepath.Join(root, "plain"),
	} {
		if got := OverrideRef(dir); got != want {
			t.Errorf("OverrideRef(%s) = %q, want %q", dir, got, want)
		}
	}
}

func TestCheckoutFromManifest(t *testing.T) {
	root := tree(t, "piste/.piste", "nomark/")
	write := func(workdir string) string {
		p := filepath.Join(t.TempDir(), "manifest.json")
		body := `{"sources":[{"name":"personal-agents","workdir":"/x"},{"name":"piste-agents","workdir":"` + workdir + `"}]}`
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ck := filepath.Join(root, "piste")
	if got, ok := CheckoutFromManifest(write(filepath.Join(ck, "agents"))); !ok || got != ck {
		t.Errorf("= %q, %v; want %q", got, ok, ck)
	}
	if got, ok := CheckoutFromManifest(write(filepath.Join(root, "nomark", "agents"))); ok {
		t.Errorf("unmarked checkout accepted: %q", got)
	}
	if got, ok := CheckoutFromManifest(filepath.Join(root, "missing.json")); ok {
		t.Errorf("missing manifest accepted: %q", got)
	}
}
