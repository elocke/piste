package cli

import (
	"context"
	"errors"
	"slices"

	"github.com/elocke/piste/cli/internal/flake"
	"github.com/elocke/piste/cli/internal/platform"
)

// Kind is the kind of host a configuration is: standalone home-manager
// (homeConfigurations."<user>@<host>", any Linux), nix-darwin
// (darwinConfigurations.<host>, a Mac) or NixOS (nixosConfigurations.<host>).
type Kind int

const (
	KindHome Kind = iota
	KindDarwin
	KindNixOS
)

func (k Kind) String() string {
	switch k {
	case KindDarwin:
		return "darwin"
	case KindNixOS:
		return "nixos"
	}
	return "home"
}

// target is what a command acts on: its kind and the configuration name.
// Host is the --host it was named by ("" for this machine's). Warning, when
// set, is a line for the command to show. Unmatched is set only on a NixOS
// machine without --host that is none of the overlay's NixOS hosts and has
// no home there either: those hosts' names, which switch's hostname guard
// refuses with.
type target struct {
	Kind      Kind
	Name      string
	Host      string
	Warning   string
	Unmatched []string
}

// resolveTarget is the target of a command run on p for host ("" for this
// machine): pickTarget over the overlay's configuration names. It evaluates
// only the names pickTarget needs: none on a Mac, nixosConfigurations for
// --host or on NixOS, homeConfigurations without --host.
//
// The names are read against the engine builds use (flake.ConfigurationNames
// with the engine override); off NixOS, without it when it can't be
// resolved, as before NixOS hosts. On a NixOS machine without --host, a
// flake with no nixosConfigurations is a home, as before NixOS hosts; an
// eval of them that fails, or an override that can't be resolved, is an
// error, never a silent home.
func (a *App) resolveTarget(p platform.Platform, host string) (target, error) {
	t, err := a.readTarget(p, host)
	var nu namesUnread
	if errors.As(err, &nu) {
		a.errorf(nu.msg)
	}
	return t, err
}

// namesUnread is readTarget's error on a NixOS machine, without --host,
// whose overlay's nixosConfigurations can't be evaluated: msg says so, with
// nix's error. It is an ExitError{1} (Unwrap), which a command that can do
// without the overlay (status, log: generationsKind) tells apart.
type namesUnread struct{ msg string }

func (e namesUnread) Error() string { return e.msg }
func (e namesUnread) Unwrap() error { return ExitError{1} }

// readTarget is resolveTarget without saying why the names can't be read.
func (a *App) readTarget(p platform.Platform, host string) (target, error) {
	if p.IsDarwin {
		return pickTarget(true, false, host, p.Hostname, p.User, nil, nil), nil
	}
	if err := a.checkNix(); err != nil {
		return target{}, err
	}
	// Every names eval reads the overlay as builds do, against the engine
	// they use: one pinned before NixOS hosts throws on a NixOS host's key.
	// An override that can't be resolved (a bad $PISTE_ENGINE, a checkout
	// that isn't a git repository) is an error only on NixOS, where the
	// names decide whether this machine is a whole system. Elsewhere the
	// names are read without it, as before NixOS hosts: a build says what's
	// wrong with the override, and status, log and rollback don't need it.
	o, err := platform.ResolveEngineOverride(p.FlakeDir, a.Env.Getenv, p.HomeDir)
	if err != nil {
		if p.IsNixOS {
			a.errorf(err.Error())
			return target{}, ExitError{1}
		}
		o = platform.EngineOverride{}
	}
	var nixosNames, homeNames []string
	if host != "" || p.IsNixOS {
		names, err := a.nixosNames(p, o)
		if err != nil && host == "" {
			why := err.Error()
			var ee *flake.EvalError
			if errors.As(err, &ee) {
				if t := stderrTail(ee.Stderr, 3); t != "" {
					why = t
				}
			}
			return target{}, namesUnread{"can't read nixosConfigurations from " + p.FlakeDir + ": " + why}
		}
		nixosNames = names
	}
	if host == "" {
		// None when the eval fails, as before: the build says why.
		homeNames, _ = flake.ConfigurationNames(context.Background(), a.runner(), p.FlakeDir, "homeConfigurations", o.Args())
	}
	return pickTarget(false, p.IsNixOS, host, p.Hostname, profileUser(p), nixosNames, homeNames), nil
}

