package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"rampa.dev/internal/cli/apiclient"
	"rampa.dev/internal/cli/config"
)

const testManifest = `apiVersion: rampa.dev/v1
kind: Application
metadata:
  name: checkout-api
  team: payments
  tier: 2
spec:
  type: api
  runtime: go
  port: 8080
  scopes:
    dev:
      size: S
      replicas: 1
    prod:
      size: M
      replicas: 3
      domain: checkout.example.com
    pr:
      size: S
      replicas: 1
      ttl: 72h
`

// fakeAPI implementa las rutas de docs/api.md que usa el CLI y registra lo que recibe.
type fakeAPI struct {
	t *testing.T
	*httptest.Server

	mu        sync.Mutex
	requests  []string          // "METHOD /path"
	bodies    map[string][]byte // por "METHOD /path", el último cuerpo
	appExists bool
	depStatus string            // status que devuelve GET /deployments/{id}
	sseEvents []apiclient.Event // lo que emite el stream
	sseHold   bool              // mantener el stream abierto tras emitir

	streamFailStatus int // si no es 0, las primeras streamFailures conexiones al stream responden este status
	streamFailures   int
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, bodies: map[string][]byte{}, depStatus: "queued"}
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer rampa_test" {
				writeErr(w, 401, "unauthorized", "token inválido")
				return
			}
			body, _ := readBody(r)
			f.mu.Lock()
			key := r.Method + " " + r.URL.Path
			f.requests = append(f.requests, key)
			f.bodies[key] = body
			f.mu.Unlock()
			h(w, r)
		}
	}
	mux.HandleFunc("GET /v1/me", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, apiclient.Me{
			User: apiclient.User{ID: "u1", Email: "juan@acme.com", Name: "Juan"},
			Memberships: []apiclient.Membership{
				{Org: apiclient.Org{ID: "o1", Slug: "acme", Name: "Acme"}, Role: "owner"},
				{Org: apiclient.Org{ID: "o2", Slug: "otra", Name: "Otra"}, Role: "viewer"},
			},
		})
	}))
	mux.HandleFunc("POST /v1/orgs/{org}/apps", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		exists := f.appExists
		f.appExists = true
		f.mu.Unlock()
		if exists {
			writeErr(w, 409, "conflict", "ya existe una application checkout-api en acme")
			return
		}
		writeJSON(w, 201, apiclient.Application{ID: "a1", Name: "checkout-api", Team: "payments", Tier: 2, Type: "api", Runtime: "go", ScopesDeclared: []string{"dev", "prod", "pr"}, ManifestSHA: "abc"})
	}))
	mux.HandleFunc("PUT /v1/orgs/{org}/apps/{app}/manifest", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, apiclient.Application{ID: "a1", Name: r.PathValue("app"), Team: "payments", Tier: 2, Type: "api", Runtime: "go", ScopesDeclared: []string{"dev", "prod", "pr"}, ManifestSHA: "def"})
	}))
	mux.HandleFunc("GET /v1/orgs/{org}/apps/{app}/scopes", auth(func(w http.ResponseWriter, r *http.Request) {
		app := r.PathValue("app")
		writeJSON(w, 200, map[string]any{"items": []apiclient.Scope{
			{Name: "dev", Declared: true, ClusterID: "c1", Namespace: app + "-dev", URL: "https://checkout-api-dev.apps.example.internal", Status: "healthy",
				CurrentDeployment: &apiclient.Deployment{ID: "d0", Image: "123.dkr.ecr.us-east-1.amazonaws.com/rampa/checkout-api:sha-0000000", Status: "healthy"}},
			{Name: "prod", Declared: true, Namespace: app + "-prod", URL: "https://checkout.example.com", Status: "unbound"},
			{Name: "pr-7", Ephemeral: true, Namespace: app + "-pr-7", URL: "https://checkout-api-pr-7.apps.example.internal", Status: "deploying", Size: "S", Replicas: 1, Strategy: "rolling"},
		}})
	}))
	mux.HandleFunc("PUT /v1/orgs/{org}/apps/{app}/scopes/{scope}", auth(func(w http.ResponseWriter, r *http.Request) {
		var req apiclient.BindScopeRequest
		_ = json.Unmarshal(f.lastBody(r), &req)
		writeJSON(w, 200, apiclient.Scope{Name: r.PathValue("scope"), ClusterID: req.ClusterID, Namespace: r.PathValue("app") + "-" + r.PathValue("scope"), Status: "idle"})
	}))
	mux.HandleFunc("DELETE /v1/orgs/{org}/apps/{app}/scopes/{scope}", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("scope") == "pr-404" {
			writeErr(w, 404, "not_found", "scope pr-404 no existe")
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /v1/orgs/{org}/apps/{app}/scopes/{scope}/deployments", auth(func(w http.ResponseWriter, r *http.Request) {
		var req apiclient.CreateDeploymentRequest
		if err := json.Unmarshal(f.lastBody(r), &req); err != nil || req.Image == "" {
			writeErr(w, 400, "invalid", "image es obligatorio")
			return
		}
		writeJSON(w, 201, apiclient.Deployment{ID: "d1", App: r.PathValue("app"), Scope: r.PathValue("scope"), ClusterID: "c1",
			Image: "123.dkr.ecr.us-east-1.amazonaws.com/rampa/" + r.PathValue("app") + ":" + req.Image, Status: "queued", Message: req.Message, CreatedAt: time.Now()})
	}))
	mux.HandleFunc("GET /v1/orgs/{org}/deployments/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		st := f.depStatus
		f.mu.Unlock()
		msg := ""
		if st == "failed" {
			msg = "ImagePullBackOff en checkout-api-xyz"
		}
		writeJSON(w, 200, apiclient.Deployment{ID: r.PathValue("id"), App: "checkout-api", Scope: "dev", Image: "img:sha-1", Status: st, Message: msg})
	}))
	mux.HandleFunc("GET /v1/orgs/{org}/events/stream", auth(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("subject_type") != "deployment" || q.Get("subject_id") != "d1" {
			t.Errorf("stream sin filtro correcto: %s", r.URL.RawQuery)
		}
		f.mu.Lock()
		if f.streamFailures > 0 {
			f.streamFailures--
			st := f.streamFailStatus
			f.mu.Unlock()
			writeErr(w, st, "unavailable", "falla simulada del stream")
			return
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		fmt.Fprint(w, ": ping\n\n")
		f.mu.Lock()
		events := append([]apiclient.Event(nil), f.sseEvents...)
		hold := f.sseHold
		f.mu.Unlock()
		for _, ev := range events {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, b)
			if fl != nil {
				fl.Flush()
			}
			// Al emitir el terminal, GET pasa a devolver ese estado, como haría la API real.
			switch ev.Kind {
			case apiclient.EventDeploymentHealthy, apiclient.EventDeploymentFailed, apiclient.EventDeploymentCancelled:
				f.mu.Lock()
				f.depStatus = statusFromEvent(ev.Kind)
				f.mu.Unlock()
			}
			time.Sleep(10 * time.Millisecond)
		}
		if hold {
			<-r.Context().Done()
		}
	}))
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Server.Close)
	return f
}

