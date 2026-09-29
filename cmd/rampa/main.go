// rampa es el CLI de la plataforma interna Rampa.
//
// Describí tu aplicación en un rampa.yaml y dejá que el CLI genere lo demás:
// el proyecto inicial, los values del chart único, y hable con la API del plano de
// control para importar la app, desplegar y seguir el estado de cada scope.
package main

import (
	"os"

	"rampa.dev/cmd/rampa/templates"
	"rampa.dev/internal/cli"
)

// version se inyecta en build con -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// Los golden paths viven en cmd/rampa/templates y los comparte la API.
	if err := cli.Execute(version, templates.FS); err != nil {
		os.Exit(1)
	}
}
