// Package scripttest holds testscript commands shared by the piste test
// suites. It is imported only by tests, so none of it reaches the binary.
//
// The golden commands compare against frozen outputs. In a
// golden the work dir is written as $WORK. A golden that differs by OS has
// a variant beside it, <name>@darwin or <name>@linux, used on that OS in
// place of <name>.
//
// `go test <pkgs> -update` writes the goldens instead of comparing (review
// the diff before committing). Linux is canonical: it writes <name> and
// drops a stale <name>@linux. darwin writes <name>@darwin only where its
// output differs from <name>, and drops it where it no longer does. A
// golden with no output is removed. Run it on Linux first, then the Mac.
package scripttest

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/rogpeppe/go-internal/testscript"
)

type cmd = func(ts *testscript.TestScript, neg bool, args []string)

// GoldenCmds returns cmpwork and cmpworkdir:
//
//	cmpwork stdout|stderr|file golden   got equals the golden file
//	cmpworkdir dir golden               dir holds exactly golden's file
//	                                    names, each equal to its golden
func GoldenCmds() map[string]cmd {
	return map[string]cmd{"cmpwork": cmpworkCmd, "cmpworkdir": cmpworkdirCmd}
}

// WithGolden adds GoldenCmds to cmds (a suite's own commands) and
// returns it.
func WithGolden(cmds map[string]cmd) map[string]cmd {
	if cmds == nil {
		cmds = map[string]cmd{}
	}
	for name, c := range GoldenCmds() {
		cmds[name] = c
	}
	return cmds
}

var oses = []string{"darwin", "linux"}

var update = flag.Bool("update", false, "rewrite the golden files instead of comparing with them")

func updating() bool { return *update }

// workText is s with the work dir (and, where it differs, its resolved
// form, e.g. /private/var/… on macOS) written as $WORK. The machine itself
// (hostname, arch, date) is pinned by PinMachine, not rewritten here.
func workText(ts *testscript.TestScript, s string) string {
	work := ts.Getenv("WORK")
	if real, err := filepath.EvalSymlinks(work); err == nil && real != work {
		s = strings.ReplaceAll(s, real, "$WORK")
	}
	return strings.ReplaceAll(s, work, "$WORK")
}

// goldenFor is the file holding the golden for path on this OS.
func goldenFor(path string) string { return variantFor(path, runtime.GOOS, exists) }

// variantFor is path's variant for goos if has reports one, else path.
func variantFor(path, goos string, has func(string) bool) string {
	if v := path + "@" + goos; has(v) {
		return v
	}
	return path
}

