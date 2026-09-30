package infra

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"rampa.dev/terraform"
)

// RootModule genera el main.tf del root module que invoca a un módulo
// embebido. El backend http se configura entero por variables de entorno
// (TF_HTTP_ADDRESS, TF_HTTP_LOCK_ADDRESS, TF_HTTP_UNLOCK_ADDRESS,
// TF_HTTP_USERNAME, TF_HTTP_PASSWORD), así el archivo no lleva ni la URL ni el
// token del run. Las variables se completan con rampa.auto.tfvars.json (los
// inputs no sensibles) y TF_VAR_* (bootstrap_token y, en aws/eks-cluster,
// agent_token).
//
// El root declara todas las variables del módulo, pero al bloque module solo
// le pasa las que el módulo declara en sus .tf (y solo reexporta los outputs
// que existen): así un módulo que todavía no acepta una variable común no
// rompe el apply, y la API no tiene que estar en fase exacta con terraform/.
func RootModule(m Module) string {
	declared, outputs := moduleDeclarations(m.Module)

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

	switch m.Provider {
	case "aws":
		// La región configura el provider; los módulos la leen de data.aws_region.
		b.WriteString("provider \"aws\" {\n  region = var.region\n}\n\n")
	case "digitalocean":
		// El token va por DIGITALOCEAN_TOKEN (provider.CredentialsProvider.Env).
		b.WriteString("provider \"digitalocean\" {}\n\n")
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
		if !declared[v.Name] {
			continue
		}
		fmt.Fprintf(&b, "  %s = var.%s\n", v.Name, v.Name)
	}
	b.WriteString("}\n")

	for _, o := range m.outputs {
		if !outputs[o] {
			continue
		}
		fmt.Fprintf(&b, "\noutput %q {\n  value = module.%s.%s\n}\n", o, m.Kind, o)
	}
	return b.String()
}

var (
	variableDeclRe = regexp.MustCompile(`(?m)^\s*variable\s+"([^"]+)"`)
	outputDeclRe   = regexp.MustCompile(`(?m)^\s*output\s+"([^"]+)"`)

	declMu    sync.Mutex
	declCache = map[string][2]map[string]bool{}
)

// moduleDeclarations lee los .tf del módulo embebido y devuelve las variables
// y los outputs que declara. Si el módulo no existe, devuelve conjuntos vacíos.
func moduleDeclarations(modulePath string) (variables, outputs map[string]bool) {
	declMu.Lock()
	defer declMu.Unlock()
	if c, ok := declCache[modulePath]; ok {
		return c[0], c[1]
	}
	variables, outputs = map[string]bool{}, map[string]bool{}
	dir := path.Join("modules", modulePath)
	entries, err := fs.ReadDir(terraform.Modules, dir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
				continue
			}
			content, err := fs.ReadFile(terraform.Modules, path.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			for _, m := range variableDeclRe.FindAllStringSubmatch(string(content), -1) {
				variables[m[1]] = true
			}
			for _, m := range outputDeclRe.FindAllStringSubmatch(string(content), -1) {
				outputs[m[1]] = true
			}
		}
	}
	declCache[modulePath] = [2]map[string]bool{variables, outputs}
	return variables, outputs
}

// ModuleDeclaresVariable dice si el módulo embebido declara la variable.
func ModuleDeclaresVariable(m Module, name string) bool {
	vars, _ := moduleDeclarations(m.Module)
	return vars[name]
}

// ModuleVariableNames devuelve, ordenadas, las variables que declara el módulo embebido.
func ModuleVariableNames(m Module) []string {
	vars, _ := moduleDeclarations(m.Module)
	out := make([]string, 0, len(vars))
	for v := range vars {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
