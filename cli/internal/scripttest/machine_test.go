package scripttest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elocke/piste/cli/internal/platform"
)

// PinMachine points the profiles, the cgroup file, the NixOS and container
// markers and a NixOS machine's /etc/nixos piste reads under $WORK/.machine,
// and /dev/disk/by-uuid under $WORK/dev, so a script builds its own and
// never reads the real ones.
func TestPinMachine(t *testing.T) {
	host, sys, users, cg, marker, ctr, etc, uuid := platform.Hostname, platform.SystemProfile, platform.UserProfiles, platform.CgroupFile, platform.NixOSMarker, platform.ContainerMarker, platform.MachineEtc, platform.DiskByUUID
	t.Cleanup(func() {
		platform.Hostname, platform.SystemProfile, platform.UserProfiles, platform.CgroupFile, platform.NixOSMarker, platform.ContainerMarker, platform.MachineEtc, platform.DiskByUUID = host, sys, users, cg, marker, ctr, etc, uuid
	})

	work := t.TempDir()
	t.Setenv("WORK", work)
	PinMachine()
	for _, c := range [][2]string{
		{platform.SystemProfile, filepath.Join(work, ".machine/profiles/system")},
		{platform.UserProfiles, filepath.Join(work, ".machine/profiles/per-user")},
		{platform.CgroupFile, filepath.Join(work, ".machine/cgroup")},
		{platform.NixOSMarker, filepath.Join(work, ".machine/NIXOS")},
		{platform.ContainerMarker, filepath.Join(work, ".machine/container")},
		{platform.MachineEtc, filepath.Join(work, ".machine/etc-nixos")},
		{platform.DiskByUUID, filepath.Join(work, "dev/disk/by-uuid")},
	} {
		if c[0] != c[1] {
			t.Errorf("with $WORK: got %s, want %s", c[0], c[1])
		}
	}
	// The hostname is testhost, or what $WORK/.machine/hostname says (a
	// fresh NixOS machine's nixos), read each time it is asked for.
	if h, err := platform.Hostname(); err != nil || h != TestHost {
		t.Errorf("hostname = %q, %v; want %s", h, err, TestHost)
	}
	if err := os.MkdirAll(filepath.Join(work, ".machine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".machine/hostname"), []byte("nixos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h, err := platform.Hostname(); err != nil || h != "nixos" {
		t.Errorf("hostname with .machine/hostname = %q, %v; want nixos", h, err)
	}

	t.Setenv("WORK", "")
	PinMachine()
	for _, c := range [][2]string{
		{platform.SystemProfile, "/nonexistent/piste-test/profiles/system"},
		{platform.UserProfiles, "/nonexistent/piste-test/profiles/per-user"},
		{platform.CgroupFile, "/nonexistent/piste-test/cgroup"},
		{platform.NixOSMarker, "/nonexistent/piste-test/NIXOS"},
		{platform.ContainerMarker, "/nonexistent/piste-test/container"},
		{platform.MachineEtc, "/nonexistent/piste-test/etc-nixos"},
		{platform.DiskByUUID, "/nonexistent/piste-test/dev/disk/by-uuid"},
	} {
		if c[0] != c[1] {
			t.Errorf("without $WORK: got %s, want %s", c[0], c[1])
		}
	}
	if h, err := platform.Hostname(); err != nil || h != TestHost {
		t.Errorf("hostname without $WORK = %q, %v; want %s", h, err, TestHost)
	}
}