func (f *fakeAPI) lastBody(r *http.Request) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[r.Method+" "+r.URL.Path]
}

func (f *fakeAPI) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func readBody(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

// isolate aísla HOME, config y variables de sesión. Devuelve el HOME temporal.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(config.EnvConfig, "")
	t.Setenv(config.EnvAPI, "")
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvOrg, "")
	t.Setenv("GITHUB_OUTPUT", "")
	return home
}

func withSession(t *testing.T, f *fakeAPI) {
	t.Helper()
	t.Setenv(config.EnvAPI, f.URL)
	t.Setenv(config.EnvToken, "rampa_test")
	t.Setenv(config.EnvOrg, "acme")
}

func writeManifest(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rampa.yaml")
	if err := os.WriteFile(p, []byte(testManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// run ejecuta el CLI con args y devuelve stdout, stderr y el error.
func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd("test")
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func has(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

func TestLoginSavesConfig(t *testing.T) {
	home := isolate(t)
	f := newFakeAPI(t)
	out, _, err := run(t, "login", "--api", f.URL+"/", "--token", "rampa_test", "--org", "acme")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out, "juan@acme.com") || !strings.Contains(out, "acme") || !strings.Contains(out, "otra") {
		t.Errorf("salida de login:\n%s", out)
	}
	if strings.Contains(out, "rampa_test") {
		t.Errorf("el token completo no debería imprimirse:\n%s", out)
	}
	p := filepath.Join(home, ".config", "rampa", "config.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("permisos %o", st.Mode().Perm())
	}
	var cfg config.Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.API != f.URL || cfg.Token != "rampa_test" || cfg.Org != "acme" {
		t.Errorf("config guardada: %+v", cfg)
	}
}

func TestLoginRejectsBadTokenAndUnknownOrg(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	if _, _, err := run(t, "login", "--api", f.URL, "--token", "malo"); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("esperaba unauthorized, tengo %v", err)
	}
	if _, _, err := run(t, "login", "--api", f.URL, "--token", "rampa_test", "--org", "nadie"); err == nil || !strings.Contains(err.Error(), "nadie") {
		t.Errorf("esperaba error por org desconocida, tengo %v", err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".config", "rampa", "config.yaml")); err == nil {
		t.Error("no debería haber guardado config con login fallido")
	}
}

func TestLoginMultipleOrgsWithoutDefault(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	out, _, err := run(t, "login", "--api", f.URL, "--token", "rampa_test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "varias organizaciones") {
		t.Errorf("debería avisar que falta --org:\n%s", out)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Org != "" {
		t.Errorf("org debería quedar vacío, tengo %q", cfg.Org)
	}
}

func TestAppsImportFallsBackToPut(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	m := writeManifest(t)

	out, _, err := run(t, "apps", "import", "-f", m, "--repo", "https://github.com/acme/checkout-api")
	if err != nil {
		t.Fatalf("primer import: %v", err)
	}
	if !strings.Contains(out, "importada") {
		t.Errorf("salida:\n%s", out)
	}
	var req apiclient.CreateAppRequest
	if err := json.Unmarshal(f.bodies["POST /v1/orgs/acme/apps"], &req); err != nil {
		t.Fatal(err)
	}
	if req.Manifest != testManifest || req.RepoURL != "https://github.com/acme/checkout-api" {
		t.Errorf("cuerpo del POST: %+v", req)
	}

	out, _, err = run(t, "apps", "import", "-f", m)
	if err != nil {
		t.Fatalf("segundo import: %v", err)
	}
	if !strings.Contains(out, "actualizada") {
		t.Errorf("salida:\n%s", out)
	}
	calls := f.calls()
	if !has(calls, "PUT /v1/orgs/acme/apps/checkout-api/manifest") {
		t.Errorf("esperaba fallback a PUT, llamadas: %v", calls)
	}
	var put apiclient.PutManifestRequest
	_ = json.Unmarshal(f.bodies["PUT /v1/orgs/acme/apps/checkout-api/manifest"], &put)
	if put.Manifest != testManifest {
		t.Error("el PUT tiene que llevar el manifest completo")
	}
}

func TestAppsImportRejectsInvalidManifestLocally(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	p := filepath.Join(t.TempDir(), "rampa.yaml")
	if err := os.WriteFile(p, []byte("apiVersion: rampa.dev/v1\nkind: Application\nmetadata:\n  name: x\nspec:\n  type: nada\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "apps", "import", "-f", p); err == nil {
		t.Fatal("esperaba error de validación")
	}
	if len(f.calls()) != 0 {
		t.Errorf("no debería haber llamado a la API: %v", f.calls())
	}
}

func TestScopeBind(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	out, _, err := run(t, "scope", "bind", "--app", "checkout-api", "--scope", "prod", "--cluster", "c-prod")
	if err != nil {
		t.Fatal(err)
	}
	var req apiclient.BindScopeRequest
	_ = json.Unmarshal(f.bodies["PUT /v1/orgs/acme/apps/checkout-api/scopes/prod"], &req)
	if req.ClusterID != "c-prod" {
		t.Errorf("cuerpo: %+v", req)
	}
	if !strings.Contains(out, "c-prod") {
		t.Errorf("salida:\n%s", out)
	}
}

func TestDeployWithoutSessionExplainsLogin(t *testing.T) {
	isolate(t)
	m := writeManifest(t)
	_, _, err := run(t, "deploy", "-f", m, "--scope", "dev", "--image", "sha-1")
	if err == nil || !strings.Contains(err.Error(), "rampa login") {
		t.Errorf("esperaba error que explique rampa login, tengo %v", err)
	}
}

func TestDeployPostsAndPrintsURL(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	m := writeManifest(t)
	ghOut := filepath.Join(t.TempDir(), "out.txt")
	t.Setenv("GITHUB_OUTPUT", ghOut)

	out, _, err := run(t, "deploy", "-f", m, "--scope", "dev", "--image", "sha-a1b2c3d", "-m", "prueba")
	if err != nil {
		t.Fatalf("deploy: %v\n%s", err, out)
	}
	var req apiclient.CreateDeploymentRequest
	if err := json.Unmarshal(f.bodies["POST /v1/orgs/acme/apps/checkout-api/scopes/dev/deployments"], &req); err != nil {
		t.Fatal(err)
	}
	if req.Image != "sha-a1b2c3d" || req.Message != "prueba" {
		t.Errorf("cuerpo del POST: %+v", req)
	}
	if !strings.Contains(out, "deployment d1 creado") || !strings.Contains(out, "https://checkout-api-dev.apps.example.internal") {
		t.Errorf("salida:\n%s", out)
	}
	if has(f.calls(), "GET /v1/orgs/acme/events/stream") {
		t.Error("sin --wait no debería abrir el stream")
	}
	gh, err := os.ReadFile(ghOut)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"url=https://checkout-api-dev.apps.example.internal", "namespace=checkout-api-dev", "deployment_id=d1", "image=123.dkr.ecr"} {
		if !strings.Contains(string(gh), want) {
			t.Errorf("GITHUB_OUTPUT sin %q:\n%s", want, gh)
		}
	}
}

func TestDeployWaitHealthy(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	f.sseEvents = []apiclient.Event{
		{ID: "e1", Kind: "deployment.applying", SubjectType: "deployment", SubjectID: "d1", Message: "aplicando 9 objetos", CreatedAt: time.Now()},
		{ID: "e2", Kind: "deployment.progressing", SubjectType: "deployment", SubjectID: "d1", Message: "1/1 réplicas listas", CreatedAt: time.Now()},
		{ID: "e3", Kind: "deployment.healthy", SubjectType: "deployment", SubjectID: "d1", Message: "workload sano", CreatedAt: time.Now()},
		{ID: "e4", Kind: "deployment.failed", SubjectType: "deployment", SubjectID: "d1", Message: "no debería procesarse", CreatedAt: time.Now()},
	}
	f.sseHold = true
	out, _, err := run(t, "deploy", "--app", "checkout-api", "--scope", "dev", "--image", "sha-1", "--wait", "--timeout", "10s")
	if err != nil {
		t.Fatalf("deploy --wait: %v\n%s", err, out)
	}
	for _, want := range []string{"deployment.applying", "deployment.progressing", "deployment.healthy", "1/1 réplicas listas", "checkout-api/dev healthy"} {
		if !strings.Contains(out, want) {
			t.Errorf("salida sin %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no debería procesarse") {
		t.Errorf("siguió leyendo después del terminal:\n%s", out)
	}
}

func TestDeployWaitFailedExitsWithError(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	f.sseEvents = []apiclient.Event{
		{ID: "e1", Kind: "deployment.progressing", SubjectType: "deployment", SubjectID: "d1", Message: "0/1 réplicas listas", CreatedAt: time.Now()},
		{ID: "e2", Kind: "deployment.failed", SubjectType: "deployment", SubjectID: "d1", Message: "ImagePullBackOff", CreatedAt: time.Now()},
	}
	f.sseHold = true
	ghOut := filepath.Join(t.TempDir(), "out.txt")
	t.Setenv("GITHUB_OUTPUT", ghOut)
	out, _, err := run(t, "deploy", "--app", "checkout-api", "--scope", "dev", "--image", "sha-1", "--wait", "--timeout", "10s")
	if err == nil {
		t.Fatalf("esperaba error con deployment.failed\n%s", out)
	}
	if !strings.Contains(err.Error(), "failed") || !strings.Contains(err.Error(), "ImagePullBackOff") {
		t.Errorf("error: %v", err)
	}
	if !strings.Contains(out, "deployment.failed") {
		t.Errorf("salida:\n%s", out)
	}
	gh, _ := os.ReadFile(ghOut)
	if !strings.Contains(string(gh), "status=failed") {
		t.Errorf("GITHUB_OUTPUT debería tener status=failed:\n%s", gh)
	}
}

func TestDeployWaitAlreadyTerminalBeforeStream(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	f.depStatus = "healthy" // el deploy ya terminó antes de conectar al stream
	out, _, err := run(t, "deploy", "--app", "checkout-api", "--scope", "dev", "--image", "sha-1", "--wait")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if has(f.calls(), "GET /v1/orgs/acme/events/stream") {
		t.Error("no hacía falta abrir el stream")
	}
}

func TestDeployWaitReconnectsAfterServerCloses(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	old := streamRetryDelay
	streamRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { streamRetryDelay = old })
	// El stream cierra sin terminal; en la segunda vuelta el GET ya lo ve healthy.
	f.sseEvents = []apiclient.Event{{Kind: "deployment.queued", SubjectType: "deployment", SubjectID: "d1"}}
	f.sseHold = false
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.mu.Lock()
		f.depStatus = "healthy"
		f.mu.Unlock()
	}()
	out, _, err := run(t, "deploy", "--app", "checkout-api", "--scope", "dev", "--image", "sha-1", "--wait", "--timeout", "5s")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestDeployWaitTimeout(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	f.sseHold = true
	_, _, err := run(t, "deploy", "--app", "checkout-api", "--scope", "dev", "--image", "sha-1", "--wait", "--timeout", "300ms")
	if err == nil || !strings.Contains(err.Error(), "sigue en queued") {
		t.Errorf("esperaba error de timeout, tengo %v", err)
	}
}

func TestDeployWaitReconnectsAfterStreamError(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	oldMin, oldMax := streamBackoffMin, streamBackoffMax
	streamBackoffMin, streamBackoffMax = 10*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { streamBackoffMin, streamBackoffMax = oldMin, oldMax })
	f.streamFailStatus, f.streamFailures = 502, 2
	f.sseEvents = []apiclient.Event{{ID: "e1", Kind: "deployment.healthy", SubjectType: "deployment", SubjectID: "d1", Message: "workload sano"}}
	f.sseHold = true
	out, _, err := run(t, "deploy", "--app", "checkout-api", "--scope", "dev", "--image", "sha-1", "--wait", "--timeout", "10s")
	if err != nil {
		t.Fatalf("un 502 del stream no debería abortar: %v\n%s", err, out)
	}
	n := 0
	for _, c := range f.calls() {
		if c == "GET /v1/orgs/acme/events/stream" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("esperaba 3 conexiones al stream, tengo %d", n)
	}
}

func TestDeployWaitAbortsOnFatalStreamError(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	f.streamFailStatus, f.streamFailures = 403, 5
	_, _, err := run(t, "deploy", "--app", "checkout-api", "--scope", "dev", "--image", "sha-1", "--wait", "--timeout", "5s")
	if err == nil || !strings.Contains(err.Error(), "siguiendo los eventos") {
		t.Errorf("un 403 debería abortar, tengo %v", err)
	}
}

func TestDeployWaitTimeoutReportsRealStatus(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	f.depStatus = "progressing"
	f.sseHold = true
	ghOut := filepath.Join(t.TempDir(), "out.txt")
	t.Setenv("GITHUB_OUTPUT", ghOut)
	_, _, err := run(t, "deploy", "--app", "checkout-api", "--scope", "dev", "--image", "sha-1", "--wait", "--timeout", "300ms")
	if err == nil || !strings.Contains(err.Error(), "sigue en progressing") {
		t.Errorf("el timeout debería reportar el estado real, tengo %v", err)
	}
	gh, _ := os.ReadFile(ghOut)
	if !strings.Contains(string(gh), "status=progressing") {
		t.Errorf("GITHUB_OUTPUT:\n%s", gh)
	}
}

func TestDeployLegacyGitOpsStillWorks(t *testing.T) {
	isolate(t)
	m := writeManifest(t)
	deploys := t.TempDir()
	out, errOut, err := run(t, "deploy", "-f", m, "--scope", "dev", "--image", "sha-1", "--deploys-repo", deploys)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(deploys, "apps", "checkout-api", "dev", "values.yaml")); err != nil {
		t.Errorf("values.yaml no generado: %v", err)
	}
	if !strings.Contains(errOut, "legado") {
		t.Errorf("debería avisar que es modo legado:\n%s", errOut)
	}
}

func TestUndeployCallsDelete(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t)
	withSession(t, f)
	out, _, err := run(t, "undeploy", "--app", "checkout-api", "--scope", "pr-42")
	if err != nil {
		t.Fatal(err)
	}
	if !has(f.calls(), "DELETE /v1/orgs/acme/apps/checkout-api/scopes/pr-42") {
		t.Errorf("llamadas: %v", f.calls())
	}
	if !strings.Contains(out, "checkout-api-pr-42") {
		t.Errorf("salida:\n%s", out)
	}

	if _, _, err := run(t, "undeploy", "--app", "checkout-api", "--scope", "prod"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("scope permanente sin --force debería fallar, tengo %v", err)
	}
	if has(f.calls(), "DELETE /v1/orgs/acme/apps/checkout-api/scopes/prod") {
		t.Error("no debería haber llamado a DELETE de prod")
	}

	out, _, err = run(t, "undeploy", "--app", "checkout-api", "--scope", "pr-404")
	if err != nil || !strings.Contains(out, "Nada que hacer") {
		t.Errorf("not_found debería ser idempotente: err=%v\n%s", err, out)
	}
}

func TestUndeployWithoutSessionExplainsLogin(t *testing.T) {
	isolate(t)
	_, _, err := run(t, "undeploy", "--app", "checkout-api", "--scope", "pr-1")
	if err == nil || !strings.Contains(err.Error(), "rampa login") {
		t.Errorf("esperaba error que explique rampa login, tengo %v", err)
	}
}

func TestScopeListOfflineAndOnline(t *testing.T) {
	isolate(t)
	m := writeManifest(t)
	out, _, err := run(t, "scope", "list", "-f", m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "STATUS") || !strings.Contains(out, "checkout-api-dev") {
		t.Errorf("offline:\n%s", out)
	}

	f := newFakeAPI(t)
	withSession(t, f)
	out, _, err = run(t, "scope", "list", "-f", m)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"STATUS", "IMAGEN", "healthy", "rampa/checkout-api:sha-0000000", "unbound", "pr-7", "deploying", "https://checkout-api-dev.apps.example.internal"} {
		if !strings.Contains(out, want) {
			t.Errorf("online sin %q:\n%s", want, out)
		}
	}

	out, _, err = run(t, "scope", "list", "-f", m, "--offline")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "STATUS") {
		t.Errorf("--offline no debería consultar la API:\n%s", out)
	}
}

