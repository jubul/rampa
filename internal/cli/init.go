package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/scaffold"
)

func newInitCmd() *cobra.Command {
	var o scaffold.Options
	c := &cobra.Command{
		Use:   "init",
		Short: "Crea una application nueva desde un golden path",
		Example: `  rampa init --name checkout-api --team payments
  rampa init --name billing-api --team payments --type api --runtime go --port 9000`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.Org == "" {
				o.Org = os.Getenv("RAMPA_GITHUB_ORG")
			}
			if o.Org == "" {
				o.Org = "CHANGEME"
			}
			dest, err := scaffold.Run(templatesFS, o)
			if err != nil {
				return err
			}
			app, err := manifest.LoadValid(filepath.Join(dest, "rampa.yaml"))
			if err != nil {
				return fmt.Errorf("el template generó un rampa.yaml inválido, esto es un bug de la plataforma: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "✓ %s creada en %s (template %s-%s)\n\n", app.Metadata.Name, dest, o.Type, o.Runtime)
			fmt.Fprintln(out, "Próximos pasos:")
			fmt.Fprintf(out, "  cd %s\n", dest)
			fmt.Fprintln(out, "  git init && git add -A && git commit -m \"rampa init\"")
			fmt.Fprintf(out, "  gh repo create %s/%s --private --source . --push\n", o.Org, app.Metadata.Name)
			fmt.Fprintln(out, "\nEl primer push a main construye la imagen y la despliega al scope dev.")
			if o.Org == "CHANGEME" {
				fmt.Fprintln(out, "\nAviso: no sé tu organización de GitHub. Reemplazá CHANGEME en .github/workflows/ o exportá RAMPA_GITHUB_ORG.")
			}
			return nil
		},
	}
	c.Flags().StringVar(&o.Name, "name", "", "nombre de la application (dns-safe, único)")
	c.Flags().StringVar(&o.Team, "team", "", "equipo owner")
	c.Flags().StringVar(&o.Type, "type", "api", "tipo de application (hoy solo hay golden path para: api)")
	c.Flags().StringVar(&o.Runtime, "runtime", "go", "runtime (hoy solo hay golden path para: go)")
	c.Flags().IntVar(&o.Port, "port", 8080, "puerto HTTP (api y static)")
	c.Flags().StringVar(&o.Dir, "dir", ".", "directorio donde crear la carpeta de la app")
	c.Flags().StringVar(&o.Org, "github-org", "", "organización de GitHub donde vive rampa (default: $RAMPA_GITHUB_ORG)")
	_ = c.MarkFlagRequired("name")
	_ = c.MarkFlagRequired("team")
	return c
}
