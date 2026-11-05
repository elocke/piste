package cli

// piste github: one GitHub account over SSH on this machine. A key of its own
// (~/.ssh/id_ed25519_github_<login>) for SSH and for signing commits, uploaded
// to the account, and the overlay's host file told which account it is
// (piste.github: modules/programs/github.nix does the rest). Each step is
// skipped when it is already done; piste stages the overlay edit and never
// commits it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/flake"
	"github.com/elocke/piste/cli/internal/onboard"
	"github.com/elocke/piste/cli/internal/platform"
)

func init() { register("github", newGithub) }

type githubOpts struct {
	login, key, host                 string
	yes, noUpload, noOverlay, dryRun bool
}

func newGithub(a *App) *cobra.Command {
	var o githubOpts
	c := &cobra.Command{
		Use:   "github",
		Short: "Set up a GitHub account over SSH: its own key for auth and signing, uploaded, and in your overlay",
		Long: "Set this machine up for a GitHub account over SSH, with gh logged in:\n\n" +
			"  1. The account: gh's active one, or --login <name>.\n" +
			"  2. A key of its own, ~/.ssh/id_ed25519_github_<login> (--key), made if missing\n" +
			"     (no passphrase; add one with ssh-keygen -p -f <key>).\n" +
			"  3. The key on the account, for authentication and for signing (gh ssh-key add);\n" +
			"     what GitHub already has is skipped. --no-upload leaves this to you.\n" +
			"  4. piste.github = { login; id; } in the overlay's host file (the one flake.nix lists\n" +
			"     for this machine's host or --host's, else hosts/<host>.nix), staged, never\n" +
			"     committed. --no-overlay leaves this to you.\n\n" +
			"After the next switch, github.com uses the key, commits are signed with it, and the\n" +
			"git email is the account's noreply address unless your overlay sets its own.\n" +
			"Each step already done is skipped, so run again whenever. Each change asks first\n" +
			"(--yes: don't). --dry-run says what would change and changes nothing.\n\n" +
			"gh needs the admin:public_key and admin:ssh_signing_key scopes to upload:\n" +
			"  " + onboard.ScopeRefresh,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runGithub(o)
		},
	}
	f := c.Flags()
	f.StringVar(&o.login, "login", "", "the GitHub account (default: gh's active one)")
	f.StringVar(&o.key, "key", "", "the private key (default ~/.ssh/id_ed25519_github_<login>)")
	f.StringVar(&o.host, "host", "", "the overlay host whose file to edit, by hostname (default: this machine's)")
	f.BoolVar(&o.yes, "yes", false, "don't ask before changing anything")
	f.BoolVar(&o.noUpload, "no-upload", false, "don't upload the key to GitHub")
	f.BoolVar(&o.noOverlay, "no-overlay", false, "don't edit the overlay")
	f.BoolVar(&o.dryRun, "dry-run", false, "say what would change, change nothing")
	return c
}

// ghLogin is gh as the account to set up: it is logged in, and (with login
// set to another account) acts as that one.
func (a *App) ghLogin(ctx context.Context, login string) (onboard.GH, onboard.Account, error) {
	g := onboard.GH{R: a.runner()}
	if _, err := exec.LookPath("gh"); err != nil {
		return g, onboard.Account{}, errors.New("gh isn't on PATH")
	}
	if err := g.AuthOK(ctx); err != nil {
		return g, onboard.Account{}, err
	}
	acct, err := g.User(ctx)
	if err != nil {
		return g, acct, err
	}
	if login != "" && !strings.EqualFold(login, acct.Login) {
		tok, err := g.TokenFor(ctx, login)
		if err != nil {
			return g, acct, err
		}
		g.Token = tok
		if acct, err = g.User(ctx); err != nil {
			return g, acct, err
		}
	}
	return g, acct, nil
}

func (a *App) runGithub(o githubOpts) error {
	ctx := context.Background()
	if o.login != "" {
		if err := onboard.CheckLogin(o.login); err != nil {
			a.errorf(fmt.Sprintf("piste github: %v (--login)", err))
			return ExitError{1}
		}
	}
	g, acct, err := a.ghLogin(ctx, o.login)
	if err != nil {
		a.errorf("piste github: " + err.Error())
		a.info("Log gh in, then run this again:")
		fmt.Fprintln(a.Stdout, "  "+onboard.LoginFix)
		return ExitError{1}
	}
	err = a.githubSteps(ctx, g, acct, o)
	if errors.Is(err, errStopped) {
		a.warn("Stopped at a question: nothing more was changed. Run piste github again to carry on.")
		return ExitError{1}
	}
	return err
}

