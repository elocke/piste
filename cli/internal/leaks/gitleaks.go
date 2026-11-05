package leaks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// gitleaks exits with this on findings. Its fatal errors exit 1, its default
// findings code, so 3 tells them apart.
const gitleaksFound = 3

type finding struct {
	RuleID, File, Commit string
	StartLine            int
}

// gitleaksConfig is the config piste runs gitleaks with: its default rules,
// whatever a .gitleaks.toml in the repo or tree, or GITLEAKS_CONFIG(_TOML),
// would say (one without useDefault drops every rule).
const gitleaksConfig = "[extend]\nuseDefault = true\n"

// gitleaksLogOpts pin gitleaks' own `git log -p -U0`, as gitLog pins piste's:
// user config can't colour it (gitleaks reads coloured output as nothing),
// narrow it to the directory it runs in, convert or hide a file's text
// (gitattributes' binary and -diff, text conversion), or change the header
// gitleaks takes the commit from.
var gitleaksLogOpts = []string{"--no-color", "--no-ext-diff", "--no-textconv", "--no-show-signature",
	"--no-relative", "--text", "--src-prefix=a/", "--dst-prefix=b/",
	"--format=medium", "--date=default", "--no-abbrev-commit", "--no-decorate"}

// GitleaksRange is gitleaks over the commits of revs in dir's repo. Run
// Commits first: gitleaks exits 0 with no findings when its own git log fails.
// (gitleaks splits --log-opts at spaces; revision arguments have none.)
func GitleaksRange(dir string, revs []string) ([]Hit, error) {
	return gitleaks(dir, "git", "--log-opts", strings.Join(slices.Concat(revs, gitleaksLogOpts), " "), ".")
}

// GitleaksDir is gitleaks over the files under dir.
func GitleaksDir(dir string) ([]Hit, error) {
	return gitleaks("", "dir", dir)
}

// gitleaks runs `gitleaks <mode> [args] <flags> <source>` in dir, the report
// as JSON on stdout. Missing, failing or unreadable is an error, never clean.
// It runs with piste's config, and its .gitleaksignore is an empty directory
// of piste's, not the one where piste runs.
func gitleaks(dir, mode string, args ...string) ([]Hit, error) {
	source := args[len(args)-1]
	// gitleaks always obeys a .gitleaksignore at its source, which would
	// skip the findings it lists: refused (mark a line gitleaks:allow).
	if p := filepath.Join(dir, source, ".gitleaksignore"); isFile(p) {
		return nil, fmt.Errorf("%s: gitleaks would skip what it lists; take it out and mark the line gitleaks:allow instead", p)
	}
	exe, err := exec.LookPath("gitleaks")
	if err != nil {
		return nil, errors.New("gitleaks not found on PATH")
	}
	tmp, err := os.MkdirTemp("", "piste-gitleaks-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	config := filepath.Join(tmp, "gitleaks.toml")
	if err := os.WriteFile(config, []byte(gitleaksConfig), 0o644); err != nil {
		return nil, err
	}
	argv := append([]string{mode}, args[:len(args)-1]...)
	argv = append(argv, "--no-banner", "--redact", "--log-level", "error",
		"-f", "json", "-r", "-", "--exit-code", strconv.Itoa(gitleaksFound),
		"--config", config, "--gitleaks-ignore-path", tmp, source)
	cmd := exec.Command(exe, argv...)
	cmd.Dir = dir
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return k == "GITLEAKS_CONFIG" || k == "GITLEAKS_CONFIG_TOML"
	})
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee) && ee.ExitCode() == gitleaksFound:
	case errors.As(err, &ee):
		return nil, fmt.Errorf("gitleaks failed (exit %d): %s", ee.ExitCode(), firstLine(strings.TrimSpace(stderr.String())))
	default:
		return nil, fmt.Errorf("gitleaks failed: %v", err)
	}
	var fs []finding
	if out := bytes.TrimSpace(stdout.Bytes()); len(out) > 0 {
		if err := json.Unmarshal(out, &fs); err != nil {
			return nil, fmt.Errorf("gitleaks' report not understood: %v", err)
		}
	}
	var hits []Hit
	for _, f := range fs {
		hits = append(hits, Hit{f.Commit[:min(7, len(f.Commit))], fmt.Sprintf("%s:%d", f.File, f.StartLine),
			"gitleaks: " + f.RuleID + " (redacted)"})
	}
	return hits, nil
}

// isFile is whether p is a file (following links), as gitleaks checks.
func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
