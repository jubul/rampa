package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/manifest"
)

type applicationResponse struct {
	*store.Application
	ScopesDeclared []string `json:"scopes_declared"`
}

// parseManifest parsea y valida un rampa.yaml. Los errores van en details.
func parseManifest(raw string) (*manifest.Application, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, invalid("manifest es requerido: el contenido del rampa.yaml")
	}
	m, err := manifest.Parse([]byte(raw))
	if err != nil {
		return nil, invalid("rampa.yaml inválido", err.Error())
	}
	if errs := m.Validate(); len(errs) > 0 {
		details := make([]string, 0, len(errs))
		for _, e := range errs {
			details = append(details, e.Error())
		}
		return nil, invalid(fmt.Sprintf("rampa.yaml con %d error(es)", len(errs)), details...)
	}
	return m, nil
}

func declaredScopes(m *manifest.Application) []string {
	out := make([]string, 0, len(m.Spec.Scopes))
	for name := range m.Spec.Scopes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// validateRepoURL exige que repo_url sea vacío o una URL http(s) absoluta.
func validateRepoURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalid("repo_url debe ser una URL http(s) absoluta, como https://github.com/acme/checkout-api")
	}
	return nil
}

func manifestSHA(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func appResponse(row *store.Application) (applicationResponse, error) {
	m, err := manifest.Parse([]byte(row.Manifest))
	if err != nil {
		return applicationResponse{}, fmt.Errorf("el manifest guardado de %s no parsea: %w", row.Name, err)
	}
	return applicationResponse{Application: row, ScopesDeclared: declaredScopes(m)}, nil
}

func fillFromManifest(row *store.Application, m *manifest.Application, raw string) {
	row.Name = m.Metadata.Name
	row.Team = m.Metadata.Team
	row.Tier = m.Metadata.Tier
	row.Description = m.Metadata.Description
	row.Type = m.Spec.Type
	row.Runtime = m.Spec.Runtime
	row.Manifest = raw
	row.ManifestSHA = manifestSHA(raw)
}

func (a *API) importApp(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	var body struct {
		Manifest string `json:"manifest"`
		RepoURL  string `json:"repo_url"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	m, err := parseManifest(body.Manifest)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.RepoURL = strings.TrimSpace(body.RepoURL)
	if err := validateRepoURL(body.RepoURL); err != nil {
		a.fail(w, r, err, "")
		return
	}
	row := &store.Application{OrgID: oc.Org.ID, RepoURL: body.RepoURL}
	fillFromManifest(row, m, body.Manifest)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	created, err := a.Store.CreateApplication(ctx, row)
	if err != nil {
		if err == store.ErrConflict {
			a.fail(w, r, conflict(fmt.Sprintf("ya existe una application %q en la organización; usá PUT /apps/%s/manifest para actualizarla", row.Name, row.Name)), "")
			return
		}
		a.fail(w, r, err, "application")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "application.imported", "application", created.ID, "application "+created.Name+" importada", map[string]any{"name": created.Name, "team": created.Team, "scopes": declaredScopes(m)})
	resp, err := appResponse(created)
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (a *API) updateAppManifest(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	var body struct {
		Manifest string `json:"manifest"`
		RepoURL  string `json:"repo_url"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	m, err := parseManifest(body.Manifest)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.RepoURL = strings.TrimSpace(body.RepoURL)
	if err := validateRepoURL(body.RepoURL); err != nil {
		a.fail(w, r, err, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	row, err := a.Store.GetApplicationByName(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	if m.Metadata.Name != row.Name {
		a.fail(w, r, invalid(fmt.Sprintf("metadata.name no puede cambiar: la application es %q y el manifest dice %q", row.Name, m.Metadata.Name)), "")
		return
	}
	fillFromManifest(row, m, body.Manifest)
	row.RepoURL = body.RepoURL
	updated, err := a.Store.UpdateApplicationManifest(ctx, row)
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "application.updated", "application", updated.ID, "manifest de "+updated.Name+" actualizado", map[string]any{"manifest_sha": updated.ManifestSHA})
	resp, err := appResponse(updated)
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) listApps(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rows, err := a.Store.ListApplications(ctx, oc.Org.ID)
	if err != nil {
		a.fail(w, r, err, "applications")
		return
	}
	out := make([]applicationResponse, 0, len(rows))
	for i := range rows {
		resp, err := appResponse(&rows[i])
		if err != nil {
			a.fail(w, r, err, "application")
			return
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, items(out))
}

func (a *API) getApp(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	row, err := a.Store.GetApplicationByName(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	resp, err := appResponse(row)
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) deleteApp(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	row, err := a.Store.GetApplicationByName(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	if err := a.Store.DeleteApplication(ctx, oc.Org.ID, row.ID); err != nil {
		if err == store.ErrConflict {
			a.fail(w, r, conflict("la application tiene scopes desplegados; hacé undeploy primero"), "")
			return
		}
		a.fail(w, r, err, "application")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "application.deleted", "application", row.ID, "application "+row.Name+" borrada", map[string]string{"name": row.Name})
	w.WriteHeader(http.StatusNoContent)
}