// expectedNames are the output names a golden dir holding files expects
// on goos: each file with any @<os> suffix removed, skipping a name whose
// only golden is another OS's variant. has reports whether a file is in
// the dir. Sorted.
func expectedNames(files []string, goos string, has func(string) bool) []string {
	seen := map[string]bool{}
	var names []string
	for _, n := range files {
		for _, o := range oses {
			if base, ok := strings.CutSuffix(n, "@"+o); ok {
				if o != goos && !has(base) && !has(base+"@"+goos) {
					n = ""
				} else {
					n = base
				}
				break
			}
		}
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// check compares got (after workText) with the golden at path, or writes
// it there when updating.
func check(ts *testscript.TestScript, name, got, path string) {
	got = workText(ts, got)
	if updating() {
		write, remove := updatePlan(path, got, runtime.GOOS, func(p string) (string, bool) {
			b, err := os.ReadFile(p)
			return string(b), err == nil
		})
		if write != "" {
			ts.Check(os.WriteFile(write, []byte(got), 0o644))
		}
		for _, p := range remove {
			ts.Check(os.Remove(p))
		}
		return
	}
	want, err := os.ReadFile(goldenFor(path))
	ts.Check(err)
	if line, g, w, differs := firstDiff(got, string(want)); differs {
		ts.Fatalf("%s differs from golden %s at line %d:\n  got:  %s\n  want: %s",
			name, filepath.Base(goldenFor(path)), line, g, w)
	}
}

// updatePlan is what -update does for the golden at path, given this
// OS's output got: the file to write ("" for none) and the existing
// files to remove. read returns a file's content and whether it exists.
func updatePlan(path, got, goos string, read func(string) (string, bool)) (write string, remove []string) {
	if goos != "darwin" { // canonical
		if _, ok := read(path + "@" + goos); ok {
			remove = append(remove, path+"@"+goos)
		}
		return path, remove
	}
	variant := path + "@" + goos
	if base, ok := read(path); ok && base == got {
		if _, ok := read(variant); ok {
			remove = append(remove, variant)
		}
		return "", remove
	}
	return variant, nil
}

// staleGoldens are the files of a golden dir that -update on goos
// removes because no output has their name: on Linux a base and its
// @linux variant, on darwin a @darwin variant.
func staleGoldens(files []string, outputs map[string]bool, goos string) []string {
	var stale []string
	for _, f := range files {
		base, variant := f, ""
		for _, o := range oses {
			if b, ok := strings.CutSuffix(f, "@"+o); ok {
				base, variant = b, o
				break
			}
		}
		if outputs[base] {
			continue
		}
		if variant == goos || (variant == "" && goos != "darwin") {
			stale = append(stale, f)
		}
	}
	return stale
}

// firstDiff is the first line (1-based) where got and want differ, each
// side quoted or "(end)" past its last line; differs is false only when
// they are equal.
func firstDiff(got, want string) (line int, g, w string, differs bool) {
	if got == want {
		return 0, "", "", false
	}
	gs, ws := strings.Split(got, "\n"), strings.Split(want, "\n")
	at := func(xs []string, i int) string {
		if i < len(xs) {
			return fmt.Sprintf("%q", xs[i])
		}
		return "(end)"
	}
	for i := 0; ; i++ {
		if g, w := at(gs, i), at(ws, i); g != w {
			return i + 1, g, w, true
		}
	}
}

func cmpworkCmd(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: cmpwork stdout|stderr|file golden")
	}
	var got string
	switch args[0] {
	case "stdout", "stderr":
		got = ts.ReadFile(args[0])
	default:
		b, err := os.ReadFile(ts.MkAbs(args[0]))
		ts.Check(err)
		got = string(b)
	}
	check(ts, args[0], got, ts.MkAbs(args[1]))
}

// goldenNames are the names a golden dir expects on this OS: its files
// with any @<os> suffix removed, skipping a name whose only golden is
// another OS's variant.
func goldenNames(ts *testscript.TestScript, dir string) []string {
	es, err := os.ReadDir(dir)
	if os.IsNotExist(err) && updating() {
		return nil
	}
	ts.Check(err)
	var files []string
	for _, e := range es {
		files = append(files, e.Name())
	}
	return expectedNames(files, runtime.GOOS, func(n string) bool { return exists(filepath.Join(dir, n)) })
}

func cmpworkdirCmd(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: cmpworkdir dir golden")
	}
	dir, gold := ts.MkAbs(args[0]), ts.MkAbs(args[1])
	es, err := os.ReadDir(dir)
	ts.Check(err)
	var got []string
	for _, e := range es {
		got = append(got, e.Name())
	}
	if updating() {
		ts.Check(os.MkdirAll(gold, 0o755))
	} else if want := goldenNames(ts, gold); strings.Join(got, "\n") != strings.Join(want, "\n") {
		ts.Fatalf("%s files differ from golden:\n  got:  %v\n  want: %v", args[0], got, want)
	}
	for _, n := range got {
		b, err := os.ReadFile(filepath.Join(dir, n))
		ts.Check(err)
		check(ts, filepath.Join(args[0], n), string(b), filepath.Join(gold, n))
	}
	if updating() {
		outputs := map[string]bool{}
		for _, n := range got {
			outputs[n] = true
		}
		es, err := os.ReadDir(gold)
		ts.Check(err)
		var files []string
		for _, e := range es {
			files = append(files, e.Name())
		}
		for _, f := range staleGoldens(files, outputs, runtime.GOOS) {
			ts.Check(os.Remove(filepath.Join(gold, f)))
		}
	}
}
