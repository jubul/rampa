package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/scaffold"
)

// TestScaffoldApiGo genera una app desde el golden path embebido y verifica
// que el rampa.yaml resultante sea válido y que no queden delimitadores sin renderizar.
func TestScaffoldApiGo(t *testing.T) {
	sub, err := fs.Sub(templates, "templates")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dest, err := scaffold.Run(sub, scaffold.Options{
		Name: "demo-api", Team: "plataforma", Type: "api", Runtime: "go", Org: "acme", Port: 8080, Dir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	app, err := manifest.LoadValid(filepath.Join(dest, "rampa.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if app.Metadata.Name != "demo-api" || app.Metadata.Team != "plataforma" {
		t.Errorf("metadata: %+v", app.Metadata)
	}
	for _, f := range []string{"Dockerfile", "go.mod", "main.go", "main_test.go", ".github/workflows/ci.yml", ".github/workflows/pr-cleanup.yml", "README.md", ".gitignore"} {
		b, err := os.ReadFile(filepath.Join(dest, f))
		if err != nil {
			t.Errorf("falta %s: %v", f, err)
			continue
		}
		if string(b) == "" {
			t.Errorf("%s está vacío", f)
		}
		if i := indexOf(b, "[["); i >= 0 {
			t.Errorf("%s tiene un delimitador sin renderizar cerca de: %q", f, b[i:min(len(b), i+40)])
		}
	}
	if len(scaffold.Available(sub)) == 0 {
		t.Error("no hay golden paths embebidos")
	}
}

func indexOf(b []byte, s string) int {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return i
		}
	}
	return -1
}
