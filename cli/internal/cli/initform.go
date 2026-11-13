package cli

import (
	"fmt"
	"strings"

	"charm.land/huh/v2"

	"github.com/elocke/piste/cli/internal/overlay"
)

// initQs: init's questions still to ask (the others came from flags or the
// name argument). nixos: the kind of machine offers NixOS beside a home
// (on a NixOS machine, without --nixos), asked with the system.
type initQs struct{ user, system, host, roles, zone, name, gitName, gitEmail, sops, extras, nixos bool }

func (q initQs) any() bool { return q != initQs{} }

// The roles nest (desktop brings dev, dev brings base: modules/roles), so a
// host takes one; a Mac's platform brings desktop, so only a Linux or NixOS
// host is asked, and there desktop installs no terminal app.
var roleOpts = []huh.Option[string]{
	huh.NewOption("Development   the basics plus vim, tmux, zellij, uv and the sops tools", "dev"),
	huh.NewOption("Desktop       Development plus zellij's and Ghostty's settings, no terminal app", "desktop"),
	huh.NewOption("Basics only   zsh, git, ssh and starship", "base"),
}

// startOpts answer "Start minimal or with everything?": everything is the
// extras role (modules/extras) beside the others.
var startOpts = []huh.Option[bool]{
	huh.NewOption("Minimal      the engine's defaults; add what you want as you go", false),
	huh.NewOption("Everything   plus the extras pack: more tools, aliases and settings, more apps on a Mac", true),
}

// startSummary is the Ready line for the answer.
func startSummary(extras bool) string {
	if extras {
		return "everything (the extras role)"
	}
	return "minimal"
}

// machineKind is an answer to "What kind of machine": its system, and for
// a Linux one whether the host is the NixOS system (else a home).
type machineKind struct {
	System string
	NixOS  bool
}

var machineKinds = []huh.Option[machineKind]{
	huh.NewOption("Mac, Apple silicon", machineKind{"aarch64-darwin", false}),
	huh.NewOption("Linux, x86_64", machineKind{"x86_64-linux", false}),
	huh.NewOption("Linux, ARM", machineKind{"aarch64-linux", false}),
	huh.NewOption("NixOS, x86_64", machineKind{"x86_64-linux", true}),
	huh.NewOption("NixOS, ARM", machineKind{"aarch64-linux", true}),
}

// machineOpts are the kinds of machine to offer: of system's ("" for any),
// NixOS and not when offerNixOS, else only those whose NixOS is nixos
// (--nixos answered it, or nothing offers it).
func machineOpts(system string, offerNixOS, nixos bool) []huh.Option[machineKind] {
	var opts []huh.Option[machineKind]
	for _, o := range machineKinds {
		if (system == "" || o.Value.System == system) && (offerNixOS || o.Value.NixOS == nixos) {
			opts = append(opts, o)
		}
	}
	return opts
}

