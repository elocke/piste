package cli

// When a switch is checked, and how it is undone (see switchcheck.go).

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/platform"
)

// checkTailLines: how much of a failed check's output to show.
const checkTailLines = 5

// runsBaseline: check the generation before the switch. Only a pass makes
// the switch undoable, so it never undoes into something no better and
// can't loop; a first switch has nothing to go back to.
func runsBaseline(noUndo, haveBefore bool) bool { return !noUndo && haveBefore }

// pisteNotInstalled: the baseline failed only because the login shell
// can't find piste (127), and this piste isn't on PATH either: a first run
// through nix run (piste setup, adopting a machine that already has
// generations). There's no piste yet to protect, so it's a note, not the
// can't-undo warning.
func pisteNotInstalled(baseline shellCheck, onPath bool) bool {
	return !onPath && baseline.err == nil && !baseline.timedOut && baseline.code == 127
}

// runsCheck: check the generation after the switch. Only when it is
// undoable and moved the profile: nix-env makes no new generation when a
// build equals the newest, so an unchanged link means nothing new was
// activated. A failed activation that moved the profile is checked too.
func runsCheck(undoable bool, before, after generation, haveAfter bool) bool {
	return undoable && haveAfter && after != before
}

// undoStep is one command of an undo, and the variables it adds.
type undoStep struct {
	argv, env []string
}

// String is the step as a command to run by hand.
func (s undoStep) String() string {
	parts := make([]string, 0, len(s.env)+1)
	for _, kv := range s.env {
		k, v, _ := strings.Cut(kv, "=")
		parts = append(parts, k+"="+execx.Quote([]string{v}))
	}
	return strings.Join(append(parts, execx.Quote(s.argv)), " ")
}

// undoSteps put back the generation before, exactly (not --rollback: it
// takes the next-lower number, which after an earlier rollback isn't where
// you were).
//
// On a Mac, darwin-rebuild's own --switch-generation moves the profile and
// runs the old system's activate, home-manager included. It's the old
// generation's darwin-rebuild, by store path: the new one may have broken
// the one on PATH.
//
// On a NixOS host, what `nixos-rebuild switch --rollback` runs, with the
// number: nix-env moves the system profile, then the old system's own
// switch-to-configuration activates it, home-manager included; both as
// root.
//
// For a home, what `home-manager switch --rollback` runs, with the number:
// nix-env moves the profile, then the old generation activates with
// --driver-version 1 (without it, the activation sets the profile itself,
// as a new generation). backupExt is the switch's -b.
func undoSteps(kind Kind, profile string, before generation, backupExt string) []undoStep {
	n := strconv.Itoa(before.num)
	switch kind {
	case KindDarwin:
		return []undoStep{{argv: []string{"sudo", before.path + "/sw/bin/darwin-rebuild", "--switch-generation", n}}}
	case KindNixOS:
		return []undoStep{
			{argv: []string{"sudo", "nix-env", "--profile", profile, "--switch-generation", n}},
			{argv: []string{"sudo", before.path + "/bin/switch-to-configuration", "switch"}},
		}
	}
	return []undoStep{
		{argv: []string{"nix-env", "--profile", profile, "--switch-generation", n}},
		{argv: []string{before.path + "/activate", "--driver-version", "1"}, env: []string{"HOME_MANAGER_BACKUP_EXT=" + backupExt}},
	}
}

// checkLogin runs the check for this machine's user, with the shell a new
// login would start now.
func (a *App) checkLogin(p platform.Platform) shellCheck {
	sh := a.loginShell(p)
	return checkShell(sh, checkEnv(p.HomeDir, p.User, sh))
}

// undoSwitch puts back before, after the check failed on after, says how it
// went, and reports whether it's undone: only when every step exits 0 and
// the profile is on before again. Otherwise it prints the steps to run by
// hand.
func (a *App) undoSwitch(profile string, before, after generation, steps []undoStep) bool {
	a.info(fmt.Sprintf("Undoing the switch: back to generation %d...", before.num))
	why := ""
	for _, s := range steps {
		if rc := a.runUndoStep(s); rc != 0 {
			why = fmt.Sprintf("exit code %d", rc)
			break
		}
	}
	if why == "" {
		switch now, ok := activeGeneration(profile); {
		case !ok:
			why = "no generation is active"
		case now != before:
			why = fmt.Sprintf("generation %d is active", now.num)
		}
	}
	if why != "" {
		a.errorf(fmt.Sprintf("The undo failed (%s). To put generation %d back by hand:", why, before.num))
		for _, s := range steps {
			a.errorf("  " + s.String())
		}
		return false
	}
	a.warn(fmt.Sprintf("Undone: generation %d is active again. %d stays in the store.", before.num, after.num))
	a.warn("  To see what it changed:  " + a.piste() + " gendiff")
	a.warn("  To keep it anyway:       " + a.piste() + " switch --no-undo")
	return true
}

// runUndoStep runs one undo step with the terminal inherited (sudo may ask for
// the password again) and returns its exit code.
func (a *App) runUndoStep(s undoStep) int {
	c := exec.Command(s.argv[0], s.argv[1:]...)
	c.Env = append(os.Environ(), s.env...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, a.Stdout, a.Stderr
	err := c.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee) && ee.ExitCode() > 0:
		return ee.ExitCode()
	case errors.As(err, &ee):
		fmt.Fprintf(a.Stderr, "piste: %s: %v\n", s.argv[0], err)
		return 1
	}
	fmt.Fprintf(a.Stderr, "piste: %s: %v\n", s.argv[0], err)
	return 127
}