// nixosNames are the overlay's nixosConfigurations names, evaluated with
// engine override o, as builds evaluate them: an overlay pinned to a
// release from before NixOS hosts reads its NixOS hosts as the checkout's
// engine does. None when the overlay has no nixosConfigurations.
func (a *App) nixosNames(p platform.Platform, o platform.EngineOverride) ([]string, error) {
	return flake.ConfigurationNames(context.Background(), a.runner(), p.FlakeDir, "nixosConfigurations", o.Args())
}

// targetFor is resolveTarget with its warning shown.
func (a *App) targetFor(p platform.Platform, host string) (target, error) {
	t, err := a.resolveTarget(p, host)
	if err == nil && t.Warning != "" {
		a.warn(t.Warning)
	}
	return t, err
}

// machineKind is the kind of this machine's own configuration, for the
// commands about the machine rather than a configuration (status, log,
// clean). Only a NixOS machine evaluates, as targetFor: whether the
// overlay has its NixOS host decides. With no overlay to read, it is a
// home, as before NixOS hosts.
func (a *App) machineKind(p platform.Platform) (Kind, error) {
	switch {
	case p.IsDarwin:
		return KindDarwin, nil
	case !p.IsNixOS || p.FlakeErr != nil:
		return KindHome, nil
	}
	t, err := a.targetFor(p, "")
	return t.Kind, err
}

// generationsKind is the kind whose generations this machine's status and
// log read: machineKind's, except on a NixOS machine whose overlay's NixOS
// hosts can't be read (said, as a warning): the system's, whose profile
// needs no overlay to read.
func (a *App) generationsKind(p platform.Platform) (Kind, error) {
	if !p.IsNixOS || p.FlakeErr != nil {
		return a.machineKind(p)
	}
	t, err := a.readTarget(p, "")
	var nu namesUnread
	if errors.As(err, &nu) {
		a.warn(nu.msg + "; this machine's generations are the system's")
		return KindNixOS, nil
	}
	if err == nil && t.Warning != "" {
		a.warn(t.Warning)
	}
	return t.Kind, err
}

// pickTarget is the target for host ("" for this machine, hostname) of the
// overlay's user, given its nixosConfigurations and homeConfigurations
// names:
//   - on a Mac, its nix-darwin system;
//   - --host naming a NixOS host, that host, from any Linux;
//   - on a NixOS machine the overlay has a NixOS host for, that host (with a
//     warning when the overlay has its home-manager configuration too);
//   - else a home-manager configuration: user@host for --host, else
//     flake.PickProfile's. On a NixOS machine with no --host whose overlay
//     has NixOS hosts but not that home, Unmatched names them; when that
//     home is the overlay's only one of user, made for another hostname,
//     a warning says so.
func pickTarget(darwin, nixosMachine bool, host, hostname, user string, nixosNames, homeNames []string) target {
	if darwin {
		if host == "" {
			return target{Kind: KindDarwin, Name: hostname}
		}
		return target{Kind: KindDarwin, Name: host, Host: host}
	}
	if host != "" {
		if slices.Contains(nixosNames, host) {
			return target{Kind: KindNixOS, Name: host, Host: host}
		}
		return target{Kind: KindHome, Name: user + "@" + host, Host: host}
	}
	if nixosMachine && slices.Contains(nixosNames, hostname) {
		t := target{Kind: KindNixOS, Name: hostname}
		if home := user + "@" + hostname; slices.Contains(homeNames, home) {
			t.Warning = "this machine has both nixosConfigurations." + hostname +
				` and homeConfigurations."` + home + `"; using the NixOS one`
		}
		return t
	}
	t := target{Kind: KindHome, Name: flake.PickProfile(homeNames, user, hostname)}
	switch {
	case !nixosMachine:
	case !slices.Contains(homeNames, t.Name):
		if len(nixosNames) > 0 {
			t.Unmatched = nixosNames
		}
	case t.Name != user+"@"+hostname:
		// The overlay's only home, made for another machine: a NixOS
		// machine taking it as a home-manager home only is no default.
		t.Warning = "this overlay has no NixOS host for " + hostname + "; using the home " + t.Name +
			" (piste host add makes this machine a NixOS host)"
	}
	return t
}
