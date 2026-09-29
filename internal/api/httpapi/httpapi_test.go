package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/store"
)

func TestWriteErrorFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "invalid", "rampa.yaml inválido", "spec.type x", "spec.port y")
	if rec.Code != 400 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status/content-type: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "invalid" || env.Error.Message != "rampa.yaml inválido" || len(env.Error.Details) != 2 {
		t.Errorf("cuerpo: %+v", env)
	}
	// Sin details, la clave no aparece.
	rec = httptest.NewRecorder()
	writeError(rec, 404, "not_found", "x")
	if strings.Contains(rec.Body.String(), "details") {
		t.Errorf("details vacío no debería serializarse: %s", rec.Body.String())
	}
}

func TestFailMapsErrors(t *testing.T) {
	a := New(Deps{Log: slog.New(slog.NewTextHandler(new(strings.Builder), nil))})
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{store.ErrNotFound, 404, "not_found"},
		{store.ErrConflict, 409, "conflict"},
		{invalid("mal", "detalle"), 400, "invalid"},
		{conflict("dup"), 409, "conflict"},
		{forbidden("no"), 403, "forbidden"},
		{&auth.ForbiddenError{Min: auth.RoleAdmin}, 403, "forbidden"},
		{auth.ErrUnauthorized, 401, "unauthorized"},
		{errors.New("algo raro"), 500, "internal"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		a.fail(rec, req, c.err, "recurso")
		var env errorEnvelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if rec.Code != c.status || env.Error.Code != c.code {
			t.Errorf("%v: esperaba %d %s, tengo %d %s", c.err, c.status, c.code, rec.Code, env.Error.Code)
		}
		if c.code == "internal" && strings.Contains(env.Error.Message, "algo raro") {
			t.Error("un error interno no debe filtrar el mensaje original")
		}
	}
}

func TestDecode(t *testing.T) {
	var v struct {
		Image string `json:"image"`
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"image":"sha-1"}`))
	if err := decode(req, &v); err != nil || v.Image != "sha-1" {
		t.Fatalf("decode: %v %+v", err, v)
	}
	for name, body := range map[string]string{"vacío": "", "roto": "{", "espacios": "   "} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		err := decode(req, &v)
		var ae *apiError
		if !errors.As(err, &ae) || ae.code != "invalid" {
			t.Errorf("%s: esperaba invalid, tengo %v", name, err)
		}
	}
	big := strings.Repeat("a", maxBodyBytes+10)
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"image":"`+big+`"}`))
	if err := decode(req, &v); err == nil {
		t.Error("un cuerpo enorme debería fallar")
	}
}

func TestParseManifest(t *testing.T) {
	if _, err := parseManifest("   "); err == nil {
		t.Error("manifest vacío debería fallar")
	}
	_, err := parseManifest("apiVersion: rampa.dev/v1\nkind: Application\nmetadata:\n  name: x\n  team: t\nspec:\n  type: lambda\n  scopes:\n    dev: {}\n")
	var ae *apiError
	if !errors.As(err, &ae) || ae.code != "invalid" || len(ae.details) == 0 {
		t.Fatalf("esperaba invalid con details, tengo %v", err)
	}
	found := false
	for _, d := range ae.details {
		if strings.Contains(d, "spec.type") {
			found = true
		}
	}
	if !found {
		t.Errorf("details sin spec.type: %v", ae.details)
	}
	if _, err := parseManifest("apiVersion: rampa.dev/v1\nkind: Application\nmetadata: {name: x, team: t}\nspec:\n  type: worker\n  scopes:\n    dev: {}\n  campo_raro: 1\n"); err == nil {
		t.Error("campos desconocidos deberían fallar")
	}
	m, err := parseManifest("apiVersion: rampa.dev/v1\nkind: Application\nmetadata: {name: x, team: t}\nspec:\n  type: worker\n  scopes:\n    dev: {}\n    pr: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := declaredScopes(m); len(got) != 2 || got[0] != "dev" || got[1] != "pr" {
		t.Errorf("declaredScopes: %v", got)
	}
}

func TestSlugAndNames(t *testing.T) {
	for _, ok := range []string{"acme", "acme-prod", "a1b"} {
		if !slugRe.MatchString(ok) {
			t.Errorf("%q debería ser un slug válido", ok)
		}
	}
	for _, bad := range []string{"ab", "Acme", "-acme", "acme-", strings.Repeat("a", 41)} {
		if slugRe.MatchString(bad) {
			t.Errorf("%q no debería ser un slug válido", bad)
		}
	}
	if envForScope("prod") != store.EnvProd || envForScope("staging") != store.EnvNonprod || envForScope("pr-1") != store.EnvNonprod {
		t.Error("envForScope incorrecto")
	}
}