// githubConfirm asks before a change: yes under --yes; no, and no answer
// (the end of the input, Ctrl+C), are errStopped.
func (a *App) githubConfirm(o githubOpts, question string) error {
	if o.yes {
		return nil
	}
	var ok bool
	if a.useForms() {
		var err error
		if ok, err = a.askYesNo(question, "Yes", "No", true); err != nil {
			return err
		}
	} else {
		p := a.prompter()
		v := p.ask(question+" y/n", "y")
		if p.eof {
			return errStopped
		}
		ok = strings.HasPrefix(strings.ToLower(v), "y")
	}
	if !ok {
		return errStopped
	}
	return nil
}

// expandHome writes a leading ~/ as home.
func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// githubHostFile is the overlay's host file for this machine, relative to
// p.FlakeDir, or for the host named host (--host, a hostname or a host key).
// The machine's host is found as piste build finds it (on Linux
// flake.DetectProfile with the overlay's user: <user>@<hostname>, else the
// only <user>@*; on a Mac the hostname, else the overlay's only Mac), and the
// host's file is the one flake.nix's hosts entry lists in its modules
// (onboard.HostFile), else hosts/<name>.nix, where <name> is the host's name
// without its user@.
func (a *App) githubHostFile(ctx context.Context, p platform.Platform, host string) string {
	user := profileUser(p)
	var keys []string // flake.nix's host keys to look for, best first
	switch {
	case host != "":
		keys = []string{host, user + "@" + host}
	case p.IsDarwin:
		// The hostname, else the overlay's only Mac (a hostname that isn't a
		// host's name or alias).
		keys = []string{p.Hostname}
		if names := flake.Configurations(ctx, a.runner(), p.FlakeDir, "darwinConfigurations"); len(names) == 1 && names[0] != p.Hostname {
			keys = names
		}
	default:
		keys = []string{user + "@" + p.Hostname}
		if len(flake.HMProfiles(ctx, a.runner(), p.FlakeDir)) > 0 {
			keys = []string{flake.DetectProfile(ctx, a.runner(), p.FlakeDir, user, p.Hostname)}
		}
	}
	if src, err := os.ReadFile(filepath.Join(p.FlakeDir, "flake.nix")); err == nil {
		for _, k := range keys {
			if rel, ok := onboard.HostFile(src, k); ok && fileExists(filepath.Join(p.FlakeDir, rel)) {
				return rel
			}
		}
	}
	name := keys[0]
	if i := strings.LastIndex(name, "@"); i >= 0 {
		name = name[i+1:]
	}
	return filepath.Join("hosts", name+".nix")
}

