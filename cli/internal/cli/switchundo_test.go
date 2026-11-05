package cli

import (
	"slices"
	"testing"
)

// When a switch is checked: a baseline only with a generation to go back to
// and no --no-undo; a check after only when the baseline passed and the
// switch moved the profile.
func TestSwitchDecision(t *testing.T) {
	g41 := generation{41, "/nix/store/aaa-41"}
	g42 := generation{42, "/nix/store/bbb-42"}
	for _, c := range []struct {
		name                 string
		noUndo, haveBefore   bool
		baselinePasses       bool
		after                generation
		haveAfter            bool
		wantBaseline, wantCk bool
	}{
		{"first switch", false, false, true, g42, true, false, false},
		{"--no-undo", true, true, true, g42, true, false, false},
		{"baseline fails", false, true, false, g42, true, true, false},
		{"unchanged", false, true, true, g41, true, true, false},
		{"same number, new path", false, true, true, generation{41, "/nix/store/ccc-41"}, true, true, true},
		{"changed", false, true, true, g42, true, true, true},
		{"no generation after", false, true, true, generation{}, false, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			baseline := runsBaseline(c.noUndo, c.haveBefore)
			if baseline != c.wantBaseline {
				t.Errorf("baseline: got %v, want %v", baseline, c.wantBaseline)
			}
			undoable := baseline && c.baselinePasses
			if got := runsCheck(undoable, g41, c.after, c.haveAfter); got != c.wantCk {
				t.Errorf("check: got %v, want %v", got, c.wantCk)
			}
		})
	}
}

// The undo goes to the generation before exactly, with the upstream tools
// (never --rollback): the old generation's own darwin-rebuild on a Mac;
// home-manager's rollback steps with the number on Linux.
func TestUndoSteps(t *testing.T) {
	mac := undoSteps(true, "/nix/var/nix/profiles/system", generation{41, "/nix/store/aaa-darwin-system"}, "backup-1761900000")
	if len(mac) != 1 || !slices.Equal(mac[0].argv, []string{"sudo", "/nix/store/aaa-darwin-system/sw/bin/darwin-rebuild", "--switch-generation", "41"}) || mac[0].env != nil {
		t.Errorf("mac: got %+v", mac)
	}

	prof := "/home/user/.local/state/nix/profiles/home-manager"
	linux := undoSteps(false, prof, generation{41, "/nix/store/bbb-home-manager-generation"}, "backup-1761900000")
	if len(linux) != 2 {
		t.Fatalf("linux: got %+v", linux)
	}
	if !slices.Equal(linux[0].argv, []string{"nix-env", "--profile", prof, "--switch-generation", "41"}) || linux[0].env != nil {
		t.Errorf("linux step 1: got %+v", linux[0])
	}
	if !slices.Equal(linux[1].argv, []string{"/nix/store/bbb-home-manager-generation/activate", "--driver-version", "1"}) ||
		!slices.Equal(linux[1].env, []string{"HOME_MANAGER_BACKUP_EXT=backup-1761900000"}) {
		t.Errorf("linux step 2: got %+v", linux[1])
	}
	if got, want := linux[1].String(), "HOME_MANAGER_BACKUP_EXT=backup-1761900000 /nix/store/bbb-home-manager-generation/activate --driver-version 1"; got != want {
		t.Errorf("hand command: got %q, want %q", got, want)
	}
	if got, want := linux[0].String(), "nix-env --profile "+prof+" --switch-generation 41"; got != want {
		t.Errorf("hand command: got %q, want %q", got, want)
	}
}

// A baseline that fails only because the shell can't find piste, run by a
// piste that isn't on PATH either (a first run through nix run: piste setup,
// adopting a machine), isn't a warning: there's no piste yet to protect.
func TestPisteNotInstalled(t *testing.T) {
	for _, c := range []struct {
		name   string
		check  shellCheck
		onPath bool
		want   bool
	}{
		{"command not found, not on PATH", shellCheck{code: 127}, false, true},
		{"command not found, on PATH", shellCheck{code: 127}, true, false},
		{"another failure, not on PATH", shellCheck{code: 3}, false, false},
		{"timed out, not on PATH", shellCheck{code: -1, timedOut: true}, false, false},
	} {
		if got := pisteNotInstalled(c.check, c.onPath); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