func TestInstallCommand(t *testing.T) {
	a := New(Deps{})
	a.Config.ChartRepo = "oci://x/charts/rampa-agent"
	a.Config.PublicURL = "https://api.rampa.dev"
	a.Config.AgentImage = "ghcr.io/acme/rampa-agent:1.2.3"
	cmd := a.installCommand("rampa_cluster_abc")
	// Las claves son las de charts/rampa-agent/values.yaml: api.url, cluster.token, image.repository, image.tag.
	// image.* con --set-string: un tag numérico con --set sería un entero y values.schema.json lo rechaza.
	want := "helm upgrade --install rampa-agent oci://x/charts/rampa-agent --namespace rampa-system --create-namespace --set api.url=https://api.rampa.dev --set cluster.token=rampa_cluster_abc --set-string image.repository=ghcr.io/acme/rampa-agent --set-string image.tag=1.2.3"
	if cmd != want {
		t.Errorf("install_command:\n%s\nquería:\n%s", cmd, want)
	}
	a.Config.AgentImage = "ghcr.io/acme/rampa-agent:20240101"
	if got := a.installCommand("t"); !strings.Contains(got, "--set-string image.tag=20240101") || strings.Contains(got, "--set image.") {
		t.Errorf("un tag numérico tiene que ir con --set-string: %s", got)
	}
	a.Config.AgentImage = ""
	if got := a.installCommand("t"); strings.Contains(got, "image.") {
		t.Errorf("sin RAMPA_AGENT_IMAGE no debería haber --set image.*: %s", got)
	}
}

func TestValidateImage(t *testing.T) {
	for _, ok := range []string{"sha-abc1234", "v1.2.3", "20240101", "latest", "a_b-c.d", strings.Repeat("a", 128),
		// Referencias completas: las valida el render, no acá.
		"ghcr.io/acme/app:v1:raro", "registry:5000/app:tag"} {
		if err := validateImage(ok); err != nil {
			t.Errorf("%q debería ser válido: %v", ok, err)
		}
	}
	for _, bad := range []string{"v1:latest", ".hidden", "-dash", "tag con espacios", "tag@sha256", strings.Repeat("a", 129)} {
		err := validateImage(bad)
		var ae *apiError
		if !errors.As(err, &ae) || ae.code != "invalid" {
			t.Errorf("%q debería ser invalid, tengo %v", bad, err)
		}
	}
}

func TestValidateRepoURL(t *testing.T) {
	for _, ok := range []string{"", "https://github.com/acme/checkout-api", "http://git.local/x.git"} {
		if err := validateRepoURL(ok); err != nil {
			t.Errorf("%q debería ser válido: %v", ok, err)
		}
	}
	for _, bad := range []string{"git@github.com:acme/x.git", "ftp://x/y", "javascript:alert(1)", "github.com/acme/x", "https://"} {
		err := validateRepoURL(bad)
		var ae *apiError
		if !errors.As(err, &ae) || ae.code != "invalid" {
			t.Errorf("%q debería ser invalid, tengo %v", bad, err)
		}
	}
}

func TestSplitImage(t *testing.T) {
	cases := map[string][2]string{
		"ghcr.io/acme/rampa-agent:1.2.3": {"ghcr.io/acme/rampa-agent", "1.2.3"},
		"ghcr.io/acme/rampa-agent":       {"ghcr.io/acme/rampa-agent", ""},
		"localhost:5000/rampa-agent":     {"localhost:5000/rampa-agent", ""},
		"localhost:5000/rampa-agent:dev": {"localhost:5000/rampa-agent", "dev"},
		"":                               {"", ""},
	}
	for in, want := range cases {
		repo, tag := splitImage(in)
		if repo != want[0] || tag != want[1] {
			t.Errorf("splitImage(%q) = %q, %q; quería %q, %q", in, repo, tag, want[0], want[1])
		}
	}
}

func TestDevCORS(t *testing.T) {
	h := devCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/me", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	h.ServeHTTP(rec, req)
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("preflight localhost: %d %v", rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("un origen ajeno no debe recibir CORS")
	}
}
