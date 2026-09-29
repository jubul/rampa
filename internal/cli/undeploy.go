package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"rampa.dev/internal/cli/apiclient"
	"rampa.dev/pkg/manifest"
)

func newUndeployCmd() *cobra.Command {
	var file, appName, scope, org, deploys string
	var force bool
	c := &cobra.Command{
		Use:   "undeploy",
		Short: "Elimina un scope a través de la API (el agente borra el namespace)",
		Long: `Llama a DELETE /v1/orgs/{org}/apps/{app}/scopes/{scope}. La API encola un undeploy
al agente del cluster, que borra el namespace <app>-<scope>. Scopes permanentes
requieren --force acá y rol admin en la API.

Modo legado (DEPRECATED): con --deploys-repo borra apps/<app>/<scope> de un checkout
de rampa-deploys para que Argo CD haga prune. No habla con la API.`,
		Example: `  rampa undeploy --scope pr-142
  rampa undeploy --app checkout-api --scope pr-142
  rampa undeploy --scope staging --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveAppName(appName, file)
			if err != nil {
				return err
			}
			if !manifest.IsEphemeral(scope) && !force {
				return fmt.Errorf("%q es un scope permanente. Si de verdad querés borrarlo, repetí con --force", scope)
			}
			if deploys != "" {
				warnDeprecated(cmd, "deploys-repo")
				return undeployGitOps(cmd, name, scope, deploys)
			}
			s, err := newSession(org)
			if err != nil {
				return err
			}
			ctx, cancel := requestCtx(cmd.Context())
			defer cancel()
			ns := name + "-" + scope
			if err := s.client.DeleteScope(ctx, s.org, name, scope); err != nil {
				if apiclient.IsNotFound(err) {
					fmt.Fprintf(cmd.OutOrStdout(), "Nada que hacer: %s/%s no existe en %s (%v)\n", name, scope, s.org, err)
					return writeGitHubOutputs(map[string]string{"namespace": ns, "deleted": "false"})
				}
				return fmt.Errorf("eliminando %s/%s en %s: %w", name, scope, s.org, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ undeploy de %s/%s encolado en %s. El agente borra el namespace %s.\n", name, scope, s.org, ns)
			return writeGitHubOutputs(map[string]string{"namespace": ns, "deleted": "true"})
		},
	}
	addFileFlag(c, &file)
	c.Flags().StringVar(&appName, "app", "", "nombre de la application (default: metadata.name del rampa.yaml)")
	c.Flags().StringVarP(&scope, "scope", "s", "", "scope a eliminar")
	c.Flags().BoolVar(&force, "force", false, "permite borrar scopes permanentes")
	addOrgFlag(c, &org)
	c.Flags().StringVar(&deploys, "deploys-repo", "", "DEPRECATED: ruta local al checkout de rampa-deploys (modo GitOps legado, no usa la API)")
	_ = c.MarkFlagRequired("scope")
	return c
}

// undeployGitOps es el modo legado: borra el directorio del scope en rampa-deploys.
func undeployGitOps(cmd *cobra.Command, appName, scope, deploys string) error {
	dir := filepath.Join(deploys, "apps", appName, scope)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(cmd.OutOrStdout(), "Nada que hacer: %s no existe\n", dir)
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// Si la app quedó sin scopes, limpiamos la carpeta.
	parent := filepath.Dir(dir)
	if entries, err := os.ReadDir(parent); err == nil && len(entries) == 0 {
		_ = os.Remove(parent)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "✓ %s eliminado. Commiteá y pusheá: Argo CD hace prune del namespace %s-%s\n", dir, appName, scope)
	return writeGitHubOutputs(map[string]string{"namespace": appName + "-" + scope})
}
