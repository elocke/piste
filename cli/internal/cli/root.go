// Package cli is the piste command tree and its dispatcher:
// built-in commands run here; unknown commands run piste-<cmd> plugins from
// PATH.
package cli

import (
	"charm.land/huh/v2"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/execx"

	notices "github.com/elocke/piste/cli"
	"github.com/elocke/piste/cli/internal/plugins"
)

// Build info, set with -ldflags "-X …/internal/cli.Version=…".
var (
	Version = "dev"
	Commit  = ""
)

// App is shared by every Go command.
type App struct {
	Stdin          io.Reader             // answers to prompts (piste init, setup); nil reads as EOF
	pr             *prompter             // a.prompter(): one reader of Stdin for every prompt
	forms          *bool                 // a.useForms(): decided once per run (tests set it)
	runForm        func(*huh.Form) error // runs a form; nil: on the terminal (tests drive it)
	Stdout, Stderr io.Writer
	Env            Env
	Verbose        bool
	Overlay        string        // --overlay: the overlay of the piste checkout to act on
	Args           []string      // the argv piste was called with (os.Args[1:])
	movedTo        string        // the checkout piste setup moved into from elsewhere (piste())
	execRunner     execx.Runner  // tests: runs external programs; nil runs them
	flakesOff      bool          // piste setup turned flakes on for its own run: this Nix has them off (nixConfigFlakes)
	announced      string        // the flake and override announceFlake last printed
	record         *switchRecord // the running switch's log (switchlog.go); nil outside a switch
}

// ExitError ends the process with Code and no further message (the command
// has already printed what went wrong, e.g. a red `check`).
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// goCommand registers a Go implementation of a top-level command.
type goCommand struct {
	name string
	new  func(*App) *cobra.Command
}

var goRegistry []goCommand

// register is called from init() in each command's file.
func register(name string, f func(*App) *cobra.Command) {
	goRegistry = append(goRegistry, goCommand{name, f})
}

func goNames() map[string]bool {
	m := map[string]bool{}
	for _, c := range goRegistry {
		m[c.name] = true
	}
	return m
}

// execFn replaces the process; a var so tests can observe it.
var execFn = func(path string, argv []string, env []string) error {
	return syscall.Exec(path, argv, env)
}

// Main runs piste with args (os.Args[1:]) and returns the exit code.
func Main(args []string) int {
	app := &App{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Env: Env{Getenv: os.Getenv}}
	return app.Main(args)
}

// pluginEnv is the environment a plugin runs with: PISTE_FLAKE is the flake piste
// resolves (the overlay, in a piste checkout), unless already set. A flake
// that doesn't resolve leaves it unset and the plugin still runs, unless
// --overlay asked for one: that is an error.
func (a *App) pluginEnv() ([]string, error) {
	env := os.Environ()
	if os.Getenv("PISTE_FLAKE") != "" && a.Overlay == "" {
		return env, nil
	}
	p, err := a.machine()
	if err == nil && p.FlakeErr == nil && p.FlakeDir != "" {
		return append(env, "PISTE_FLAKE="+p.FlakeDir), nil
	}
	if a.Overlay != "" {
		if err == nil {
			err = p.FlakeErr
		}
		return nil, err
	}
	return env, nil
}

func (a *App) Main(args []string) int {
	a.Args = args
	if len(args) == 1 && args[0] == "--licenses" {
		fmt.Fprint(a.Stdout, notices.Text)
		return 0
	}
	if r := Resolve(args, goNames(), a.Env); r.Impl == ImplPlugin {
		flags, _ := positionals(args)
		a.Overlay = overlayFlag(flags)
		env, err := a.pluginEnv()
		if err != nil {
			fmt.Fprintf(a.Stderr, "piste: %v\n", err)
			return 1
		}
		if err := execFn(r.Path, r.Argv, env); err != nil {
			fmt.Fprintf(a.Stderr, "piste: exec %s: %v\n", r.Path, err)
			return 127
		}
		return 0 // not reached
	}
	root := a.newRoot()
	root.SetArgs(args)
	err := fang.Execute(context.Background(), root,
		fang.WithVersion(Version), fang.WithCommit(Commit),
		fang.WithErrorHandler(func(w io.Writer, styles fang.Styles, err error) {
			var ee ExitError
			if errors.As(err, &ee) {
				return
			}
			// fang title-cases the error's first word ("--host" prints as
			// "--Host"): capitalise its first letter and leave the rest.
			styles.ErrorText = styles.ErrorText.Transform(capitalizeFirst)
			fang.DefaultErrorHandler(w, styles, err)
		}))
	if err == nil {
		return 0
	}
	var ee ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return 1
}

// capitalizeFirst is s with its first letter upper-cased, the rest as written.
func capitalizeFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

func (a *App) newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "piste",
		Short: "Nix configuration management CLI",
		Long: "piste: Nix configuration management CLI.\n\n" +
			"Unknown commands run `piste-<command>` from PATH (plugins).\n" +
			"`piste --licenses` prints the third-party notices.",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.PersistentFlags().BoolVarP(&a.Verbose, "verbose", "v", false, "verbose output (echo external commands)")
	root.PersistentFlags().StringVar(&a.Overlay, "overlay", "", "the overlay of the piste checkout to act on (default: the one you are in, else the one with this host)")
	root.AddGroup(&cobra.Group{ID: "go", Title: "Commands:"},
		&cobra.Group{ID: "plugin", Title: "Plugins (piste-* on PATH):"})
	root.SetHelpCommandGroupID("go")
	root.SetCompletionCommandGroupID("go")

	have := map[string]bool{}
	for _, c := range goRegistry {
		cmd := c.new(a)
		cmd.GroupID = "go"
		root.AddCommand(cmd)
		have[c.name] = true
	}
	// piste-pnpm is piste.packages.pnpm's helper, which piste pkgs runs: no plugin.
	const pnpmHelper = "pnpm"
	for _, p := range plugins.List(a.Env.Getenv("PATH")) {
		top := strings.SplitN(p, "-", 2)[0]
		if have[top] || cobraBuiltins[p] || p == pnpmHelper {
			continue
		}
		root.AddCommand(&cobra.Command{
			Use: p, Short: "plugin: " + plugins.Prefix + p, GroupID: "plugin",
			DisableFlagParsing: true,
			// Plugins are exec'd before cobra (App.Main); reaching this means
			// dispatch didn't recognise the argv, so say so rather than no-op.
			RunE: func(*cobra.Command, []string) error {
				return fmt.Errorf("plugin %s%s was not run: put piste's flags before the command", plugins.Prefix, p)
			},
		})
	}
	return root
}
