package release

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elocke/piste/cli/internal/platform"
)

// The release check after a successful `piste switch`: one line when a newer
// release is out, at most one network check a day, never a prompt, silent
// on any error.

const (
	fresh   = 24 * time.Hour  // a check younger than this is reused
	timeout = 3 * time.Second // the longest the check waits
)

// Probe is what the check needs: the release you're on, the cache, the
// clock, and the remote's tags.
type Probe struct {
	Yours, CacheFile string
	Now              time.Time
	Tags             func() ([]string, error)
}

// cache records the last check, whatever its outcome (Newest "" when it
// failed), so being offline costs one wait a day.
type cache struct {
	Checked int64  `json:"checked"`
	Newest  string `json:"newest"`
}

// Notice is the line to print, "" when there's nothing to say: no release
// you're on, or none newer than it.
func Notice(p Probe) string {
	if _, ok := ParseVersion(p.Yours); !ok {
		return ""
	}
	newest, ok := cached(p.CacheFile, p.Now)
	if !ok {
		newest = ""
		if tags, err := p.Tags(); err == nil {
			newest = Latest(tags)
		}
		if b, err := json.Marshal(cache{p.Now.Unix(), newest}); err == nil && os.MkdirAll(filepath.Dir(p.CacheFile), 0o755) == nil {
			_ = os.WriteFile(p.CacheFile, b, 0o644)
		}
	}
	if !Newer(newest, p.Yours) {
		return ""
	}
	return fmt.Sprintf("piste %s is out (you're on %s): piste update shows what changed", newest, p.Yours)
}

// cached is the cache's newest release when its check is under a day old.
func cached(file string, now time.Time) (string, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	var c cache
	if json.Unmarshal(b, &c) != nil {
		return "", false
	}
	age := now.Sub(time.Unix(c.Checked, 0))
	return c.Newest, age >= 0 && age < fresh
}

// InSwitchUnit: this process runs in a piste-switch-* unit, the detached
// `piste switch`, whose outer piste prints the notice instead. The
// cgroup v2 ("0::") path names the unit; darwin has no such file.
func InSwitchUnit(cgroupFile string) bool {
	b, err := os.ReadFile(cgroupFile)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		path, ok := strings.CutPrefix(l, "0::")
		if !ok {
			continue
		}
		for _, c := range strings.Split(path, "/") {
			if strings.HasPrefix(c, "piste-switch-") {
				return true
			}
		}
	}
	return false
}

// Check is the notice for a switch of overlayDir in the checkout at root.
// Yours is the checkout's release when it's detached on one; on a branch
// it's the overlay's pin, even when the branch's tip is a release commit
// (main right after a release), as piste update reads a branch first.
// Skipped under CI, outside a checkout, and inside a piste-switch-* unit.
func Check(root, overlayDir string) string {
	if os.Getenv("CI") != "" || root == "" || InSwitchUnit(platform.CgroupFile) {
		return ""
	}
	yours := ""
	if exec.Command("git", "-C", root, "symbolic-ref", "-q", "HEAD").Run() != nil {
		yours = HeadRelease(root)
	}
	if yours == "" && overlayDir != "" {
		if b, err := os.ReadFile(filepath.Join(overlayDir, "flake.nix")); err == nil {
			if url, ok := ReadPin(string(b)); ok {
				yours = PinTag(url)
			}
		}
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return Notice(Probe{
		Yours:     yours,
		CacheFile: filepath.Join(dir, "piste", "release.json"),
		Now:       time.Now(),
		Tags:      func() ([]string, error) { return remoteTags(root) },
	})
}

// remoteTags are the tags at root's origin: git ls-remote with no prompt (no
// terminal, ssh in batch mode, no stdin), given up on after timeout.
func remoteTags(root string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "ls-remote", "--tags", "--refs", "origin")
	c.Dir = root
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND="+sshCommand(root)+" -o BatchMode=yes")
	c.WaitDelay = 500 * time.Millisecond // an ssh child holding the pipe doesn't hold us
	out, err := c.Output()
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, l := range strings.Split(string(out), "\n") {
		if _, ref, ok := strings.Cut(l, "\trefs/tags/"); ok {
			tags = append(tags, strings.TrimSpace(ref))
		}
	}
	return tags, nil
}

// sshCommand is the ssh git would run: $GIT_SSH_COMMAND, else
// core.sshCommand, else ssh.
func sshCommand(root string) string {
	if c := os.Getenv("GIT_SSH_COMMAND"); c != "" {
		return c
	}
	if out, err := exec.Command("git", "-C", root, "config", "--get", "core.sshCommand").Output(); err == nil {
		if c := strings.TrimSpace(string(out)); c != "" {
			return c
		}
	}
	return "ssh"
}
