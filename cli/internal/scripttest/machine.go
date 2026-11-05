package scripttest

import (
	"os"
	"path/filepath"
	"time"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/elocke/piste/cli/internal/agents/userscope"
	"github.com/elocke/piste/cli/internal/flake"
	"github.com/elocke/piste/cli/internal/platform"
)

// The machine a test binary's piste sees, whatever machine runs it.
const (
	TestHost  = "testhost"
	TestArch  = "amd64"
	TestToday = "2026-01-01"
)

// TestNow is the clock flake input ages are measured against: after every
// fixture's lastModified, so ages are positive and the same on any day.
var TestNow = time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)

// PinMachine fixes what piste would read from the machine (hostname, CPU
// arch, the profiles a switch moves, its cgroup, today's date, the clock),
// so goldens hold the same text on every machine, day and sandbox, and no
// script reads the real profiles or starts a real unit. Call it in a test
// binary's "piste" entry, before cli.Main. The OS is not pinned: Linux and
// darwin behave differently, which <name>@darwin goldens record.
//
// The profiles and the cgroup file live under $WORK/.machine (a script
// makes them there): profiles/system, profiles/per-user and cgroup. Without
// $WORK, under /nonexistent/piste-test.
func PinMachine() {
	platform.Hostname = func() (string, error) { return TestHost, nil }
	platform.Arch = TestArch
	root := "/nonexistent/piste-test"
	if w := os.Getenv("WORK"); w != "" {
		root = filepath.Join(w, ".machine")
	}
	platform.SystemProfile = filepath.Join(root, "profiles", "system")
	platform.UserProfiles = filepath.Join(root, "profiles", "per-user")
	platform.CgroupFile = filepath.Join(root, "cgroup")
	userscope.Today = func() string { return TestToday }
	flake.Now = func() time.Time { return TestNow }
}

// PinEnv sets what a script's commands read from the environment the same
// on every machine: UTC, no system-wide git config (e.g. a NixOS
// init.defaultBranch), the C locale. Call it from Setup.
func PinEnv(e *testscript.Env) {
	e.Setenv("TZ", "UTC")
	e.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	e.Setenv("LC_ALL", "C")
}
