package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"rampa.dev/pkg/manifest"
)

func newValidateCmd() *cobra.Command {
	var file string
	c := &cobra.Command{
		Use:   "validate",
		Short: "Valida el rampa.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := manifest.Load(file)
			if err != nil {
				return err
			}
			if errs := app.Validate(); len(errs) > 0 {
				for _, e := range errs {
					fmt.Fprintln(cmd.ErrOrStderr(), "  ✗", e)
				}
				return fmt.Errorf("%d error(es) en %s", len(errs), file)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ %s: %s (team %s, %s/%s) con %d scope(s), %d parameter(s), %d service(s)\n",
				file, app.Metadata.Name, app.Metadata.Team, app.Spec.Type, app.Spec.Runtime,
				len(app.Spec.Scopes), len(app.Spec.Parameters), len(app.Spec.Services))
			return nil
		},
	}
	addFileFlag(c, &file)
	return c
}
