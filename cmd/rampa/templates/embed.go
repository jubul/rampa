// Package templates embebe los golden paths de `rampa init` (api-go, ...).
// Lo usan el CLI y la API (GET /apps/{app}/scaffold.zip), así los dos generan
// exactamente el mismo proyecto inicial desde una sola copia de los archivos.
package templates

import "embed"

// FS tiene un directorio por golden path, nombrado <type>-<runtime>. Los
// archivos .tmpl se renderizan con pkg/scaffold. "all:" incluye los archivos
// ocultos (.github, .gitignore.tmpl, .dockerignore).
//
//go:embed all:api-go
var FS embed.FS
