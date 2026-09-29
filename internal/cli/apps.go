package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"rampa.dev/internal/cli/apiclient"
	"rampa.dev/pkg/manifest"
)

func newAppsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "apps",
		Short: "Gestiona applications en el plano de control",
	}
	c.AddCommand(newAppsImportCmd())
	return c
}

func newAppsImportCmd() *cobra.Command {
	var file, repo, org string
	c := &cobra.Command{
		Use:   "import",
		Short: "Importa (o actualiza) el rampa.yaml en la API",
		Long: `Valida el rampa.yaml localmente y lo manda con POST /v1/orgs/{org}/apps.
Si la application ya existe (conflict), reemplaza el manifest con PUT .../manifest.`,
		Example: `  rampa apps import
  rampa apps import -f rampa.yaml --repo https://github.com/acme/checkout-api`,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("leyendo %s: %w", file, err)
			}
			app, err := manifest.Parse(raw)
			if err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			if errs := app.Validate(); len(errs) > 0 {
				for _, e := range errs {
					fmt.Fprintln(cmd.ErrOrStderr(), "  ✗", e)
				}
				return fmt.Errorf("%d error(es) en %s; corregilos antes de importar", len(errs), file)
			}
			s, err := newSession(org)
			if err != nil {
				return err
			}
			ctx, cancel := requestCtx(cmd.Context())
			defer cancel()
			action := "importada"
			res, err := s.client.CreateApp(ctx, s.org, apiclient.CreateAppRequest{Manifest: string(raw), RepoURL: repo})
			if apiclient.IsConflict(err) {
				action = "actualizada"
				res, err = s.client.PutManifest(ctx, s.org, app.Metadata.Name, string(raw))
			}
			if err != nil {
				return fmt.Errorf("importando %s en %s: %w", app.Metadata.Name, s.org, err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "✓ %s %s en %s\n", res.Name, action, s.org)
			fmt.Fprintf(out, "  team: %s  tier: %d  type: %s/%s\n", res.Team, res.Tier, res.Type, res.Runtime)
			if len(res.ScopesDeclared) > 0 {
				fmt.Fprintf(out, "  scopes: %s\n", strings.Join(res.ScopesDeclared, ", "))
			}
			if res.ManifestSHA != "" {
				fmt.Fprintf(out, "  manifest_sha: %s\n", res.ManifestSHA)
			}
			if res.RepoURL != "" {
				fmt.Fprintf(out, "  repo: %s\n", res.RepoURL)
			}
			return nil
		},
	}
	addFileFlag(c, &file)
	c.Flags().StringVar(&repo, "repo", "", "URL del repositorio de la app")
	addOrgFlag(c, &org)
	return c
}
