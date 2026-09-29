package cli

import (
	"errors"
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"rampa.dev/internal/cli/apiclient"
	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/render"
)

func newScopeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "scope",
		Short: "Consulta y administra los scopes de una application",
	}
	c.AddCommand(newScopeListCmd(), newScopeURLCmd(), newScopeBindCmd())
	return c
}

func newScopeListCmd() *cobra.Command {
	var file, platform, org string
	var offline bool
	c := &cobra.Command{
		Use:   "list",
		Short: "Lista los scopes declarados en el rampa.yaml (y su estado real si hay sesión)",
		Long: `Sin sesión muestra lo declarado en el rampa.yaml. Con sesión (rampa login o
RAMPA_API/RAMPA_TOKEN) consulta GET /scopes y agrega STATUS, IMAGEN, la URL real
y los scopes efímeros vivos. Con --offline no toca la API.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := manifest.LoadValid(file)
			if err != nil {
				return err
			}
			plat, err := render.LoadPlatformOptional(platform)
			if err != nil {
				return err
			}

			var live map[string]apiclient.Scope
			var extra []apiclient.Scope
			if !offline {
				s, err := optionalSession(org)
				if errors.Is(err, errNoOrg) {
					fmt.Fprintf(cmd.ErrOrStderr(), "aviso: %v; muestro solo lo declarado\n", err)
					s, err = nil, nil
				}
				if err != nil {
					return err
				}
				if s != nil {
					ctx, cancel := requestCtx(cmd.Context())
					items, err := s.client.ListScopes(ctx, s.org, app.Metadata.Name)
					cancel()
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "aviso: no pude consultar la API (%v); muestro solo lo declarado\n", err)
					} else {
						live = make(map[string]apiclient.Scope, len(items))
						for _, it := range items {
							live[it.Name] = it
							if _, declared := app.Spec.Scopes[it.Name]; !declared {
								extra = append(extra, it)
							}
						}
						sort.Slice(extra, func(i, j int) bool { return extra[i].Name < extra[j].Name })
					}
				}
			}

			names := make([]string, 0, len(app.Spec.Scopes))
			for n := range app.Spec.Scopes {
				names = append(names, n)
			}
			sort.Strings(names)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if live == nil {
				fmt.Fprintln(w, "SCOPE\tNAMESPACE\tSIZE\tREPLICAS\tSTRATEGY\tURL")
			} else {
				fmt.Fprintln(w, "SCOPE\tNAMESPACE\tSIZE\tREPLICAS\tSTRATEGY\tSTATUS\tIMAGEN\tURL")
			}
			row := func(name, ns, size string, replicas int, strategy, url string) {
				if live == nil {
					fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n", name, ns, size, replicas, strategy, url)
					return
				}
				status, image := "-", "-"
				if l, ok := live[name]; ok {
					if l.Status != "" {
						status = l.Status
					}
					if l.CurrentDeployment != nil && l.CurrentDeployment.Image != "" {
						image = l.CurrentDeployment.Image
					}
					if l.URL != "" {
						url = l.URL
					}
					if l.Namespace != "" {
						ns = l.Namespace
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n", name, ns, size, replicas, strategy, status, image, url)
			}
			for _, n := range names {
				s := app.Spec.Scopes[n]
				if n == manifest.PRScopeKey {
					if live == nil {
						fmt.Fprintf(w, "pr-<n>\t%s-pr-<n>\t%s\t%d\t%s\t(plantilla de efímeros, ttl %s)\n", app.Metadata.Name, s.Size, s.Replicas, s.Strategy, s.TTL)
					} else {
						fmt.Fprintf(w, "pr-<n>\t%s-pr-<n>\t%s\t%d\t%s\t-\t-\t(plantilla de efímeros, ttl %s)\n", app.Metadata.Name, s.Size, s.Replicas, s.Strategy, s.TTL)
					}
					continue
				}
				vals, _, err := render.Build(app, n, render.Options{Platform: plat, Image: "latest"})
				if err != nil {
					return err
				}
				row(n, app.Namespace(n), s.Size, s.Replicas, s.Strategy, render.URL(vals))
			}
			// Efímeros vivos que la API conoce y el manifest no nombra.
			for _, e := range extra {
				row(e.Name, e.Namespace, e.Size, e.Replicas, e.Strategy, e.URL)
			}
			return w.Flush()
		},
	}
	addFileFlag(c, &file)
	c.Flags().StringVar(&platform, "platform", "", "ruta a platform.yaml para resolver dominios (modo offline)")
	c.Flags().BoolVar(&offline, "offline", false, "no consultar la API aunque haya sesión")
	addOrgFlag(c, &org)
	return c
}

func newScopeURLCmd() *cobra.Command {
	var file, scope, platform string
	c := &cobra.Command{
		Use:   "url",
		Short: "Imprime la URL pública de un scope",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := manifest.LoadValid(file)
			if err != nil {
				return err
			}
			plat, err := render.LoadPlatformOptional(platform)
			if err != nil {
				return err
			}
			vals, _, err := render.Build(app, scope, render.Options{Platform: plat, Image: "latest"})
			if err != nil {
				return err
			}
			url := render.URL(vals)
			if url == "" {
				return fmt.Errorf("%s no expone HTTP (type %s)", app.Metadata.Name, app.Spec.Type)
			}
			fmt.Fprintln(cmd.OutOrStdout(), url)
			return writeGitHubOutputs(map[string]string{"url": url})
		},
	}
	addFileFlag(c, &file)
	c.Flags().StringVarP(&scope, "scope", "s", "", "scope")
	c.Flags().StringVar(&platform, "platform", "", "ruta a platform.yaml para resolver dominios")
	_ = c.MarkFlagRequired("scope")
	return c
}

func newScopeBindCmd() *cobra.Command {
	var file, appName, scope, cluster, org string
	c := &cobra.Command{
		Use:   "bind",
		Short: "Ata un scope a un cluster",
		Long: `Llama a PUT /v1/orgs/{org}/apps/{app}/scopes/{scope} con el cluster_id. Un scope prod
solo puede vivir en un cluster prod; el resto en clusters nonprod.`,
		Example: `  rampa scope bind --scope prod --cluster 7d1c...
  rampa scope bind --app checkout-api --scope dev --cluster 7d1c...`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveAppName(appName, file)
			if err != nil {
				return err
			}
			s, err := newSession(org)
			if err != nil {
				return err
			}
			ctx, cancel := requestCtx(cmd.Context())
			defer cancel()
			res, err := s.client.BindScope(ctx, s.org, name, scope, cluster)
			if err != nil {
				return fmt.Errorf("atando %s/%s al cluster %s: %w", name, scope, cluster, err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "✓ %s/%s atado al cluster %s\n", name, scope, res.ClusterID)
			if res.Namespace != "" {
				fmt.Fprintf(out, "  namespace: %s\n", res.Namespace)
			}
			if res.Status != "" {
				fmt.Fprintf(out, "  status: %s\n", res.Status)
			}
			if res.URL != "" {
				fmt.Fprintf(out, "  url: %s\n", res.URL)
			}
			return nil
		},
	}
	addFileFlag(c, &file)
	c.Flags().StringVar(&appName, "app", "", "nombre de la application (default: metadata.name del rampa.yaml)")
	c.Flags().StringVarP(&scope, "scope", "s", "", "scope a atar")
	c.Flags().StringVar(&cluster, "cluster", "", "id del cluster")
	addOrgFlag(c, &org)
	_ = c.MarkFlagRequired("scope")
	_ = c.MarkFlagRequired("cluster")
	return c
}
