package main

// Registro de providers de nube. Cada paquete se registra en su init con
// provider.Register; importarlos acá es lo que los hace disponibles en la API.
import (
	_ "rampa.dev/pkg/provider/aws"
	_ "rampa.dev/pkg/provider/azure"
	_ "rampa.dev/pkg/provider/digitalocean"
	_ "rampa.dev/pkg/provider/gcp"
	_ "rampa.dev/pkg/provider/oci"
	_ "rampa.dev/pkg/provider/ovh"
)
