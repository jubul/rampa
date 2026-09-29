// Package terraform embebe los módulos de OpenTofu en el binario de la API.
// El runner de infraestructura los escribe a disco antes de correr tofu.
// El patrón sin "all:" excluye .terraform/ y .terraform.lock.hcl si quedaron
// de una validación local.
package terraform

import "embed"

// Modules contiene terraform/modules/<provider>/<módulo>/*.tf y sus README.
//
//go:embed modules
var Modules embed.FS
