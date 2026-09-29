package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/render"
)

func newRenderCmd() *cobra.Command {
	var (
		file, scope, image, platform, registry, domain string
	)
	c := &cobra.Command{
		Use:   "render",
		Short: "Genera los values del chart rampa-app para un scope",
		Example: `  rampa render --scope dev
  rampa render --scope prod --image sha-a1b2c3d --platform ../rampa-deploys/platform.yaml | helm template x charts/rampa-app -f -`,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := manifest.LoadValid(file)
			if err != nil {
				return err
			}
			plat, err := render.LoadPlatformOptional(platform)
			if err != nil {
				return err
			}
			vals, warns, err := render.Build(app, scope, render.Options{
				Image: image, Platform: plat, Registry: registry, Domain: domain,
			})
			if err != nil {
				return err
			}
			for _, w := range warns {
				fmt.Fprintln(cmd.ErrOrStderr(), "aviso:", w)
			}
			out, err := yaml.Marshal(vals)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(out)
			return err
		},
	}
	addFileFlag(c, &file)
	c.Flags().StringVarP(&scope, "scope", "s", "", "scope a renderizar (dev, staging, prod, pr-<n>)")
	c.Flags().StringVar(&image, "image", "", "tag o referencia completa de la imagen")
	c.Flags().StringVar(&platform, "platform", "", "ruta a platform.yaml (registry, clusters)")
	c.Flags().StringVar(&registry, "registry", "", "registry ECR, pisa al de platform.yaml")
	c.Flags().StringVar(&domain, "domain", "", "dominio del cluster, pisa al de platform.yaml")
	_ = c.MarkFlagRequired("scope")
	return c
}