// githubSteps is steps 2 to 5 for acct.
func (a *App) githubSteps(ctx context.Context, g onboard.GH, acct onboard.Account, o githubOpts) error {
	home := a.home()
	key := onboard.DefaultKey(home, acct.Login)
	if o.key != "" {
		key = expandHome(o.key, home)
		if !filepath.IsAbs(key) {
			if abs, err := filepath.Abs(key); err == nil {
				key = abs
			}
		}
	}
	pubPath := key + ".pub"
	shown := tilde(home, key)
	a.success(fmt.Sprintf("✓ gh is logged in as %s (id %d)", acct.Login, acct.ID))

	// The overlay's host file, found before any write.
	var hostFile, rel, flakeDir string
	if !o.noOverlay {
		p, err := a.plat()
		if err != nil {
			a.errorf("piste github: run it in a piste checkout, in an overlay or with --overlay <name>; --no-overlay leaves the overlay to you")
			return ExitError{1}
		}
		flakeDir = p.FlakeDir
		rel = a.githubHostFile(ctx, p, o.host)
		hostFile = filepath.Join(flakeDir, rel)
		if o.host != "" && !fileExists(hostFile) {
			a.errorf(fmt.Sprintf("piste github: %s has no host file %s (--host)", tilde(home, flakeDir), rel))
			return ExitError{1}
		}
	}

	// 2. The key.
	haveKey := fileExists(key)
	switch {
	case haveKey:
		a.success("✓ Key " + shown + " exists: using it")
	case o.dryRun:
		a.info(fmt.Sprintf("Would make the key %s: ssh-keygen -q -t ed25519 -N \"\" -C %s@github -f %s", shown, acct.Login, shown))
	default:
		if err := a.githubConfirm(o, fmt.Sprintf("Make an SSH key for %s at %s?", acct.Login, shown)); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
			return err
		}
		if err := onboard.Keygen(ctx, a.runner(), key, acct.Login); err != nil {
			a.errorf("piste github: " + err.Error())
			return ExitError{1}
		}
		haveKey = true
		a.success("Made the key " + shown)
		a.info("It has no passphrase. To add one: ssh-keygen -p -f " + shown)
	}
	pub := ""
	if haveKey {
		if b, err := os.ReadFile(pubPath); err == nil && onboard.KeyMaterial(string(b)) != "" {
			pub = strings.TrimSpace(string(b))
		} else {
			line, err := onboard.PublicKey(ctx, a.runner(), key)
			if err != nil {
				a.errorf("piste github: " + err.Error())
				return ExitError{1}
			}
			pub = line
			if !o.dryRun {
				if err := os.WriteFile(pubPath, []byte(line+"\n"), 0o644); err != nil {
					return err
				}
			}
		}
	}

	// 3. The upload.
	if o.noUpload {
		a.info(fmt.Sprintf("Not uploading (--no-upload): add %s.pub to github.com/settings/keys as an authentication key and as a signing key", shown))
	} else if err := a.githubUpload(ctx, g, o, pub, pubPath, tilde(home, pubPath), key); err != nil {
		return err
	}

	// 4. The overlay.
	if o.noOverlay {
		a.info("Not editing the overlay (--no-overlay). To use the account, set in a host file:")
		a.block(strings.TrimRight(onboard.Block("", acct, key, home), "\n"))
	} else if err := a.githubOverlay(o, acct, key, home, flakeDir, rel, hostFile); err != nil {
		return err
	}

	// 5. What's next.
	if o.dryRun {
		a.info("Dry run: nothing changed.")
		return nil
	}
	if !o.noOverlay {
		fmt.Fprintf(a.Stdout, "Next: %s switch\n", a.piste())
	}
	fmt.Fprintf(a.Stdout, "Then: ssh -T git@github.com    # greets %s once the switch is done\n", acct.Login)
	return nil
}

// githubUpload adds the key to the account as whichever of authentication
// and signing it isn't yet.
func (a *App) githubUpload(ctx context.Context, g onboard.GH, o githubOpts, pub, pubPath, shownPub, key string) error {
	fail := func(err error) error {
		var se *onboard.ScopeError
		if errors.As(err, &se) {
			a.errorf(fmt.Sprintf("piste github: gh isn't allowed to manage keys: it needs the %s scope. Run this, then piste github again:", se.Scope))
			fmt.Fprintln(a.Stdout, "  "+onboard.ScopeRefresh)
			return ExitError{1}
		}
		a.errorf("piste github: " + err.Error())
		return ExitError{1}
	}
	if pub == "" { // a dry run that would have made the key
		a.info("Would upload it to GitHub as an authentication key and as a signing key")
		return nil
	}
	material := onboard.KeyMaterial(pub)
	var missing []onboard.KeyKind
	for _, kind := range []onboard.KeyKind{onboard.Auth, onboard.Signing} {
		have, err := g.Keys(ctx, kind)
		if err != nil {
			return fail(err)
		}
		if !onboard.Has(have, material) {
			missing = append(missing, kind)
		}
	}
	if len(missing) == 0 {
		a.success("✓ The key is on your GitHub account, for authentication and for signing")
		return nil
	}
	names := make([]string, len(missing))
	for i, k := range missing {
		names[i] = string(k)
	}
	if o.dryRun {
		a.info("Would upload " + shownPub + " to GitHub as: " + strings.Join(names, ", "))
		return nil
	}
	if err := a.githubConfirm(o, fmt.Sprintf("Upload %s to GitHub as: %s?", shownPub, strings.Join(names, ", "))); err != nil {
		return err
	}
	q, _, err := a.thisMachine()
	if err != nil {
		return err
	}
	title := fmt.Sprintf("%s (%s)", filepath.Base(key), q.Hostname)
	for _, kind := range missing {
		if err := g.Add(ctx, pubPath, kind, title); err != nil {
			if errors.Is(err, onboard.ErrKeyInUse) {
				a.errorf(fmt.Sprintf("piste github: GitHub says %s is already in use on another account. GitHub allows a key on one account only, so each account needs its own key: pass --key <path> for another.", shownPub))
				return ExitError{1}
			}
			return fail(err)
		}
		a.success(fmt.Sprintf("Uploaded %s as a %s key", shownPub, kind))
	}
	return nil
}

