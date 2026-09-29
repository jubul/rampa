package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"rampa.dev/internal/cli/apiclient"
	"rampa.dev/internal/cli/config"
)

func newLoginCmd() *cobra.Command {
	var api, token, org string
	c := &cobra.Command{
		Use:   "login",
		Short: "Guarda la sesión contra la API de Rampa",
		Long: `Valida el token llamando a GET /v1/me y guarda api, token y org en
~/.config/rampa/config.yaml (permisos 0600). Las variables RAMPA_API,
RAMPA_TOKEN y RAMPA_ORG ganan sobre el archivo, así en CI no hace falta login.`,
		Example: `  rampa login --api https://api.rampa.dev --token rampa_xxx
  rampa login --api https://api.rampa.dev --token rampa_xxx --org acme`,
		RunE: func(cmd *cobra.Command, args []string) error {
			api = strings.TrimRight(strings.TrimSpace(api), "/")
			token = strings.TrimSpace(token)
			if !strings.HasPrefix(api, "http://") && !strings.HasPrefix(api, "https://") {
				return fmt.Errorf("--api tiene que ser una URL http(s), tengo %q", api)
			}
			if token == "" {
				return fmt.Errorf("--token no puede estar vacío")
			}
			client := apiclient.New(api, token).WithUserAgent("rampa-cli/" + cliVersion)
			ctx, cancel := requestCtx(cmd.Context())
			defer cancel()
			me, err := client.Me(ctx)
			if err != nil {
				return fmt.Errorf("no pude validar el token contra %s: %w", api, err)
			}
			if len(me.Memberships) == 0 {
				return fmt.Errorf("el token de %s no pertenece a ninguna organización", me.User.Email)
			}
			if org != "" {
				found := false
				for _, m := range me.Memberships {
					if m.Org.Slug == org {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("el token no tiene acceso a la organización %q (tiene: %s)", org, orgSlugs(me.Memberships))
				}
			} else if len(me.Memberships) == 1 {
				org = me.Memberships[0].Org.Slug
			}
			cfg := &config.Config{API: api, Token: token, Org: org}
			path, err := cfg.Save()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			name := me.User.Name
			if name == "" {
				name = me.User.Email
			}
			fmt.Fprintf(out, "✓ Sesión guardada en %s\n", path)
			fmt.Fprintf(out, "  api: %s\n", api)
			fmt.Fprintf(out, "  usuario: %s <%s>\n", name, me.User.Email)
			fmt.Fprintf(out, "  token: %s\n", config.Redacted(token))
			fmt.Fprintln(out, "\nOrganizaciones:")
			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "  SLUG\tNOMBRE\tROL\t")
			for _, m := range me.Memberships {
				mark := ""
				if m.Org.Slug == org {
					mark = "(default)"
				}
				fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", m.Org.Slug, m.Org.Name, m.Role, mark)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if org == "" {
				fmt.Fprintln(out, "\nTenés varias organizaciones: repetí con --org SLUG o pasá --org en cada comando.")
			}
			return nil
		},
	}
	c.Flags().StringVar(&api, "api", "", "URL de la API (ej. https://api.rampa.dev)")
	c.Flags().StringVar(&token, "token", "", "token de API (rampa_...)")
	c.Flags().StringVar(&org, "org", "", "slug de la organización por defecto")
	_ = c.MarkFlagRequired("api")
	_ = c.MarkFlagRequired("token")
	return c
}

func orgSlugs(ms []apiclient.Membership) string {
	s := make([]string, 0, len(ms))
	for _, m := range ms {
		s = append(s, m.Org.Slug)
	}
	return strings.Join(s, ", ")
}
