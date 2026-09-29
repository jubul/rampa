package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"rampa.dev/cmd/rampa/templates"
	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/scaffold"
)

// DefaultGitHubOrg es la organización de GitHub que va en los workflows del
// scaffold cuando ni la query ni la org la definen.
const DefaultGitHubOrg = "CHANGEME"

type specRequest struct {
	Spec    *manifest.ApplicationSpec `json:"spec"`
	RepoURL string                    `json:"repo_url"`
}

// manifestFromSpec convierte el spec al rampa.yaml canónico y lo valida igual
// que un manifest pegado. Devuelve la Application, el YAML y la lista de
// problemas (vacía si es válido). El YAML se devuelve aunque haya errores, para
// que la web lo muestre mientras el usuario corrige.
func manifestFromSpec(spec manifest.ApplicationSpec) (*manifest.Application, string, []string, error) {
	details := []string{}
	app, err := manifest.FromSpec(spec)
	if err != nil {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, e := range joined.Unwrap() {
				details = append(details, e.Error())
			}
		} else {
			details = append(details, err.Error())
		}
	}
	for _, e := range app.Validate() {
		details = append(details, e.Error())
	}
	raw, err := manifest.MarshalManifest(app)
	if err != nil {
		return nil, "", nil, err
	}
	return app, string(raw), details, nil
}

func decodeSpec(r *http.Request) (specRequest, error) {
	var body specRequest
	if err := decode(r, &body); err != nil {
		return body, err
	}
	if body.Spec == nil {
		return body, invalid("spec es requerido: el ApplicationSpec del formulario")
	}
	body.RepoURL = strings.TrimSpace(body.RepoURL)
	return body, nil
}

// previewApp convierte el spec y devuelve el YAML con su validación, sin guardar.
func (a *API) previewApp(w http.ResponseWriter, r *http.Request) {
	body, err := decodeSpec(r)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	_, raw, details, err := manifestFromSpec(*body.Spec)
	if err != nil {
		a.fail(w, r, err, "spec")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"manifest": raw, "valid": len(details) == 0, "details": details})
}

// createAppFromSpec es POST /apps con el manifest derivado del spec.
func (a *API) createAppFromSpec(w http.ResponseWriter, r *http.Request) {
	body, err := decodeSpec(r)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	m, raw, details, err := manifestFromSpec(*body.Spec)
	if err != nil {
		a.fail(w, r, err, "spec")
		return
	}
	if len(details) > 0 {
		a.fail(w, r, invalid(fmt.Sprintf("spec con %d error(es)", len(details)), details...), "")
		return
	}
	if err := validateRepoURL(body.RepoURL); err != nil {
		a.fail(w, r, err, "")
		return
	}
	a.createApplication(w, r, m, raw, body.RepoURL)
}

// getAppSpec deriva el spec del manifest guardado (pegado o generado).
func (a *API) getAppSpec(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	row, m, err := a.loadApp(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"spec": manifest.ToSpec(m), "manifest": row.Manifest})
}

// updateAppSpec es PUT /manifest con el manifest derivado del spec.
func (a *API) updateAppSpec(w http.ResponseWriter, r *http.Request) {
	body, err := decodeSpec(r)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	m, raw, details, err := manifestFromSpec(*body.Spec)
	if err != nil {
		a.fail(w, r, err, "spec")
		return
	}
	if len(details) > 0 {
		a.fail(w, r, invalid(fmt.Sprintf("spec con %d error(es)", len(details)), details...), "")
		return
	}
	if err := validateRepoURL(body.RepoURL); err != nil {
		a.fail(w, r, err, "")
		return
	}
	a.replaceManifest(w, r, chi.URLParam(r, "app"), m, raw, body.RepoURL)
}

// scaffoldZip arma el proyecto inicial del golden path de la app con su
// rampa.yaml real y los workflows apuntando a <github_org>/rampa.
func (a *API) scaffoldZip(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	row, m, err := a.loadApp(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	key := m.Spec.Type + "-" + m.Spec.Runtime
	available := scaffold.Available(templates.FS)
	found := false
	for _, av := range available {
		if av == key {
			found = true
		}
	}
	if !found {
		a.fail(w, r, invalid(fmt.Sprintf("no hay golden path para type=%s runtime=%s. Disponibles: %s", m.Spec.Type, m.Spec.Runtime, strings.Join(available, ", "))), "")
		return
	}
	githubOrg := strings.TrimSpace(r.URL.Query().Get("github_org"))
	if githubOrg == "" {
		githubOrg = strings.TrimSpace(oc.Org.Settings.GitHubOrg)
	}
	if githubOrg == "" {
		githubOrg = DefaultGitHubOrg
	}
	raw, err := manifest.MarshalManifest(m)
	if err != nil {
		a.fail(w, r, err, "manifest")
		return
	}
	buf, err := buildScaffoldZip(row.Name, scaffold.Options{
		Name: row.Name, Team: row.Team, Type: m.Spec.Type, Runtime: m.Spec.Runtime, Org: githubOrg, Port: m.Spec.Port,
	}, raw)
	if err != nil {
		a.fail(w, r, err, "scaffold")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", row.Name+".zip"))
	w.Header().Set("Content-Length", fmt.Sprint(buf.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// buildScaffoldZip genera el golden path en un directorio temporal, reemplaza
// el rampa.yaml por el real y lo empaqueta con root como carpeta raíz.
func buildScaffoldZip(root string, o scaffold.Options, rampaYAML []byte) (*bytes.Buffer, error) {
	tmp, err := os.MkdirTemp("", "rampa-scaffold-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	o.Dir = tmp
	dest, err := scaffold.Run(templates.FS, o)
	if err != nil {
		return nil, fmt.Errorf("generando el scaffold: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "rampa.yaml"), rampaYAML, 0o644); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	now := time.Now().UTC()
	err = filepath.WalkDir(dest, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dest, p)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: root + "/" + filepath.ToSlash(rel), Method: zip.Deflate, Modified: now}
		hdr.SetMode(0o644)
		f, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = f.Write(content)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}