// githubOverlay puts piste.github in the host file and stages it.
func (a *App) githubOverlay(o githubOpts, acct onboard.Account, key, home, flakeDir, rel, hostFile string) error {
	byHand := func(why string) error {
		a.warn(fmt.Sprintf("Add by hand: %s into %s (%s)", oneLine(onboard.Block("", acct, key, home)), tilde(home, hostFile), why))
		return nil
	}
	src, err := os.ReadFile(hostFile)
	if err != nil {
		return byHand("it isn't there")
	}
	out, changed, err := onboard.SetGithub(src, acct, key, home)
	if err != nil {
		return byHand("piste found no place for it in this file")
	}
	if !changed {
		a.success(fmt.Sprintf("✓ %s already sets piste.github for %s", rel, acct.Login))
		a.githubEmail(acct, flakeDir, src, rel)
		return nil
	}
	if o.dryRun {
		a.info(fmt.Sprintf("Would set piste.github for %s in %s and stage it", acct.Login, rel))
		return nil
	}
	if err := a.githubConfirm(o, fmt.Sprintf("Set piste.github for %s in %s?", acct.Login, rel)); err != nil {
		return err
	}
	if err := os.WriteFile(hostFile, out, 0o644); err != nil {
		return err
	}
	if err := a.gitIn(flakeDir, "add", "--", rel); err != nil {
		return err
	}
	a.success(fmt.Sprintf("Set piste.github for %s in %s, staged (piste never commits)", acct.Login, rel))
	a.githubEmail(acct, flakeDir, out, rel)
	return nil
}

// oneLine is a block on one line, for a hint.
func oneLine(block string) string {
	f := strings.Fields(block)
	return strings.Join(f, " ")
}

var emailRE = regexp.MustCompile(`(?m)^[^#\n]*\bemail\s*=\s*"([^"]+)"`)

// githubEmail says which email commits will use: the noreply address, unless
// the overlay's host file or home.nix sets one (that wins over the engine's).
func (a *App) githubEmail(acct onboard.Account, flakeDir string, host []byte, rel string) {
	noreply := acct.Noreply()
	home, _ := os.ReadFile(filepath.Join(flakeDir, "home.nix"))
	for _, f := range []struct {
		name string
		src  []byte
	}{{rel, host}, {"home.nix", home}} {
		if m := emailRE.FindSubmatch(f.src); m != nil && string(m[1]) != noreply {
			a.warn(fmt.Sprintf("Commits will use %s, which your overlay sets in %s. For the noreply address, %s, remove that line or set it to that address.", m[1], f.name, noreply))
			return
		}
	}
	a.info("Commits will use the noreply address, " + noreply)
}

// setupGithub is piste setup's offer: when gh is logged in and this machine's
// host file doesn't have an account yet, set one up, so the first switch has
// it. Never fatal: a refusal, a failure or no answer leaves piste github for
// later. later: gh isn't on PATH yet (the switch brings it), so setup's last
// lines say how to log it in and run piste github.
func (a *App) setupGithub(name string, force, skip bool) (later bool) {
	if skip {
		return false
	}
	ctx := context.Background()
	a.Overlay = name
	p, err := a.plat()
	if err != nil {
		return false
	}
	if b, err := os.ReadFile(filepath.Join(p.FlakeDir, a.githubHostFile(ctx, p, ""))); err == nil && onboard.HasGithub(b) {
		return false
	}
	// A fresh machine has no gh until the switch brings it: nothing to
	// warn about yet; setup's last lines say how, after the switch.
	if _, err := exec.LookPath("gh"); err != nil {
		return true
	}
	g, acct, err := a.ghLogin(ctx, "")
	if err != nil {
		a.warn("! GitHub: gh isn't logged in, so no SSH key for it yet. Later: " + onboard.LoginFix + ", then " + a.piste() + " github")
		return false
	}
	if !force {
		ask := fmt.Sprintf("Set up a GitHub SSH key for %s? (a key of its own for SSH and signing, uploaded to GitHub, and named in your host file)", acct.Login)
		var ok bool
		if a.useForms() {
			if ok, err = a.askYesNo(ask, "Set it up", "Not now", false); err != nil {
				return false
			}
		} else {
			ok = strings.HasPrefix(strings.ToLower(a.prompter().ask(ask+" y/n", "n")), "y")
		}
		if !ok {
			a.info("Not now. When you want it: " + a.piste() + " github")
			return false
		}
	}
	if err := a.githubSteps(ctx, g, acct, githubOpts{yes: true}); err != nil {
		a.warn("GitHub setup stopped: see above. Run " + a.piste() + " github to carry on.")
	}
	return false
}