func TestScopeListOnlineWithBadTokenFallsBack(t *testing.T) {
	isolate(t)
	m := writeManifest(t)
	f := newFakeAPI(t)
	withSession(t, f)
	t.Setenv(config.EnvToken, "invalido")
	out, errOut, err := run(t, "scope", "list", "-f", m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "aviso") || strings.Contains(out, "STATUS") {
		t.Errorf("debería avisar y seguir offline:\nstdout: %s\nstderr: %s", out, errOut)
	}
}

func TestScopeListSessionWithoutOrgFallsBackOffline(t *testing.T) {
	isolate(t)
	m := writeManifest(t)
	t.Setenv(config.EnvAPI, "http://127.0.0.1:1")
	t.Setenv(config.EnvToken, "rampa_x")
	out, errOut, err := run(t, "scope", "list", "-f", m)
	if err != nil {
		t.Fatalf("sin org debería mostrar la tabla offline: %v", err)
	}
	if !strings.Contains(errOut, "aviso") || strings.Contains(out, "STATUS") || !strings.Contains(out, "checkout-api-dev") {
		t.Errorf("stdout: %s\nstderr: %s", out, errOut)
	}
}

func TestInitHelpOnlyListsExistingGoldenPaths(t *testing.T) {
	isolate(t)
	out, _, err := run(t, "init", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "cron") || strings.Contains(out, "python") {
		t.Errorf("la ayuda anuncia golden paths inexistentes:\n%s", out)
	}
}