// initForm asks q as a form at a terminal, editing ans in place: on entry
// its fields hold each question's default or flag value. Ready's Change
// something asks again from the start, every answer kept. errStopped when
// the person quits.
func (a *App) initForm(ans *overlay.Answers, q initQs, checkName func(string) error) error {
	if !q.any() {
		return nil
	}
	v := *ans
	m := machineKind{v.System, v.NixOS}
	role := "dev"
	if len(ans.Roles) > 0 {
		role = ans.Roles[0]
	}
	name := ""
	if !q.name {
		name = ans.Name
	}
	overlayName := func() string {
		if name == "" {
			return v.Zone
		}
		return name
	}
	roles := func() []string {
		switch {
		case !q.roles:
			return ans.Roles
		case overlay.Darwin(m.System):
			return nil
		}
		return []string{role}
	}

	for {
		create := true
		var groups []*huh.Group
		add := func(title string, hide func() bool, fs ...huh.Field) {
			if len(fs) > 0 {
				g := huh.NewGroup(fs...).Title(title)
				if hide != nil {
					g = g.WithHideFunc(hide)
				}
				groups = append(groups, g)
			}
		}

		var fs []huh.Field
		if q.user {
			fs = append(fs, huh.NewInput().Title("Your username on the machine").Value(&v.User).Validate(overlay.CheckUser))
		}
		if q.system || q.nixos {
			// Without the system question, the NixOS one alone: that
			// system's home or NixOS.
			sys := ""
			if !q.system {
				sys = m.System
			}
			sel := huh.NewSelect[machineKind]().Title("What kind of machine").Options(machineOpts(sys, q.nixos, m.NixOS)...).Value(&m)
			if q.nixos {
				sel = sel.Description("NixOS: piste makes the whole system, from this machine's /etc/nixos. Linux: your home only.")
			}
			fs = append(fs, sel)
		}
		if q.host {
			fs = append(fs, huh.NewInput().Title("Hostname").Value(&v.Hostname).
				DescriptionFunc(func() string {
					switch {
					case overlay.Darwin(m.System):
						return "The Mac's LocalHostName: letters, digits and dashes"
					case m.NixOS:
						return "The machine's hostname from now on: letters, digits and dashes"
					}
					return "The machine's short hostname"
				}, &m).
				Validate(func(h string) error { return overlay.CheckHostname(h, m.System, m.NixOS) }))
		}
		add("You and this machine", nil, fs...)

		// What it's for: with roles on Linux, without on a Mac. huh fields
		// can't be in two groups, so each variant gets its own.
		purpose := func(withRoles bool) []huh.Field {
			var fs []huh.Field
			if withRoles && q.roles {
				fs = append(fs, huh.NewSelect[string]().Title("Roles").
					Description("Desktop includes Development, which includes the basics.").
					Options(roleOpts...).Value(&role))
			}
			if q.zone {
				fs = append(fs, huh.NewInput().Title("Agent zone").Value(&v.Zone).
					Description("What AI agents may use on this machine; each overlay is one zone, e.g. personal or work.").
					Validate(overlay.CheckZone))
			}
			if q.name {
				fs = append(fs, huh.NewInput().Title("Overlay name").Value(&name).
					Description("Its directory in overlays/. Empty: the zone's name.").
					PlaceholderFunc(func() string { return v.Zone }, &v.Zone).
					Validate(func(s string) error {
						if s == "" {
							return checkName(v.Zone)
						}
						return checkName(s)
					}))
			}
			return fs
		}
		switch {
		case q.system:
			add("What it's for", func() bool { return overlay.Darwin(m.System) }, purpose(true)...)
			add("What it's for", func() bool { return !overlay.Darwin(m.System) }, purpose(false)...)
		default:
			add("What it's for", nil, purpose(!overlay.Darwin(m.System))...)
		}

		fs = nil
		if q.gitName {
			fs = append(fs, huh.NewInput().Title("Git name").Value(&v.GitName).
				Description("For your commits; filled in from your git config when it has one.").
				Validate(func(s string) error { return overlay.CheckGit("git name", s) }))
		}
		if q.gitEmail {
			fs = append(fs, huh.NewInput().Title("Git email").Value(&v.GitEmail).
				Validate(func(s string) error { return overlay.CheckGit("git email", s) }))
		}
		if q.sops {
			fs = append(fs, huh.NewConfirm().Title("Set up encrypted secrets (sops)?").
				Description("An age key and .sops.yaml, so you can commit secrets encrypted. You can add this later.").
				Affirmative("Yes").Negative("Not now").Value(&v.Sops))
		}
		add("Git and secrets", nil, fs...)

		fs = nil
		if q.extras {
			fs = append(fs, huh.NewSelect[bool]().Title("Start minimal or with everything?").
				Description("Everything adds the extras role. Each of its parts has a switch, so you can drop one later (docs/customising.md).").
				Options(startOpts...).Value(&v.Extras))
		}
		add("What to start with", nil, fs...)

		add("Ready", nil,
			huh.NewNote().Title("Ready").DescriptionFunc(func() string {
				r := strings.Join(roles(), ", ")
				switch {
				case overlay.Darwin(m.System) && r == "":
					r = "desktop (every Mac)"
				case r == "":
					r = "none"
				}
				sops := "no"
				if v.Sops {
					sops = "yes"
				}
				// A note's text is markup (_ and * are emphasis): the answers
				// are shown as typed.
				md := strings.NewReplacer(`\`, `\\`, "_", `\_`, "*", `\*`, "`", "\\`").Replace
				kind := m.System
				if m.NixOS {
					kind += ", NixOS"
				}
				return fmt.Sprintf("overlays/%s\n\n  user      %s\n  machine   %s (%s)\n  roles     %s\n  zone      %s\n  git       %s <%s>\n  secrets   %s\n  start     %s",
					md(overlayName()), md(v.User), md(v.Hostname), md(kind), md(r), md(v.Zone), md(v.GitName), md(v.GitEmail), sops, startSummary(v.Extras))
			}, []any{&v, &m, &role, &name}),
			huh.NewConfirm().TitleFunc(func() string { return "Create overlays/" + overlayName() + "?" }, []any{&name, &v.Zone}).
				Affirmative("Create").Negative("Change something").Value(&create),
		)

		if err := a.form(huh.NewForm(groups...)); err != nil {
			return err
		}
		if create {
			break
		}
	}
	v.System, v.NixOS = m.System, m.NixOS
	v.Roles, v.Name = roles(), overlayName()
	*ans = v
	return nil
}
