package scripttest

import (
	"path/filepath"
	"testing"

	"github.com/elocke/piste/cli/internal/platform"
)

// PinMachine points the profiles and the cgroup file piste reads under
// $WORK/.machine, so a script builds its own and never reads the real ones.
func TestPinMachine(t *testing.T) {
	sys, users, cg := platform.SystemProfile, platform.UserProfiles, platform.CgroupFile
	t.Cleanup(func() { platform.SystemProfile, platform.UserProfiles, platform.CgroupFile = sys, users, cg })

	work := t.TempDir()
	t.Setenv("WORK", work)
	PinMachine()
	for _, c := range [][2]string{
		{platform.SystemProfile, filepath.Join(work, ".machine/profiles/system")},
		{platform.UserProfiles, filepath.Join(work, ".machine/profiles/per-user")},
		{platform.CgroupFile, filepath.Join(work, ".machine/cgroup")},
	} {
		if c[0] != c[1] {
			t.Errorf("with $WORK: got %s, want %s", c[0], c[1])
		}
	}

	t.Setenv("WORK", "")
	PinMachine()
	for _, c := range [][2]string{
		{platform.SystemProfile, "/nonexistent/piste-test/profiles/system"},
		{platform.UserProfiles, "/nonexistent/piste-test/profiles/per-user"},
		{platform.CgroupFile, "/nonexistent/piste-test/cgroup"},
	} {
		if c[0] != c[1] {
			t.Errorf("without $WORK: got %s, want %s", c[0], c[1])
		}
	}
}
