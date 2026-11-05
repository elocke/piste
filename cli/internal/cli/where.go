package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() { register("where", newWhere) }

// newWhere prints the flake dir and the piste override that piste targets
// from the cwd, in two lines.
func newWhere(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "where",
		Short: "Show the flake dir and engine override piste targets from the cwd",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			o, err := a.engineOverride(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Stdout, "flake: %s (%s)\n", p.FlakeDir, p.FlakeSource)
			fmt.Fprintf(a.Stdout, "engine override: %s\n", o.Describe())
			return nil
		},
	}
}
