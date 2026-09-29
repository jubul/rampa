// Package scaffold crea una application nueva a partir de un golden path embebido.
package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// Options describe qué generar. Org es la organización de GitHub donde vive rampa.
type Options struct {
	Name, Team, Type, Runtime, Org, Dir string
	Port                                int
}

// Run escribe el proyecto en <Dir>/<Name> y devuelve esa ruta.
// Los archivos .tmpl se renderizan con delimitadores [[ ]] para no chocar con ${{ }} de GitHub Actions.
func Run(templates fs.FS, o Options) (string, error) {
	if o.Name == "" || o.Team == "" {
		return "", errors.New("name y team son requeridos")
	}
	tpl := o.Type + "-" + o.Runtime
	if _, err := fs.Stat(templates, tpl); err != nil {
		return "", fmt.Errorf("no hay golden path para type=%s runtime=%s. Disponibles: %s", o.Type, o.Runtime, strings.Join(Available(templates), ", "))
	}
	dest := filepath.Join(o.Dir, o.Name)
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("%s ya existe", dest)
	}

	err := fs.WalkDir(templates, tpl, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, tpl)
		rel = strings.TrimPrefix(rel, "/")
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dest, rel), 0o755)
		}
		content, err := fs.ReadFile(templates, p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(rel, ".tmpl") {
			rel = strings.TrimSuffix(rel, ".tmpl")
			content, err = renderTemplate(p, content, o)
			if err != nil {
				return err
			}
		}
		return os.WriteFile(filepath.Join(dest, rel), content, 0o644)
	})
	if err != nil {
		_ = os.RemoveAll(dest)
		return "", err
	}
	return dest, nil
}

// Available lista los golden paths embebidos (type-runtime).
func Available(templates fs.FS) []string {
	entries, err := fs.ReadDir(templates, ".")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func renderTemplate(name string, content []byte, o Options) ([]byte, error) {
	t, err := template.New(name).Delims("[[", "]]").Option("missingkey=error").Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, o); err != nil {
		return nil, fmt.Errorf("template %s: %w", name, err)
	}
	return buf.Bytes(), nil
}
