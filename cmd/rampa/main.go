// rampa es el CLI de la plataforma interna Rampa.
//
// Describí tu aplicación en un rampa.yaml y dejá que el CLI genere lo demás:
// el proyecto inicial, los values del chart único, y hable con la API del plano de
// control para importar la app, desplegar y seguir el estado de cada scope.
package main

import (
	"embed"
	"io/fs"
	"os"

	"rampa.dev/internal/cli"
)

//go:embed all:templates
var templates embed.FS

// version se inyecta en build con -ldflags "-X main.version=...".
var version = "dev"

func main() {
	sub, err := fs.Sub(templates, "templates")
	if err != nil {
		panic(err)
	}
	if err := cli.Execute(version, sub); err != nil {
		os.Exit(1)
	}
}
