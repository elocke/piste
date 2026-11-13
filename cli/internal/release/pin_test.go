package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	ghURL  = "github:example/piste/v0.1.0"
	gitURL = "git+file:///x/piste.git?ref=refs/tags/v0.1.0"
)

// The two input forms, around a URL.
func attrForm(url string) string {
	return "{\n  description = \"jane's overlay\";\n  inputs.piste.url = \"" + url + "\";\n" +
		"  inputs.nixpkgs.follows = \"piste/nixpkgs\";\n  outputs = _: { };\n}\n"
}

func blockForm(url string) string {
	return "{\n  inputs = {\n    # the engine\n    piste = {\n      inputs.nixpkgs.follows = \"nixpkgs\";\n" +
		"      url = \"" + url + "\";\n    };\n    nixpkgs.url = \"github:NixOS/nixpkgs\";\n  };\n  outputs = _: { };\n}\n"
}

func TestReadPin(t *testing.T) {
	nested := "{\n  inputs = {\n    piste.url = \"" + gitURL + "\";\n    layer = {\n      url = \"git+file:///layer\";\n" +
		"      inputs.piste.follows = \"piste\";\n    };\n  };\n}\n"
	for name, c := range map[string]struct{ text, want string }{
		"inputs.piste.url":                   {attrForm(ghURL), ghURL},
		"piste.url inside inputs":            {nested, gitURL},
		"the block form":                     {blockForm(gitURL), gitURL},
		"the block form, url first":          {"{ inputs = {\n  piste = { url = \"" + ghURL + "\"; };\n}; }\n", ghURL},
		"a layer's follows, no pin":          {"{\n  inputs.layer = {\n    url = \"git+file:///layer\";\n    inputs.piste.follows = \"piste\";\n  };\n}\n", ""},
		"a commented pin":                    {"{\n  # inputs.piste.url = \"" + ghURL + "\";\n}\n", ""},
		"another input's nested url":         {"{\n  inputs.layer.inputs.piste.url = \"" + ghURL + "\";\n}\n", ""},
		"a commented block-form line":        {"{\n  # piste = { url = \"" + ghURL + "\"; };\n}\n", ""},
		"the block form, a block before url": {"{ inputs = {\n  piste = { inputs = { nixpkgs.follows = \"nixpkgs\"; }; url = \"" + ghURL + "\"; };\n}; }\n", ghURL},
		"a url inside the block's own block": {"{ inputs = {\n  piste = { inputs = { url = \"" + gitURL + "\"; }; };\n}; }\n", ""},
	} {
		got, ok := ReadPin(c.text)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s: ReadPin = %q, %v; want %q", name, got, ok, c.want)
		}
	}
}

// Unreadable: the engine input is declared as a block, but not with a url
// ReadPin can find, so piste update can't move it.
func TestUnreadable(t *testing.T) {
	for name, c := range map[string]struct {
		text string
		want bool
	}{
		"inputs.piste.url":          {attrForm(ghURL), false},
		"the block form":            {blockForm(gitURL), false},
		"no engine input":           {"{ inputs.nixpkgs.url = \"github:NixOS/nixpkgs\"; }\n", false},
		"a follows, not a pin":      {"{\n  inputs.piste.follows = \"layer/piste\";\n}\n", false},
		"attributes, not a url":     {"{\n  inputs.piste = { type = \"github\"; owner = \"example\"; repo = \"piste\"; ref = \"v0.1.0\"; };\n}\n", true},
		"a url that isn't a string": {"{\n  inputs = {\n    piste = {\n      url = engine;\n    };\n  };\n}\n", true},
		"a commented block":         {"{\n  # piste = { type = \"github\"; };\n}\n", false},
	} {
		if got := Unreadable(c.text); got != c.want {
			t.Errorf("%s: Unreadable = %v, want %v", name, got, c.want)
		}
	}
}

func TestPinTag(t *testing.T) {
	for url, want := range map[string]string{
		ghURL:  "v0.1.0",
		gitURL: "v0.1.0",
		"git+ssh://git@example.com/o/piste.git?ref=refs/tags/v1.2.3":           "v1.2.3",
		"git+file:///x/piste.git?rev=0123456789abcdef0123456789abcdef01234567": "",
		"github:example/piste": "",
		"github:example/piste/0123456789abcdef0123456789abcdef01234567": "",
		"git+file:///x/piste.git?ref=refs/tags/v0.3.0-rc1":              "",
		"git+ssh://git@example.com/o/r.git":                             "",
		"git+file:///x/piste.git?ref=main":                              "",
	} {
		if got := PinTag(url); got != want {
			t.Errorf("PinTag(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestMovePin(t *testing.T) {
	for _, form := range []func(string) string{attrForm, blockForm} {
		for _, url := range []string{ghURL, gitURL} {
			text := form(url)
			got, ok := MovePin(text, "v0.2.0")
			want := strings.Replace(text, "v0.1.0", "v0.2.0", 1)
			if !ok || got != want {
				t.Errorf("MovePin:\n%s\ngot (%v)\n%s\nwant\n%s", text, ok, got, want)
			}
			if p, _ := ReadPin(got); PinTag(p) != "v0.2.0" {
				t.Errorf("after the move the pin is %q", p)
			}
		}
	}
	rev := attrForm("git+file:///x/piste.git?rev=0123456789abcdef0123456789abcdef01234567")
	if got, ok := MovePin(rev, "v0.2.0"); ok || got != rev {
		t.Errorf("a rev pin moved: %v\n%s", ok, got)
	}
	none := "{ inputs.nixpkgs.url = \"github:NixOS/nixpkgs\"; }\n"
	if got, ok := MovePin(none, "v0.2.0"); ok || got != none {
		t.Errorf("no pin, moved: %v", ok)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// A release commit can carry a pre-release tag too (the rc that became it).
func TestHeadRelease(t *testing.T) {
	d := t.TempDir()
	git(t, d, "init", "-q")
	if got := HeadRelease(d); got != "" {
		t.Errorf("no commit: %q", got)
	}
	os.WriteFile(filepath.Join(d, "f"), []byte("1"), 0o644)
	git(t, d, "add", "f")
	git(t, d, "commit", "-q", "-m", "one")
	git(t, d, "tag", "v0.2.0-rc1")
	if got := HeadRelease(d); got != "" {
		t.Errorf("only a pre-release: %q", got)
	}
	git(t, d, "tag", "v0.2.0")
	git(t, d, "tag", "v0.1.9")
	if got := HeadRelease(d); got != "v0.2.0" {
		t.Errorf("HeadRelease = %q, want v0.2.0", got)
	}
}
