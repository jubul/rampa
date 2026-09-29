package cli

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"
)

var templatesFS fs.FS

// Execute corre el CLI. templates es el filesystem embebido con los golden paths.
func Execute(version string, templates fs.FS) error {
	templatesFS = templates
	cliVersion = version
	root := newRootCmd(version)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return err
	}
	return nil
}

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "rampa",
		Short: "Rampa: describí tu app en rampa.yaml y dejá el resto acá",
		Long: `Rampa es la plataforma de desarrollo. Un rampa.yaml describe la application; el CLI
la valida, genera los values del chart único y habla con la API del plano de control
para importar apps, desplegar y seguir el estado.

Sin conexión: init, validate, render, scope list, scope url.
Con sesión (rampa login): apps import, scope bind, deploy, undeploy, scope list con estado real.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newInitCmd(),
		newValidateCmd(),
		newRenderCmd(),
		newLoginCmd(),
		newAppsCmd(),
		newDeployCmd(),
		newUndeployCmd(),
		newScopeCmd(),
	)
	return root
}

func addFileFlag(c *cobra.Command, file *string) {
	c.Flags().StringVarP(file, "file", "f", "rampa.yaml", "ruta al rampa.yaml")
}
