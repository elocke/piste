package cli

// `piste remote move`: hand blocked Claude Code background jobs to a remote dev
// box (internal/remotemove).

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/remotemove"
)

func newRemoteMove(a *App) *cobra.Command {
	var o remotemove.Options
	c := &cobra.Command{
		Use:   "move <host> (<job-id|session-id>... | --all-blocked)",
		Short: "Move Claude Code background jobs to a remote host to resume there",
		Long: "Move Claude Code background jobs to a remote host to resume there.\n\n" +
			"Per job: clone the repo it last worked in from origin at the same path under the\n" +
			"remote's $HOME, push the branch from here straight into that clone (never to\n" +
			"origin), check it out in the same worktree layout, and import the transcript with\n" +
			remotemove.SessionMigrate + " (via uvx). Prints the resume command.\n\n" +
			"Refuses uncommitted changes, a detached HEAD, and transcripts that match a secret\n" +
			"pattern (names and counts only; --allow-secrets overrides). Safe to re-run.\n" +
			"The remote needs git and uvx.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.IDs = args[1:]
			if err := remotemove.Validate(o.IDs, o.AllBlocked); err != nil {
				return fmt.Errorf("%w\nusage: piste remote %s", err, cmd.Use)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			o.Host = a.resolveHost(args[0])
			o.Home = home
			o.ClaudeDir = remotemove.ClaudeDir(a.Env.Getenv, home)
			o.Runner = a.runner()
			o.Say = func(kind, msg string) {
				w := a.Stdout
				if kind == "err" {
					w = a.Stderr
				}
				a.say(w, msg, kind)
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			s, err := remotemove.Run(ctx, o)
			if err != nil {
				return err
			}
			line := fmt.Sprintf("piste remote move: %d moved, %d not moved", s.Moved, s.NotMoved)
			if o.DryRun {
				line += " (dry run)"
			}
			if s.NotMoved > 0 {
				a.warn(line)
				return ExitError{1}
			}
			a.success(line)
			return nil
		},
	}
	c.Flags().BoolVarP(&o.DryRun, "dry-run", "n", false, "Print the plan; never contact the remote")
	c.Flags().BoolVar(&o.AllowSecrets, "allow-secrets", false, "Move transcripts that match a secret pattern")
	c.Flags().BoolVar(&o.AllBlocked, "all-blocked", false, "Move every job whose state is blocked")
	return c
}
