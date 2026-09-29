// Package charts embebe los charts de Rampa en los binarios que los necesitan.
// La API renderiza rampa-app con estos archivos y le manda al agente los
// objetos ya resueltos.
package charts

import "embed"

// RampaApp es el chart único de aplicaciones (charts/rampa-app).
//
//go:embed all:rampa-app
var RampaApp embed.FS

// RampaAgent es el chart de instalación del agente (charts/rampa-agent). El
// runner de infraestructura lo instala con Helm en los clusters que crea.
//
//go:embed all:rampa-agent
var RampaAgent embed.FS
