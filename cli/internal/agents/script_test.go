// Package agents_test runs the failure-mode suite:
// testdata/script/*.txtar against the real piste entry point.
//
// Setup writes four zone roots under $WORK/zones (shared, personal, work, agent)
// with an item of every kind, and exposes them as single arguments:
//
//	$RS  --zone-root=shared=shared:$WORK/zones/shared
//	$RP  --zone-root=personal=personal:$WORK/zones/personal
//	$RW  --zone-root=work=work:$WORK/zones/work
//	$RA  --zone-root=agent=agent:$WORK/zones/agent (any zone name works)
//
// $GOLDEN is testdata/golden: frozen outputs the engine must still
// reproduce. cmpwork / cmpworkdir compare against them with
// the work dir written as $WORK.
//
// Conditions: [fakes] (PISTE_AGENTS_FAKES names tests/agents/fakes),
// [store] (/nix/store exists), [tint] (PISTE_TINT=1: real claude / codex
// binaries).
package agents_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/elocke/piste/cli/internal/cli"
	"github.com/elocke/piste/cli/internal/scripttest"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"piste": func() {
			scripttest.PinMachine()
			os.Exit(cli.Main(os.Args[1:]))
		},
	})
}

// zoneFixture is the shared fixture: sanitised shapes, no real secrets.
var zoneFixture = map[string]string{
	"shared/skills/web/alpha/SKILL.md":    "---\nname: alpha\ndescription: Use when alpha things happen.\nallowed-tools: Bash\n---\nAlpha body.\n",
	"shared/skills/web/alpha/scripts/run": "#!/bin/sh\necho alpha\n",
	"shared/skills/tools/gamma/SKILL.md":  "---\nname: gamma\ndescription: Use when gamma.\n---\nGamma.\n",
	"shared/agents/rev.md":                "---\nname: rev\ndescription: Reviews diffs.\n---\nYou review diffs.\nBe terse.\n",
	"shared/instructions/never.md":        "## Never\n\nNever do the bad thing.\n",
	"shared/hooks.toml": `[[hook]]
name = "guard"
event = "preToolUse"
matcher = "Bash"
script = "guard.sh"
timeout = 10
targets = ["claude", "codex"]
`,
	"shared/hooks/guard.sh": "#!/bin/sh\nexit 0\n",
	"shared/permissions.toml": `[[permission]]
name = "no-rm-rf"
pattern = "rm -rf *"
decision = "deny"

[[permission]]
name = "git-status"
pattern = "git status"
decision = "allow"
`,
	"shared/agents.base.toml": `[[mcp]]
name = "demo"
command = "demo-mcp"
args = ["--stdio"]
secretEnv = ["DEMO_TOKEN"]

[[claude.marketplace]]
name = "demo-market"
source = "github:example/demo-market"

[[claude.plugin]]
id = "demoplug@demo-market"
version = "1.0.0"

[[tool]]
name = "rtk"
source = "nix:piste#rtk"
version = "0.43.0"

[[hook]]
name = "hindsight"
installer = "npm:@example/hindsight"
version = "0.7.0"
`,
	"shared/presets/hermes-family.toml":  "targets = [\"hermes\"]\nskills = [\"alpha\", \"gamma\"]\n",
	"personal/skills/home/beta/SKILL.md": "---\nname: beta\ndescription: Use when ZONE-P personal beta.\n---\nZONE-P\n",
	"work/skills/job/delta/SKILL.md":     "---\nname: delta\ndescription: Use when work delta.\n---\nWork.\n",
	"agent/skills/bot/beta2/SKILL.md":    "---\nname: beta2\ndescription: Use when ZONE-A agent beta2.\n---\nZONE-A\n",
}

func writeZones(dir string) error {
	for rel, body := range zoneFixture {
		p := filepath.Join(dir, "zones", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.Contains(rel, "/scripts/") || strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			return err
		}
	}
	return nil
}

