package drift

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestExtractCasksAndBrews(t *testing.T) {
	content := `
{ pkgs, ... }:
let
  comment = "this should not match";
  casks = [
    "raycast"
    "slack"
    "1password"
  ];
  brews = [
    "ffmpeg"
    "imagemagick"
  ];
  cli = with pkgs; [ ripgrep fd ];
in {
  environment.systemPackages = cli;
}
`
	got := keys(ExtractNixListStrings(content, []string{"casks", "brews"}))
	want := []string{"1password", "ffmpeg", "imagemagick", "raycast", "slack"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

func TestExtractHomebrewDotted(t *testing.T) {
	c := "{\n  homebrew.casks = [\n    \"discord\"\n    \"telegram\"\n  ];\n  homebrew.brews = [\n    \"mas\"\n  ];\n}\n"
	got := keys(ExtractNixListStrings(c, []string{"homebrew.casks", "homebrew.brews"}))
	if !reflect.DeepEqual(got, []string{"discord", "mas", "telegram"}) {
		t.Fatal(got)
	}
	if len(ExtractNixListStrings("", []string{"casks"})) != 0 {
		t.Fatal("empty")
	}
}

func TestExtractPnpmNixList(t *testing.T) {
	c := "  pnpmPackages = [\n    \"agent-browser\"\n    \"typescript\"\n    \"@moonrepo/cli\"\n  ];\n"
	got := ExtractNixListStrings(c, []string{"pnpmPackages"})
	for _, k := range []string{"agent-browser", "typescript", "@moonrepo/cli"} {
		if !got[k] {
			t.Fatal(k)
		}
	}
}

func TestExtractBashArrays(t *testing.T) {
	c := "home.activation.x = ''\n    PNPM_PACKAGES=(\n      \"@google/gemini-cli\"\n      \"@moonrepo/cli\"\n      \"tsx\"\n    )\n    if [ ... ]; then\n      ...\n    fi\n'';\n"
	got := keys(ExtractBashArrayStrings(c, []string{"PNPM_PACKAGES"}))
	if !reflect.DeepEqual(got, []string{"@google/gemini-cli", "@moonrepo/cli", "tsx"}) {
		t.Fatal(got)
	}
	got = keys(ExtractBashArrayStrings("NPM_PACKAGES=(\n  \"bosun\"\n  \"task-master-ai\"\n)\n", []string{"NPM_PACKAGES"}))
	if !reflect.DeepEqual(got, []string{"bosun", "task-master-ai"}) {
		t.Fatal(got)
	}
	if len(ExtractBashArrayStrings("", []string{"PNPM_PACKAGES"})) != 0 {
		t.Fatal("empty")
	}
}

func TestScanNixFiles(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "a.nix"), []byte(`casks = [ "foo" ];`), 0o644)
	os.MkdirAll(filepath.Join(d, "sub"), 0o755)
	os.WriteFile(filepath.Join(d, "sub", "b.nix"), []byte(`brews = [ "bar" ];`), 0o644)
	c := ScanNixFiles(d)
	if !contains(c, "foo") || !contains(c, "bar") {
		t.Fatal(c)
	}
	if ScanNixFiles(filepath.Join(d, "none")) != "" {
		t.Fatal("missing dir")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || filepath.Base(sub) != "" && (stringIndex(s, sub) >= 0))
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestUnpin(t *testing.T) {
	for in, want := range map[string]string{"githits@0.23.0": "githits", "@scope/pkg@1.2.3": "@scope/pkg", "@scope/pkg": "@scope/pkg", "x@latest": "x@latest"} {
		if got := Unpin(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}

// A list name inside a longer one isn't that list: npmPackages is not
// pnpmPackages, NPM_PACKAGES not PNPM_PACKAGES.
func TestExtractWholeNames(t *testing.T) {
	if got := ExtractNixListStrings("  pnpmPackages = [ \"a\" ];\n", []string{"npmPackages"}); len(got) != 0 {
		t.Errorf("npmPackages read from pnpmPackages: %v", got)
	}
	if got := ExtractBashArrayStrings("PNPM_PACKAGES=( \"b\" )\n", []string{"NPM_PACKAGES"}); len(got) != 0 {
		t.Errorf("NPM_PACKAGES read from PNPM_PACKAGES: %v", got)
	}
	if got := ExtractNixListStrings("{ npmPackages = [ \"c\" ]; homebrew.casks = [ \"d\" ]; }", []string{"npmPackages", "casks"}); !got["c"] || !got["d"] {
		t.Errorf("whole names: %v", got)
	}
}
