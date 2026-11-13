package scripttest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/elocke/piste/cli/internal/agents/userscope"
	"github.com/elocke/piste/cli/internal/flake"
	"github.com/elocke/piste/cli/internal/platform"
)

// The machine a test binary's piste sees, whatever machine runs it. Its CPU
// is amd64 off a Mac and arm64 on one: piste takes no Intel Mac
// (x86_64-darwin), so an amd64 Mac would be refused by init and setup.
const (
	TestHost  = "testhost"
	TestToday = "2026-01-01"
)

// TestArch is the CPU the test binaries pin (platform.Arch).
var TestArch = testArch(runtime.GOOS)

func testArch(goos string) string {
	if goos == "darwin" {
		return "arm64"
	}
	return "amd64"
}

// TestNow is the clock flake input ages are measured against: after every
// fixture's lastModified, so ages are positive and the same on any day.
var TestNow = time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)

// PinMachine fixes what piste would read from the machine (hostname, CPU
// arch, the profiles a switch moves, its cgroup, the NixOS and container
// markers, its /etc/nixos, today's date, the clock), so goldens hold the
// same text on every machine, day and sandbox, and no script reads the real
// profiles or starts a real unit. Call it in a test binary's "piste" entry,
// before cli.Main. The OS is not pinned: Linux and darwin behave
// differently, which <name>@darwin goldens record.
//
// The profiles, the cgroup file and the markers live under $WORK/.machine
// (a script makes them there): profiles/system, profiles/per-user, cgroup,
// NIXOS (a script that makes it runs on NixOS), container (in a container)
// and etc-nixos (a NixOS machine's /etc/nixos). The hostname is TestHost,
// or the one line of $WORK/.machine/hostname while a script has one (a
// fresh NixOS machine's nixos). The disks are where the scripts make them,
// under $WORK/dev: /dev/disk/by-uuid is $WORK/dev/disk/by-uuid. Without
// $WORK, under /nonexistent/piste-test.
func PinMachine() {
	root, dev := "/nonexistent/piste-test", "/nonexistent/piste-test/dev"
	if w := os.Getenv("WORK"); w != "" {
		root, dev = filepath.Join(w, ".machine"), filepath.Join(w, "dev")
	}
	platform.Hostname = func() (string, error) {
		if b, err := os.ReadFile(filepath.Join(root, "hostname")); err == nil {
			return strings.TrimSpace(string(b)), nil
		}
		return TestHost, nil
	}
	platform.Arch = TestArch
	platform.SystemProfile = filepath.Join(root, "profiles", "system")
	platform.UserProfiles = filepath.Join(root, "profiles", "per-user")
	platform.CgroupFile = filepath.Join(root, "cgroup")
	platform.NixOSMarker = filepath.Join(root, "NIXOS")
	platform.ContainerMarker = filepath.Join(root, "container")
	platform.MachineEtc = filepath.Join(root, "etc-nixos")
	platform.DiskByUUID = filepath.Join(dev, "disk", "by-uuid")
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
