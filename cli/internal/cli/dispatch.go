package cli

import (
	"path/filepath"
	"strings"

	"github.com/elocke/piste/cli/internal/plugins"
)

// Impl says who runs a command line.
type Impl string

const (
	ImplGo      Impl = "go"      // this binary (cobra)
	ImplPlugin  Impl = "plugin"  // piste-<cmd> from PATH
	ImplUnknown Impl = "unknown" // nothing: cobra reports the error
)

// Route is the dispatch decision for one argv.
type Route struct {
	Impl Impl
	Cmd  string   // the top-level command, "" for the root
	Path string   // executable for a plugin
	Argv []string // full argv for a plugin (Argv[0] included)
}

// Env is what routing reads; tests supply their own.
type Env struct {
	Getenv func(string) string
}

// leading global flags accepted before the command.
var globalFlags = map[string]bool{"-v": true, "--verbose": true}

// positionals splits args into the leading global flags and the rest,
// starting at the first positional. --overlay takes a value (--overlay <v>
// or --overlay=<v>).
func positionals(args []string) (flags, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if globalFlags[a] || strings.HasPrefix(a, "--overlay=") {
			continue
		}
		if a == "--overlay" {
			i++ // its value
			continue
		}
		if strings.HasPrefix(a, "-") {
			return args[:i], nil // --help, --version …: the root handles it
		}
		return args[:i], args[i:]
	}
	return args, nil
}

// overlayFlag is the value of a leading --overlay in flags ("" if none).
func overlayFlag(flags []string) string {
	v := ""
	for i, a := range flags {
		if a == "--overlay" && i+1 < len(flags) {
			v = flags[i+1]
		} else if s, ok := strings.CutPrefix(a, "--overlay="); ok {
			v = s
		}
	}
	return v
}

// Resolve decides who runs args (os.Args[1:]): a built-in runs in Go and can
// never be shadowed by a plugin; anything else is the plugin piste-<cmd> from
// PATH, if there is one.
func Resolve(args []string, goCmds map[string]bool, env Env) Route {
	_, rest := positionals(args)
	if len(rest) == 0 {
		return Route{Impl: ImplGo}
	}
	cmd := rest[0]
	if goCmds[cmd] || cobraBuiltins[cmd] {
		// A Go group may still defer an unknown subcommand to a plugin; the
		// group's own command decides (it knows its subcommands).
		return Route{Impl: ImplGo, Cmd: cmd}
	}
	if p := plugins.Find([]string{cmd}, env.Getenv("PATH")); p != "" {
		return Route{Impl: ImplPlugin, Cmd: cmd, Path: p, Argv: append([]string{filepath.Base(p)}, rest[1:]...)}
	}
	return Route{Impl: ImplUnknown, Cmd: cmd}
}
