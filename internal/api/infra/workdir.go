package infra

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"rampa.dev/charts"
	"rampa.dev/terraform"
)

// Workdir es el directorio de trabajo de un run ya escrito a disco.
type Workdir struct {
	// Dir es la raíz: contiene main.tf, rampa.auto.tfvars.json, modules/ y charts/.
	Dir string
	// AgentChartPath es la ruta del chart rampa-agent escrito desde charts.RampaAgent.
	AgentChartPath string
}

// TFVarsFile es el archivo con los inputs no sensibles del run. OpenTofu
// carga solo los *.auto.tfvars.json.
const TFVarsFile = "rampa.auto.tfvars.json"

// WriteWorkdir crea un directorio temporal bajo base con el root module del
// módulo, los módulos embebidos (terraform.Modules) y el chart del agente
// (charts.RampaAgent). El llamador lo borra al terminar.
func WriteWorkdir(base string, m Module) (*Workdir, error) {
	if base == "" {
		base = os.TempDir()
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("creando %s: %w", base, err)
	}
	dir, err := os.MkdirTemp(base, "rampa-infra-")
	if err != nil {
		return nil, fmt.Errorf("creando el directorio de trabajo: %w", err)
	}
	wd := &Workdir{Dir: dir}
	if err := wd.populate(m); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return wd, nil
}

func (w *Workdir) populate(m Module) error {
	if err := os.WriteFile(filepath.Join(w.Dir, "main.tf"), []byte(RootModule(m)), 0o644); err != nil {
		return err
	}
	// Solo el módulo que se ejecuta, no todo terraform/modules.
	if err := copyFS(terraform.Modules, "modules/"+m.Module, filepath.Join(w.Dir, "modules", filepath.FromSlash(m.Module))); err != nil {
		return fmt.Errorf("escribiendo el módulo %s: %w", m.Module, err)
	}
	w.AgentChartPath = filepath.Join(w.Dir, "charts", "rampa-agent")
	if err := copyFS(charts.RampaAgent, "rampa-agent", w.AgentChartPath); err != nil {
		return fmt.Errorf("escribiendo el chart rampa-agent: %w", err)
	}
	return nil
}

// WriteTFVars escribe los inputs como rampa.auto.tfvars.json.
func (w *Workdir) WriteTFVars(vars map[string]any) error {
	b, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.Dir, TFVarsFile), b, 0o644)
}

// Remove borra el directorio de trabajo.
func (w *Workdir) Remove() {
	if w != nil && w.Dir != "" {
		_ = os.RemoveAll(w.Dir)
	}
}

// copyFS copia el subárbol root de src a dest.
func copyFS(src fs.FS, root, dest string) error {
	if _, err := fs.Stat(src, root); err != nil {
		return err
	}
	return fs.WalkDir(src, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	})
}