func TestScripts(t *testing.T) {
	fakes := os.Getenv("PISTE_AGENTS_FAKES")
	golden, err := filepath.Abs("testdata/golden")
	if err != nil {
		t.Fatal(err)
	}
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(e *testscript.Env) error {
			if err := writeZones(e.WorkDir); err != nil {
				return err
			}
			home := filepath.Join(e.WorkDir, "home")
			if err := os.MkdirAll(home, 0o755); err != nil {
				return err
			}
			e.Setenv("HOME", home)
			e.Setenv("NO_COLOR", "1")
			scripttest.PinEnv(e)
			z := filepath.Join(e.WorkDir, "zones")
			e.Setenv("RS", "--zone-root=shared=shared:"+z+"/shared")
			e.Setenv("RP", "--zone-root=personal=personal:"+z+"/personal")
			e.Setenv("RW", "--zone-root=work=work:"+z+"/work")
			e.Setenv("RA", "--zone-root=agent=agent:"+z+"/agent")
			e.Setenv("FAKES", fakes)
			e.Setenv("GOLDEN", golden)
			return nil
		},
		Cmds: scripttest.WithGolden(map[string]func(ts *testscript.TestScript, neg bool, args []string){
			// prefix a b: file b starts with the bytes of file a.
			"prefix": func(ts *testscript.TestScript, neg bool, args []string) {
				if len(args) != 2 {
					ts.Fatalf("usage: prefix orig new")
				}
				ok := strings.HasPrefix(ts.ReadFile(args[1]), ts.ReadFile(args[0]))
				if ok == neg {
					ts.Fatalf("prefix %s %s: %v", args[0], args[1], ok)
				}
			},
			// countdir n dir: dir has exactly n entries.
			"countdir": func(ts *testscript.TestScript, neg bool, args []string) {
				es, err := os.ReadDir(ts.MkAbs(args[1]))
				ts.Check(err)
				if (fmt.Sprint(len(es)) == args[0]) == neg {
					ts.Fatalf("%s has %d entries", args[1], len(es))
				}
			},
			// notmp dir: no piste temp files anywhere under dir.
			"notmp": func(ts *testscript.TestScript, neg bool, args []string) {
				filepath.Walk(ts.MkAbs(args[0]), func(p string, fi os.FileInfo, err error) error {
					if err == nil && (strings.HasSuffix(p, ".tmp") || strings.Contains(p, ".piste-")) {
						ts.Fatalf("temp file left: %s", p)
					}
					return nil
				})
			},
			// replace old new file: replace the first occurrence in a file.
			"replace": func(ts *testscript.TestScript, neg bool, args []string) {
				if len(args) != 3 {
					ts.Fatalf("usage: replace old new file")
				}
				s := ts.ReadFile(args[2])
				if !strings.Contains(s, args[0]) {
					ts.Fatalf("%q not in %s", args[0], args[2])
				}
				ts.Check(os.WriteFile(ts.MkAbs(args[2]), []byte(strings.Replace(s, args[0], args[1], 1)), 0o644))
			},
			// snaptree dir file: write a listing of dir (every path, file
			// hash and mode, link target; .git skipped) to file, for cmp.
			"snaptree": func(ts *testscript.TestScript, neg bool, args []string) {
				if len(args) != 2 {
					ts.Fatalf("usage: snaptree dir file")
				}
				root := ts.MkAbs(args[0])
				var lines []string
				ts.Check(filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					rel, _ := filepath.Rel(root, p)
					switch {
					case fi.IsDir() && fi.Name() == ".git":
						return filepath.SkipDir
					case fi.Mode()&os.ModeSymlink != 0:
						t, _ := os.Readlink(p)
						lines = append(lines, "L "+rel+" "+t)
					case fi.IsDir():
						lines = append(lines, "D "+rel)
					default:
						b, _ := os.ReadFile(p)
						lines = append(lines, fmt.Sprintf("F %s %o %x", rel, fi.Mode().Perm(), sha256.Sum256(b)))
					}
					return nil
				}))
				ts.Check(os.WriteFile(ts.MkAbs(args[1]), []byte(strings.Join(lines, "\n")+"\n"), 0o644))
			},
		}),
		Condition: func(cond string) (bool, error) {
			switch cond {
			case "fakes":
				return fakes != "", nil
			case "store":
				_, err := os.Stat("/nix/store")
				return err == nil, nil
			case "tint":
				return os.Getenv("PISTE_TINT") == "1", nil
			}
			return false, fmt.Errorf("unknown condition %q", cond)
		},
	})
}
