package infra

import (
	"fmt"
	"strings"
)

// RootModule genera el main.tf del root module que invoca a un módulo
// embebido. El backend http se configura entero por variables de entorno
// (TF_HTTP_ADDRESS, TF_HTTP_LOCK_ADDRESS, TF_HTTP_UNLOCK_ADDRESS,
// TF_HTTP_USERNAME, TF_HTTP_PASSWORD), así el archivo no lleva ni la URL ni el
// token del run. Las variables se completan con rampa.auto.tfvars.json (los
// inputs no sensibles) y TF_VAR_agent_token (el token del agente).
func RootModule(m Module) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Root module generado por Rampa para %s. No editar: se regenera en cada run.\n", m.Module)
	fmt.Fprintf(&b, "# Backend http configurado por TF_HTTP_*; variables en rampa.auto.tfvars.json y TF_VAR_*.\n\n")

	b.WriteString("terraform {\n")
	b.WriteString("  required_version = \">= 1.6\"\n\n")
	b.WriteString("  backend \"http\" {}\n")
	if len(m.providers) > 0 {
		b.WriteString("\n  required_providers {\n")
		for _, p := range m.providers {
			fmt.Fprintf(&b, "    %s = {\n      source  = %q\n      version = %q\n    }\n", p.Name, p.Source, p.Version)
		}
		b.WriteString("  }\n")
	}
	b.WriteString("}\n\n")

	if m.Provider == "aws" {
		b.WriteString("provider \"aws\" {\n  region = var.region\n}\n\n")
	}

	for _, v := range m.variables {
		fmt.Fprintf(&b, "variable %q {\n", v.Name)
		fmt.Fprintf(&b, "  type = %s\n", v.Type)
		if v.Default != "" {
			fmt.Fprintf(&b, "  default = %s\n", v.Default)
		}
		if v.Sensitive {
			b.WriteString("  sensitive = true\n")
		}
		b.WriteString("}\n\n")
	}

	fmt.Fprintf(&b, "module %q {\n", m.Kind)
	fmt.Fprintf(&b, "  source = %q\n\n", "./modules/"+m.Module)
	for _, v := range m.variables {
		if v.Name == "region" {
			// region configura el provider, el módulo la lee de data.aws_region.
			continue
		}
		fmt.Fprintf(&b, "  %s = var.%s\n", v.Name, v.Name)
	}
	b.WriteString("}\n")

	for _, o := range m.outputs {
		fmt.Fprintf(&b, "\noutput %q {\n  value = module.%s.%s\n}\n", o, m.Kind, o)
	}
	return b.String()
}
