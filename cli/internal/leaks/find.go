package leaks

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/workspace"
)

// FindLists is the piste checkout cwd belongs to and its marker lists, every
// overlays/*/leak-markers*.txt, sorted. In a linked worktree (of the engine,
// or of an overlay) it is the main checkout's: an engine worktree has its
// own .piste, but no overlays.
func FindLists(cwd string) (root string, paths []string) {
	start := cwd
	if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output(); err == nil {
		if main := platform.WorktreeMainCheckout(strings.TrimSpace(string(out))); main != "" {
			start = main
		}
	}
	root, ok := workspace.FindCheckout(start)
	if !ok {
		return "", nil
	}
	matches, _ := filepath.Glob(filepath.Join(root, "overlays", "*", "leak-markers*.txt"))
	for _, p := range matches {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return root, paths
}
