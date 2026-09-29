package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"

	"rampa.dev/internal/api/config"
	"rampa.dev/internal/api/httpapi"
	"rampa.dev/internal/api/hub"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/protocol"
	"rampa.dev/pkg/provider"
)

// fakeProvider reemplaza a AWS en el registro durante el test: verifica sin
// red y devuelve un registry determinístico.
type fakeProvider struct{}

func (fakeProvider) Kind() provider.Kind { return provider.AWS }
func (fakeProvider) DisplayName() string { return "AWS (falso para tests)" }
func (fakeProvider) Available() bool     { return true }
func (fakeProvider) ConnectInstructions(req provider.ConnectRequest) provider.Instructions {
	return provider.Instructions{
		Title:       "Conectar cuenta falsa",
		Description: "org " + req.OrgSlug + " region " + req.Region,
		URL:         req.CallbackURL + "/public/cloudformation/rampa-connect.yaml?external_id=" + req.ExternalID,
		Fields:      []provider.Field{{Name: "external_ref", Label: "ARN del rol", Required: true}},
	}
}
func (fakeProvider) Verify(_ context.Context, acc provider.CloudAccount) (provider.Verification, error) {
	if !strings.HasPrefix(acc.ExternalRef, "arn:aws:iam::") {
		return provider.Verification{OK: false, Message: "external_ref no es un ARN"}, nil
	}
	if acc.Settings["external_id"] == "" {
		return provider.Verification{OK: false, Message: "falta external_id"}, nil
	}
	return provider.Verification{OK: true, Identity: acc.ExternalRef, Message: "ok"}, nil
}
func (fakeProvider) Registry(acc provider.CloudAccount) (string, error) {
	return "123456789012.dkr.ecr." + acc.Region + ".amazonaws.com", nil
}
func (fakeProvider) SecretStoreSpec(provider.CloudAccount, provider.ClusterInfo) provider.SecretStoreSpec {
	return provider.SecretStoreSpec{Name: "aws-secrets-manager"}
}
func (fakeProvider) WorkloadIdentityAnnotations(provider.CloudAccount, string) map[string]string {
	return nil
}
func (fakeProvider) ServiceModule(string) (provider.ModuleRef, bool) {
	return provider.ModuleRef{}, false
}

// El Postgres embebido se levanta una sola vez por proceso de test, en un
// puerto libre y un directorio temporal; cada test usa una base propia.
var (
	pgOnce    sync.Once
	pgAdmin   string // URL de la base de mantenimiento
	pgSkip    string // motivo para saltar, si no se pudo arrancar
	pgCounter int
)

func TestMain(m *testing.M) {
	code := m.Run()
	if pgStop != nil {
		pgStop()
	}
	os.Exit(code)
}

var pgStop func()

func startSharedPostgres() {
	if os.Getenv("RAMPA_TEST_SKIP_DB") == "1" {
		pgSkip = "RAMPA_TEST_SKIP_DB=1: se saltan los tests con base de datos"
		return
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		pgSkip = "no pude reservar un puerto: " + err.Error()
		return
	}
	port := uint32(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	dir, err := os.MkdirTemp("", "rampa-pg-")
	if err != nil {
		pgSkip = "no pude crear el directorio temporal: " + err.Error()
		return
	}
	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(port).
		Database("postgres").
		Username("rampa").
		Password("rampa").
		DataPath(filepath.Join(dir, "data")).
		RuntimePath(filepath.Join(dir, "runtime")).
		StartTimeout(90 * time.Second).
		Logger(io.Discard)
	pg := embeddedpostgres.NewDatabase(cfg)
	if err := pg.Start(); err != nil {
		pgSkip = fmt.Sprintf("no pude arrancar el Postgres embebido (hace falta red para descargar los binarios la primera vez): %v", err)
		_ = os.RemoveAll(dir)
		return
	}
	pgStop = func() {
		_ = pg.Stop()
		_ = os.RemoveAll(dir)
	}
	pgAdmin = fmt.Sprintf("postgres://rampa:rampa@127.0.0.1:%d/postgres?sslmode=disable", port)
}

// startPostgres devuelve la URL de una base nueva y vacía para el test.
func startPostgres(t *testing.T) string {
	t.Helper()
	pgOnce.Do(startSharedPostgres)
	if pgSkip != "" {
		t.Skip(pgSkip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, pgAdmin)
	if err != nil {
		t.Fatalf("conectando a la base de mantenimiento: %v", err)
	}
	defer conn.Close(ctx)
	pgCounter++
	name := fmt.Sprintf("rampa_test_%d_%d", os.Getpid(), pgCounter)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creando la base %s: %v", name, err)
	}
	return strings.Replace(pgAdmin, "/postgres?", "/"+name+"?", 1)
}

type testEnv struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnvWith(t, nil)
}

// newTestEnvWith arma el entorno con variables extra (o pisadas) de configuración.
func newTestEnvWith(t *testing.T, extra map[string]string) *testEnv {
	t.Helper()
	dbURL := startPostgres(t)
	provider.Register(fakeProvider{})

	vars := map[string]string{
		"RAMPA_DATABASE_URL":   dbURL,
		"RAMPA_SESSION_SECRET": strings.Repeat("t", 40),
		"RAMPA_DEV_LOGIN":      "true",
		"RAMPA_PUBLIC_URL":     "http://api.test.local",
		"RAMPA_CHART_REPO":     "oci://ghcr.io/acme/charts/rampa-agent",
		"RAMPA_DEPLOY_TIMEOUT": "3m",
	}
	for k, v := range extra {
		vars[k] = v
	}
	cfg, err := config.Load(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv, err := New(ctx, cfg, nil, "test")
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		srv.Hub().Shutdown(sctx)
		ts.Close()
		srv.Close()
	})
	return &testEnv{t: t, srv: srv, ts: ts}
}

// client es un cliente HTTP con cookie jar propio (una sesión) y token opcional.
type client struct {
	env   *testEnv
	http  *http.Client
	token string
}

func (e *testEnv) newClient() *client {
	jar, _ := cookiejar.New(nil)
	return &client{env: e, http: &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// do hace un request JSON y devuelve el status y el cuerpo decodificado.
func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.env.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.env.t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.env.ts.URL+path, rdr)
	if err != nil {
		c.env.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.env.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			c.env.t.Fatalf("%s %s: respuesta no es JSON: %s", method, path, raw)
		}
	}
	return resp.StatusCode, out
}

func (c *client) must(method, path string, body any, want int) map[string]any {
	c.env.t.Helper()
	status, out := c.do(method, path, body)
	if status != want {
		c.env.t.Fatalf("%s %s: esperaba %d, tengo %d: %v", method, path, want, status, out)
	}
	return out
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	return str(e, "code")
}

func itemsOf(m map[string]any) []map[string]any {
	raw, _ := m["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if mm, ok := r.(map[string]any); ok {
			out = append(out, mm)
		}
	}
	return out
}

func exampleManifest(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "checkout-api", "rampa.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFullFlow(t *testing.T) {
	env := newTestEnv(t)
	c := env.newClient()

	// Salud y versión.
	c.must(http.MethodGet, "/healthz", nil, http.StatusOK)
	c.must(http.MethodGet, "/readyz", nil, http.StatusOK)
	ver := c.must(http.MethodGet, "/version", nil, http.StatusOK)
	if ver["version"] != "test" || ver["protocol"] != float64(protocol.Version) {
		t.Errorf("version: %v", ver)
	}

	// Sin sesión: 401.
	if status, out := c.do(http.MethodGet, "/v1/me", nil); status != http.StatusUnauthorized || errCode(out) != "unauthorized" {
		t.Fatalf("sin sesión esperaba 401 unauthorized, tengo %d %v", status, out)
	}

	// dev-login.
	me := c.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "Ana@Acme.com", "name": "Ana"}, http.StatusOK)
	user := me["user"].(map[string]any)
	if user["email"] != "ana@acme.com" || user["name"] != "Ana" {
		t.Errorf("me tras dev-login: %v", me)
	}

	// Crear org.
	if status, out := c.do(http.MethodPost, "/v1/orgs", map[string]string{"slug": "Acme!", "name": "Acme"}); status != http.StatusBadRequest || errCode(out) != "invalid" {
		t.Fatalf("slug inválido: %d %v", status, out)
	}
	org := c.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "acme", "name": "Acme"}, http.StatusCreated)
	if org["slug"] != "acme" || org["settings"].(map[string]any)["require_approval_prod"] != true {
		t.Errorf("org: %v", org)
	}
	me = c.must(http.MethodGet, "/v1/me", nil, http.StatusOK)
	ms := me["memberships"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["role"] != "owner" {
		t.Errorf("memberships: %v", ms)
	}
	orgs := c.must(http.MethodGet, "/v1/orgs", nil, http.StatusOK)
	if len(itemsOf(orgs)) != 1 {
		t.Errorf("orgs: %v", orgs)
	}

	// Providers.
	provs := c.must(http.MethodGet, "/v1/orgs/acme/providers", nil, http.StatusOK)
	if len(itemsOf(provs)) != 3 {
		t.Errorf("providers: %v", provs)
	}

	// Cuenta de nube.
	acc := c.must(http.MethodPost, "/v1/orgs/acme/cloud-accounts", map[string]string{"provider": "aws", "name": "acme-nonprod", "region": "us-east-1"}, http.StatusCreated)
	if acc["status"] != "pending" || acc["instructions"] == nil {
		t.Fatalf("cloud account: %v", acc)
	}
	settings := acc["settings"].(map[string]any)
	if str(settings, "external_id") == "" {
		t.Errorf("la API debería generar external_id: %v", settings)
	}
	accID := str(acc, "id")

	// Verify sin external_ref: invalid.
	if status, out := c.do(http.MethodPost, "/v1/orgs/acme/cloud-accounts/"+accID+"/verify", nil); status != http.StatusBadRequest {
		t.Fatalf("verify sin ref: %d %v", status, out)
	}
	acc = c.must(http.MethodPatch, "/v1/orgs/acme/cloud-accounts/"+accID, map[string]any{"external_ref": "arn:aws:iam::123456789012:role/RampaConnect", "settings": map[string]string{"external_id": "hackeado", "note": "x"}}, http.StatusOK)
	if acc["external_ref"] != "arn:aws:iam::123456789012:role/RampaConnect" {
		t.Errorf("patch: %v", acc)
	}
	if acc["settings"].(map[string]any)["external_id"] == "hackeado" {
		t.Error("el cliente no debería poder pisar external_id")
	}
	acc = c.must(http.MethodPost, "/v1/orgs/acme/cloud-accounts/"+accID+"/verify", nil, http.StatusOK)
	if acc["status"] != "connected" || acc["verification"].(map[string]any)["ok"] != true {
		t.Fatalf("verify: %v", acc)
	}

	// Cluster.
	cl := c.must(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{
		"name": "eks-nonprod", "env": "nonprod", "cloud_account_id": accID, "domain": "apps.dev.acme.com", "ingress_class": "nginx", "secret_store": "aws-secrets-manager",
	}, http.StatusCreated)
	clusterToken := str(cl, "token")
	clusterID := str(cl, "id")
	if !strings.HasPrefix(clusterToken, "rampa_cluster_") || cl["status"] != "offline" {
		t.Fatalf("cluster: %v", cl)
	}
	install := str(cl, "install_command")
	for _, want := range []string{"helm upgrade --install rampa-agent oci://ghcr.io/acme/charts/rampa-agent", "--namespace rampa-system", "--create-namespace", "--set api.url=http://api.test.local", "--set cluster.token=" + clusterToken} {
		if !strings.Contains(install, want) {
			t.Errorf("install_command sin %q: %s", want, install)
		}
	}
	got := c.must(http.MethodGet, "/v1/orgs/acme/clusters/"+clusterID, nil, http.StatusOK)
	if _, leaked := got["token"]; leaked {
		t.Error("GET cluster no debe devolver el token")
	}
	if status, _ := c.do(http.MethodDelete, "/v1/orgs/acme/cloud-accounts/"+accID, nil); status != http.StatusConflict {
		t.Errorf("borrar cuenta con clusters debería ser 409, fue %d", status)
	}

	// Importar app: inválida primero.
	bad := strings.Replace(exampleManifest(t), "type: api", "type: lambda", 1)
	status, out := c.do(http.MethodPost, "/v1/orgs/acme/apps", map[string]string{"manifest": bad})
	if status != http.StatusBadRequest || errCode(out) != "invalid" {
		t.Fatalf("manifest inválido: %d %v", status, out)
	}
	details, _ := out["error"].(map[string]any)["details"].([]any)
	if len(details) == 0 || !strings.Contains(details[0].(string), "spec.type") {
		t.Errorf("details de validación: %v", details)
	}
	app := c.must(http.MethodPost, "/v1/orgs/acme/apps", map[string]string{"manifest": exampleManifest(t), "repo_url": "https://github.com/acme/checkout-api"}, http.StatusCreated)
	if app["name"] != "checkout-api" || app["team"] != "payments" || app["type"] != "api" || str(app, "manifest_sha") == "" {
		t.Fatalf("app: %v", app)
	}
	if fmt.Sprint(app["scopes_declared"]) != "[dev pr prod staging]" {
		t.Errorf("scopes_declared: %v", app["scopes_declared"])
	}
	if status, out := c.do(http.MethodPost, "/v1/orgs/acme/apps", map[string]string{"manifest": exampleManifest(t)}); status != http.StatusConflict || errCode(out) != "conflict" {
		t.Errorf("app repetida: %d %v", status, out)
	}
	renamed := strings.Replace(exampleManifest(t), "name: checkout-api", "name: otra-api", 1)
	if status, _ := c.do(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/manifest", map[string]string{"manifest": renamed}); status != http.StatusBadRequest {
		t.Errorf("cambiar el nombre por PUT debería ser 400, fue %d", status)
	}
	c.must(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/manifest", map[string]string{"manifest": exampleManifest(t)}, http.StatusOK)

	// Scopes: declarados sin fila.
	scopes := c.must(http.MethodGet, "/v1/orgs/acme/apps/checkout-api/scopes", nil, http.StatusOK)
	byName := map[string]map[string]any{}
	for _, s := range itemsOf(scopes) {
		byName[str(s, "name")] = s
	}
	if len(byName) != 3 || byName["dev"]["status"] != "unbound" || byName["prod"]["declared"] != true || byName["prod"]["strategy"] != "canary" {
		t.Fatalf("scopes iniciales: %v", scopes)
	}

	// Bind: prod no puede ir a nonprod.
	if status, out := c.do(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/scopes/prod", map[string]string{"cluster_id": clusterID}); status != http.StatusBadRequest {
		t.Errorf("prod en nonprod: %d %v", status, out)
	}
	sc := c.must(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/scopes/dev", map[string]string{"cluster_id": clusterID}, http.StatusOK)
	if sc["cluster_id"] != clusterID || sc["status"] != "idle" || sc["namespace"] != "checkout-api-dev" || sc["url"] != "https://checkout-api-dev.apps.dev.acme.com" {
		t.Fatalf("bind dev: %v", sc)
	}

	// Deploy a dev: queued y con agent_command.
	if status, out := c.do(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/dev/deployments", map[string]string{}); status != http.StatusBadRequest {
		t.Errorf("deploy sin image: %d %v", status, out)
	}
	dep := c.must(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/dev/deployments", map[string]string{"image": "sha-abc1234", "message": "primer deploy"}, http.StatusCreated)
	depID := str(dep, "id")
	if dep["status"] != "queued" || dep["image"] != "123456789012.dkr.ecr.us-east-1.amazonaws.com/rampa/checkout-api:sha-abc1234" || dep["cluster_id"] != clusterID {
		t.Fatalf("deployment: %v", dep)
	}
	if rb, _ := dep["requested_by"].(map[string]any); rb == nil || rb["email"] != "ana@acme.com" {
		t.Errorf("requested_by: %v", dep["requested_by"])
	}
	orgRow, err := env.srv.Store().GetOrgBySlug(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	cmds, err := env.srv.Store().ListAgentCommandsForDeployment(context.Background(), orgRow.ID, depID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Status != store.CommandQueued || cmds[0].Kind != "deploy" || cmds[0].ClusterID != clusterID {
		t.Fatalf("agent_commands: %+v", cmds)
	}
	var pcmd protocol.Command
	if err := json.Unmarshal(cmds[0].Payload, &pcmd); err != nil || pcmd.Deploy == nil || len(pcmd.Deploy.Objects) == 0 || pcmd.Deploy.TimeoutSeconds != 180 {
		t.Fatalf("payload del comando: %v %+v", err, pcmd)
	}
	if pcmd.Deploy.Labels["rampa.dev/deployment"] != depID || pcmd.Deploy.Namespace != "checkout-api-dev" {
		t.Errorf("labels/namespace del comando: %+v", pcmd.Deploy)
	}

	// Eventos del deployment.
	evs := c.must(http.MethodGet, "/v1/orgs/acme/events?subject_type=deployment&subject_id="+depID, nil, http.StatusOK)
	kinds := map[string]bool{}
	for _, e := range itemsOf(evs) {
		kinds[str(e, "kind")] = true
	}
	if !kinds["deployment.requested"] || !kinds["deployment.queued"] {
		t.Errorf("eventos del deployment: %v", kinds)
	}

	// Scope dev ahora está deploying.
	scopes = c.must(http.MethodGet, "/v1/orgs/acme/apps/checkout-api/scopes", nil, http.StatusOK)
	for _, s := range itemsOf(scopes) {
		if str(s, "name") == "dev" && s["status"] != "deploying" {
			t.Errorf("dev debería estar deploying: %v", s)
		}
	}

	// Un agente falso se conecta, recibe el comando y lo completa.
	runFakeAgent(t, env, clusterToken, depID)
	dep = c.must(http.MethodGet, "/v1/orgs/acme/deployments/"+depID, nil, http.StatusOK)
	if dep["status"] != "healthy" || dep["finished_at"] == nil || dep["workload"] == nil {
		t.Fatalf("deployment tras el agente: %v", dep)
	}
	if wl, _ := dep["workload"].(map[string]any); wl == nil || wl["kind"] != "Deployment" {
		t.Errorf("workload: %v", dep["workload"])
	}
	scopes = c.must(http.MethodGet, "/v1/orgs/acme/apps/checkout-api/scopes", nil, http.StatusOK)
	for _, s := range itemsOf(scopes) {
		if str(s, "name") == "dev" {
			if s["status"] != "healthy" {
				t.Errorf("dev debería estar healthy: %v", s)
			}
			if cd, _ := s["current_deployment"].(map[string]any); cd == nil || cd["id"] != depID {
				t.Errorf("current_deployment: %v", s["current_deployment"])
			}
		}
	}
	list := c.must(http.MethodGet, "/v1/orgs/acme/apps/checkout-api/scopes/dev/deployments?limit=5", nil, http.StatusOK)
	if len(itemsOf(list)) != 1 {
		t.Errorf("listado de deployments: %v", list)
	}
	got = c.must(http.MethodGet, "/v1/orgs/acme/clusters/"+clusterID, nil, http.StatusOK)
	if got["agent_version"] != "fake-1.0" || got["kubernetes_version"] != "v1.30.2" || got["last_seen_at"] == nil {
		t.Errorf("cluster tras el agente: %v", got)
	}

	// Prod: requiere cluster prod y aprobación.
	if status, out := c.do(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/prod/deployments", map[string]string{"image": "sha-abc1234"}); status != http.StatusBadRequest {
		t.Errorf("prod sin cluster prod: %d %v", status, out)
	}
	prodCl := c.must(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "eks-prod", "env": "prod", "cloud_account_id": accID, "domain": "apps.acme.com"}, http.StatusCreated)
	prodDep := c.must(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/prod/deployments", map[string]string{"image": "sha-abc1234"}, http.StatusCreated)
	if prodDep["status"] != "awaiting_approval" || prodDep["cluster_id"] != str(prodCl, "id") {
		t.Fatalf("prod deployment: %v", prodDep)
	}
	if status, _ := c.do(http.MethodPost, "/v1/orgs/acme/deployments/"+str(dep, "id")+"/approve", nil); status != http.StatusConflict {
		t.Errorf("aprobar algo que no espera aprobación debería ser 409, fue %d", status)
	}
	approved := c.must(http.MethodPost, "/v1/orgs/acme/deployments/"+str(prodDep, "id")+"/approve", nil, http.StatusOK)
	if approved["status"] != "queued" || approved["approved_by"] == nil {
		t.Fatalf("approve: %v", approved)
	}
	cmds, _ = env.srv.Store().ListAgentCommandsForDeployment(context.Background(), orgRow.ID, str(prodDep, "id"))
	if len(cmds) != 1 || cmds[0].ClusterID != str(prodCl, "id") {
		t.Errorf("comando de prod: %+v", cmds)
	}
	// Cancelar otro que quedó en awaiting_approval.
	prodDep2 := c.must(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/prod/deployments", map[string]string{"image": "sha-def5678"}, http.StatusCreated)
	cancelled := c.must(http.MethodPost, "/v1/orgs/acme/deployments/"+str(prodDep2, "id")+"/cancel", nil, http.StatusOK)
	if cancelled["status"] != "cancelled" {
		t.Errorf("cancel: %v", cancelled)
	}
	if status, _ := c.do(http.MethodPost, "/v1/orgs/acme/deployments/"+str(prodDep2, "id")+"/cancel", nil); status != http.StatusConflict {
		t.Errorf("cancelar dos veces debería ser 409, fue %d", status)
	}
	// Sin aprobación obligatoria, prod queda queued directo.
	c.must(http.MethodPatch, "/v1/orgs/acme", map[string]any{"settings": map[string]bool{"require_approval_prod": false}}, http.StatusOK)
	prodDep3 := c.must(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/prod/deployments", map[string]string{"image": "sha-0000000"}, http.StatusCreated)
	if prodDep3["status"] != "queued" {
		t.Errorf("prod sin aprobación: %v", prodDep3)
	}

	// Tokens de API y roles.
	tok := c.must(http.MethodPost, "/v1/orgs/acme/tokens", map[string]any{"name": "ci-checkout", "role": "developer", "expires_in_days": 30}, http.StatusCreated)
	plain := str(tok, "token")
	if !strings.HasPrefix(plain, "rampa_") || tok["expires_at"] == nil {
		t.Fatalf("token: %v", tok)
	}
	listed := c.must(http.MethodGet, "/v1/orgs/acme/tokens", nil, http.StatusOK)
	if len(itemsOf(listed)) != 1 || itemsOf(listed)[0]["token"] != nil {
		t.Errorf("listado de tokens no debe traer el secreto: %v", listed)
	}
	ci := env.newClient()
	ci.token = plain
	ciMe := ci.must(http.MethodGet, "/v1/me", nil, http.StatusOK)
	if ciMe["memberships"].([]any)[0].(map[string]any)["role"] != "developer" {
		t.Errorf("me con token: %v", ciMe)
	}
	ci.must(http.MethodGet, "/v1/orgs/acme/apps", nil, http.StatusOK)
	if status, out := ci.do(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/prod/deployments", map[string]string{"image": "sha-1111111"}); status != http.StatusForbidden || errCode(out) != "forbidden" {
		t.Errorf("developer a prod: %d %v", status, out)
	}
	if status, _ := ci.do(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "x", "env": "nonprod", "cloud_account_id": accID, "domain": "d"}); status != http.StatusForbidden {
		t.Errorf("developer creando clusters: %d", status)
	}
	staging := ci.must(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/staging/deployments", map[string]string{"image": "sha-2222222"}, http.StatusCreated)
	if staging["status"] != "queued" || staging["cluster_id"] != clusterID {
		t.Errorf("developer a staging con cluster por defecto: %v", staging)
	}
	bogus := env.newClient()
	bogus.token = "rampa_" + strings.Repeat("0", 64)
	if status, _ := bogus.do(http.MethodGet, "/v1/me", nil); status != http.StatusUnauthorized {
		t.Errorf("token inventado: %d", status)
	}

	// Miembros.
	mem := c.must(http.MethodPost, "/v1/orgs/acme/members", map[string]string{"email": "bob@acme.com", "role": "viewer"}, http.StatusOK)
	if mem["role"] != "viewer" {
		t.Errorf("member: %v", mem)
	}
	members := c.must(http.MethodGet, "/v1/orgs/acme/members", nil, http.StatusOK)
	if len(itemsOf(members)) != 2 {
		t.Errorf("members: %v", members)
	}
	if status, _ := c.do(http.MethodDelete, "/v1/orgs/acme/members/"+str(user, "id"), nil); status != http.StatusConflict {
		t.Errorf("sacar al último owner debería ser 409, fue %d", status)
	}

	// Borrar scope efímero inexistente: 404. Borrar permanente requiere admin.
	if status, _ := c.do(http.MethodDelete, "/v1/orgs/acme/apps/checkout-api/scopes/pr-99", nil); status != http.StatusNotFound {
		t.Errorf("borrar pr-99 inexistente: %d", status)
	}
	if status, out := ci.do(http.MethodDelete, "/v1/orgs/acme/apps/checkout-api/scopes/dev", nil); status != http.StatusForbidden {
		t.Errorf("developer borrando scope permanente: %d %v", status, out)
	}
	del := c.must(http.MethodDelete, "/v1/orgs/acme/apps/checkout-api/scopes/dev", nil, http.StatusAccepted)
	if del["status"] != "deleting" {
		t.Errorf("delete scope: %v", del)
	}
	if status, _ := c.do(http.MethodDelete, "/v1/orgs/acme/clusters/"+clusterID, nil); status != http.StatusConflict {
		t.Errorf("borrar cluster con scopes debería ser 409, fue %d", status)
	}

	// Logout.
	c.must(http.MethodPost, "/v1/auth/logout", nil, http.StatusNoContent)
	if status, _ := c.do(http.MethodGet, "/v1/me", nil); status != http.StatusUnauthorized {
		t.Errorf("tras logout esperaba 401, tengo %d", status)
	}
}

// runFakeAgent conecta por WebSocket con el token del cluster, hace hello,
// recibe el deploy y responde ack, event y result.
func runFakeAgent(t *testing.T, env *testEnv, token, wantDeploymentID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/v1/agent/connect"

	// Token inválido: 401 antes del upgrade.
	if _, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer rampa_cluster_nope"}}}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token de cluster inválido debería dar 401: %v %v", err, resp)
	}

	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	send := func(kind protocol.Kind, commandID string, payload any) {
		env, err := protocol.NewEnvelope(kind, "m", commandID, payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Write(ctx, conn, env); err != nil {
			t.Fatalf("write %s: %v", kind, err)
		}
	}
	read := func() protocol.Envelope {
		var e protocol.Envelope
		if err := wsjson.Read(ctx, conn, &e); err != nil {
			t.Fatalf("read: %v", err)
		}
		return e
	}

	send(protocol.KindHello, "", protocol.Hello{Protocol: protocol.Version, AgentVersion: "fake-1.0", KubernetesVersion: "v1.30.2", Capabilities: []string{"deploy", "undeploy"}})
	e := read()
	if e.Kind != protocol.KindWelcome {
		t.Fatalf("esperaba welcome, tengo %s", e.Kind)
	}
	var w protocol.Welcome
	_ = json.Unmarshal(e.Payload, &w)
	if w.OrgSlug != "acme" || w.ClusterName != "eks-nonprod" || w.PendingCommands != 1 {
		t.Fatalf("welcome: %+v", w)
	}

	e = read()
	if e.Kind != protocol.KindCommand {
		t.Fatalf("esperaba command, tengo %s", e.Kind)
	}
	var cmd protocol.Command
	if err := json.Unmarshal(e.Payload, &cmd); err != nil || cmd.Deploy == nil || cmd.Deploy.DeploymentID != wantDeploymentID {
		t.Fatalf("command: %v %+v", err, cmd)
	}
	send(protocol.KindAck, cmd.ID, protocol.Ack{CommandID: cmd.ID})
	send(protocol.KindEvent, cmd.ID, protocol.Event{CommandID: cmd.ID, Time: time.Now(), Phase: protocol.PhaseApplying, Reason: "Applied", Message: fmt.Sprintf("%d objetos aplicados", len(cmd.Deploy.Objects))})
	send(protocol.KindEvent, cmd.ID, protocol.Event{CommandID: cmd.ID, Time: time.Now(), Phase: protocol.PhaseProgressing, Reason: "Progressing", Message: "0/1 listos"})
	send(protocol.KindHeartbeat, "", protocol.Heartbeat{Nodes: 2})
	send(protocol.KindResult, cmd.ID, protocol.Result{CommandID: cmd.ID, Status: protocol.ResultSucceeded, Message: "checkout-api listo", Workload: &protocol.WorkloadStatus{Kind: "Deployment", Phase: protocol.PhaseHealthy, Desired: 1, Ready: 1, Updated: 1, Available: 1}})

	// Esperamos a que el result se procese antes de cerrar: cuando el
	// deployment está healthy, el hub terminó.
	orgRow, err := env.srv.Store().GetOrgBySlug(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		d, err := env.srv.Store().GetDeployment(ctx, orgRow.ID, wantDeploymentID)
		if err == nil && d.Status == store.DeploymentHealthy {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// El cluster está online mientras la conexión vive.
	clRow, err := env.srv.Store().GetClusterByTokenHash(ctx, hashOf(token))
	if err != nil {
		t.Fatal(err)
	}
	if clRow.Status != store.ClusterOnline {
		t.Errorf("el cluster debería estar online con el agente conectado: %s", clRow.Status)
	}
	conn.Close(websocket.StatusNormalClosure, "fin")
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		clRow, _ = env.srv.Store().GetClusterByTokenHash(ctx, hashOf(token))
		if clRow != nil && clRow.Status == store.ClusterOffline {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("el cluster debería quedar offline al cerrar la conexión")
}

func TestTenantIsolation(t *testing.T) {
	env := newTestEnv(t)
	a := env.newClient()
	a.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "a@alpha.com"}, http.StatusOK)
	a.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "alpha", "name": "Alpha"}, http.StatusCreated)
	acc := a.must(http.MethodPost, "/v1/orgs/alpha/cloud-accounts", map[string]string{"provider": "aws", "name": "alpha-aws", "region": "us-east-1"}, http.StatusCreated)
	cl := a.must(http.MethodPost, "/v1/orgs/alpha/clusters", map[string]string{"name": "eks", "env": "nonprod", "cloud_account_id": str(acc, "id"), "domain": "apps.alpha.com"}, http.StatusCreated)
	a.must(http.MethodPost, "/v1/orgs/alpha/apps", map[string]string{"manifest": exampleManifest(t)}, http.StatusCreated)
	dep := a.must(http.MethodPost, "/v1/orgs/alpha/apps/checkout-api/scopes/dev/deployments", map[string]string{"image": "sha-abc1234"}, http.StatusCreated)
	tok := a.must(http.MethodPost, "/v1/orgs/alpha/tokens", map[string]any{"name": "ci", "role": "admin"}, http.StatusCreated)

	b := env.newClient()
	b.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "b@beta.com"}, http.StatusOK)
	b.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "beta", "name": "Beta"}, http.StatusCreated)

	// B no es miembro de alpha: todo lo de alpha es 404 para B.
	paths := []string{
		"/v1/orgs/alpha",
		"/v1/orgs/alpha/members",
		"/v1/orgs/alpha/cloud-accounts",
		"/v1/orgs/alpha/cloud-accounts/" + str(acc, "id"),
		"/v1/orgs/alpha/clusters/" + str(cl, "id"),
		"/v1/orgs/alpha/apps",
		"/v1/orgs/alpha/apps/checkout-api",
		"/v1/orgs/alpha/apps/checkout-api/scopes",
		"/v1/orgs/alpha/deployments/" + str(dep, "id"),
		"/v1/orgs/alpha/events",
	}
	for _, p := range paths {
		status, out := b.do(http.MethodGet, p, nil)
		if status != http.StatusNotFound && status != http.StatusForbidden {
			t.Errorf("GET %s como B: esperaba 404 o 403, tengo %d %v", p, status, out)
		}
	}
	if status, _ := b.do(http.MethodPost, "/v1/orgs/alpha/apps/checkout-api/scopes/dev/deployments", map[string]string{"image": "x"}); status != http.StatusNotFound && status != http.StatusForbidden {
		t.Errorf("POST deploy en alpha como B: %d", status)
	}
	if status, _ := b.do(http.MethodPost, "/v1/orgs/alpha/deployments/"+str(dep, "id")+"/cancel", nil); status != http.StatusNotFound && status != http.StatusForbidden {
		t.Errorf("cancel en alpha como B: %d", status)
	}

	// Los ids de alpha no existen dentro de beta aunque B sea owner de beta.
	for _, p := range []string{
		"/v1/orgs/beta/cloud-accounts/" + str(acc, "id"),
		"/v1/orgs/beta/clusters/" + str(cl, "id"),
		"/v1/orgs/beta/deployments/" + str(dep, "id"),
		"/v1/orgs/beta/apps/checkout-api",
	} {
		if status, _ := b.do(http.MethodGet, p, nil); status != http.StatusNotFound {
			t.Errorf("GET %s: esperaba 404, tengo %d", p, status)
		}
	}

	// Un token de alpha no entra a beta, y viceversa A no ve beta.
	ta := env.newClient()
	ta.token = str(tok, "token")
	ta.must(http.MethodGet, "/v1/orgs/alpha/apps", nil, http.StatusOK)
	if status, _ := ta.do(http.MethodGet, "/v1/orgs/beta", nil); status != http.StatusNotFound {
		t.Errorf("token de alpha en beta: %d", status)
	}
	if status, _ := a.do(http.MethodGet, "/v1/orgs/beta/apps", nil); status != http.StatusNotFound {
		t.Errorf("A en beta: %d", status)
	}
	orgs := a.must(http.MethodGet, "/v1/orgs", nil, http.StatusOK)
	if len(itemsOf(orgs)) != 1 || itemsOf(orgs)[0]["slug"] != "alpha" {
		t.Errorf("A debería ver solo alpha: %v", orgs)
	}
	// Slug repetido: conflict.
	if status, out := b.do(http.MethodPost, "/v1/orgs", map[string]string{"slug": "alpha", "name": "Alpha 2"}); status != http.StatusConflict || errCode(out) != "conflict" {
		t.Errorf("slug repetido: %d %v", status, out)
	}
}

func TestEventStream(t *testing.T) {
	env := newTestEnv(t)
	c := env.newClient()
	c.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "s@sse.com"}, http.StatusOK)
	c.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "sse", "name": "SSE"}, http.StatusCreated)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, env.ts.URL+"/v1/orgs/sse/events/stream?subject_type=cloud_account", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	lines := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	waitLine := func(prefix string) string {
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stream cerrado esperando %q", prefix)
				}
				if strings.HasPrefix(l, prefix) {
					return l
				}
			case <-ctx.Done():
				t.Fatalf("timeout esperando %q", prefix)
			}
		}
	}
	waitLine(": connected")

	// Un evento de otro subject_type no pasa el filtro; uno de cloud_account sí.
	c.must(http.MethodPost, "/v1/orgs/sse/tokens", map[string]any{"name": "ruido", "role": "viewer"}, http.StatusCreated)
	acc := c.must(http.MethodPost, "/v1/orgs/sse/cloud-accounts", map[string]string{"provider": "aws", "name": "sse-aws", "region": "us-east-1"}, http.StatusCreated)
	ev := waitLine("event: ")
	if ev != "event: cloud_account.created" {
		t.Fatalf("primer evento del stream: %q", ev)
	}
	data := waitLine("data: ")
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(data, "data: ")), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["subject_id"] != str(acc, "id") || payload["actor"].(map[string]any)["type"] != "user" {
		t.Errorf("data del evento: %v", payload)
	}
	cancel()
	// Al cancelar el request, el servidor libera la suscripción.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := <-lines; !ok {
			break
		}
	}
}

func TestCloudFormationAndSPA(t *testing.T) {
	env := newTestEnv(t)
	resp, err := http.Get(env.ts.URL + CloudFormationPath)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "AWSTemplateFormatVersion") {
		t.Errorf("cloudformation: %d %.80s", resp.StatusCode, body)
	}
	// Sin RAMPA_WEB_DIR, una ruta desconocida es un 404 JSON.
	resp, err = http.Get(env.ts.URL + "/lo-que-sea")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("ruta desconocida: %d", resp.StatusCode)
	}
}

func TestSPAHandler(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>rampa</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := spaHandler(dir, nil)
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := get("/assets/app.js"); code != 200 || body != "console.log(1)" {
		t.Errorf("asset: %d %q", code, body)
	}
	if code, body := get("/orgs/acme/apps"); code != 200 || !strings.Contains(body, "rampa") {
		t.Errorf("fallback a index: %d %q", code, body)
	}
	if code, _ := get("/v1/nope"); code != 404 {
		t.Errorf("/v1 no debe caer en la SPA: %d", code)
	}
	if code, body := get("/../../etc/passwd"); code != 200 || !strings.Contains(body, "rampa") {
		t.Errorf("path traversal debería caer en index: %d %q", code, body)
	}
	if code, body := get("/index.html"); code != 200 || !strings.Contains(body, "rampa") {
		t.Errorf("/index.html: %d %q", code, body)
	}
}

// ---- helpers para los tests de consistencia y seguridad ----

// agentConn es un agente falso conectado, ya con hello y welcome hechos.
type agentConn struct {
	t       *testing.T
	ctx     context.Context
	cancel  context.CancelFunc
	conn    *websocket.Conn
	welcome protocol.Welcome
}

// dialFakeAgent conecta con el token del cluster, manda hello (con inFlight) y
// devuelve la conexión tras el welcome.
func dialFakeAgent(t *testing.T, env *testEnv, token string, inFlight []string) *agentConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	url := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/v1/agent/connect"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}
	a := &agentConn{t: t, ctx: ctx, cancel: cancel, conn: conn}
	t.Cleanup(a.close)
	a.send(protocol.KindHello, "", protocol.Hello{Protocol: protocol.Version, AgentVersion: "fake-2.0", KubernetesVersion: "v1.30.2", Capabilities: []string{"deploy", "undeploy"}, InFlight: inFlight})
	e := a.read()
	if e.Kind != protocol.KindWelcome {
		t.Fatalf("esperaba welcome, tengo %s", e.Kind)
	}
	_ = json.Unmarshal(e.Payload, &a.welcome)
	return a
}

func (a *agentConn) send(kind protocol.Kind, commandID string, payload any) {
	a.t.Helper()
	env, err := protocol.NewEnvelope(kind, "m", commandID, payload)
	if err != nil {
		a.t.Fatal(err)
	}
	if err := wsjson.Write(a.ctx, a.conn, env); err != nil {
		a.t.Fatalf("write %s: %v", kind, err)
	}
}

func (a *agentConn) readErr() (protocol.Envelope, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	var e protocol.Envelope
	err := wsjson.Read(ctx, a.conn, &e)
	return e, err
}

func (a *agentConn) read() protocol.Envelope {
	a.t.Helper()
	e, err := a.readErr()
	if err != nil {
		a.t.Fatalf("read: %v", err)
	}
	return e
}

// readCommand espera el próximo command y lo decodifica.
func (a *agentConn) readCommand() protocol.Command {
	a.t.Helper()
	e := a.read()
	if e.Kind != protocol.KindCommand {
		a.t.Fatalf("esperaba command, tengo %s", e.Kind)
	}
	var cmd protocol.Command
	if err := json.Unmarshal(e.Payload, &cmd); err != nil {
		a.t.Fatal(err)
	}
	return cmd
}

func (a *agentConn) ack(cmdID string) {
	a.t.Helper()
	a.send(protocol.KindAck, cmdID, protocol.Ack{CommandID: cmdID})
}

// crash cierra la conexión como si el agente se hubiera reiniciado.
func (a *agentConn) crash() {
	_ = a.conn.Close(websocket.StatusGoingAway, "reinicio")
}

func (a *agentConn) close() {
	_ = a.conn.Close(websocket.StatusNormalClosure, "")
	a.cancel()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout esperando: %s", what)
}

// deploymentStatus lee el estado y mensaje de un deployment directo del store.
func (e *testEnv) deploymentStatus(orgID, id string) (status, message string) {
	d, err := e.srv.Store().GetDeployment(context.Background(), orgID, id)
	if err != nil {
		e.t.Fatalf("GetDeployment %s: %v", id, err)
	}
	return d.Status, d.Message
}

func (e *testEnv) commandStatus(orgID, depID string) string {
	cmds, err := e.srv.Store().ListAgentCommandsForDeployment(context.Background(), orgID, depID)
	if err != nil || len(cmds) == 0 {
		e.t.Fatalf("comandos de %s: %v %v", depID, err, cmds)
	}
	return cmds[len(cmds)-1].Status
}

// orgFixture arma org, cuenta, cluster nonprod y la app de ejemplo con dev atado.
type orgFixture struct {
	owner        *client
	orgID        string
	accID        string
	clusterID    string
	clusterToken string
}

func newOrgFixture(t *testing.T, env *testEnv, slug, ownerEmail string) *orgFixture {
	t.Helper()
	c := env.newClient()
	c.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": ownerEmail}, http.StatusOK)
	c.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": slug, "name": slug}, http.StatusCreated)
	acc := c.must(http.MethodPost, "/v1/orgs/"+slug+"/cloud-accounts", map[string]string{"provider": "aws", "name": slug + "-aws", "region": "us-east-1"}, http.StatusCreated)
	cl := c.must(http.MethodPost, "/v1/orgs/"+slug+"/clusters", map[string]string{"name": "eks-nonprod", "env": "nonprod", "cloud_account_id": str(acc, "id"), "domain": "apps.dev." + slug + ".com"}, http.StatusCreated)
	c.must(http.MethodPost, "/v1/orgs/"+slug+"/apps", map[string]string{"manifest": exampleManifest(t)}, http.StatusCreated)
	c.must(http.MethodPut, "/v1/orgs/"+slug+"/apps/checkout-api/scopes/dev", map[string]string{"cluster_id": str(cl, "id")}, http.StatusOK)
	orgRow, err := env.srv.Store().GetOrgBySlug(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	return &orgFixture{owner: c, orgID: orgRow.ID, accID: str(acc, "id"), clusterID: str(cl, "id"), clusterToken: str(cl, "token")}
}

func scopeByName(c *client, org, app, name string) map[string]any {
	c.env.t.Helper()
	scopes := c.must(http.MethodGet, "/v1/orgs/"+org+"/apps/"+app+"/scopes", nil, http.StatusOK)
	for _, s := range itemsOf(scopes) {
		if str(s, "name") == name {
			return s
		}
	}
	c.env.t.Fatalf("scope %s no está en %v", name, scopes)
	return nil
}

// TestMemberHierarchyAndTokenRevocation cubre: un admin no degrada a un owner;
// sacar a un miembro revoca sus tokens (404/401 después); bajarle el rol acota
// sus tokens y el token efectivo nunca supera la membresía actual.
func TestMemberHierarchyAndTokenRevocation(t *testing.T) {
	env := newTestEnv(t)
	fx := newOrgFixture(t, env, "acme", "ana@acme.com")
	owner := fx.owner
	prodCl := owner.must(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "eks-prod", "env": "prod", "cloud_account_id": fx.accID, "domain": "apps.acme.com"}, http.StatusCreated)
	_ = prodCl

	for email, role := range map[string]string{"bob@acme.com": "owner", "mia@acme.com": "admin", "dan@acme.com": "admin", "eva@acme.com": "admin", "vic@acme.com": "viewer"} {
		owner.must(http.MethodPost, "/v1/orgs/acme/members", map[string]string{"email": email, "role": role}, http.StatusOK)
	}
	members := owner.must(http.MethodGet, "/v1/orgs/acme/members", nil, http.StatusOK)
	userID := map[string]string{}
	for _, m := range itemsOf(members) {
		u := m["user"].(map[string]any)
		userID[str(u, "email")] = str(u, "id")
	}

	// Mia (admin) intenta degradar a los owners: forbidden, como en removeMember.
	mia := env.newClient()
	mia.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "mia@acme.com"}, http.StatusOK)
	for _, target := range []string{"ana@acme.com", "bob@acme.com"} {
		if status, out := mia.do(http.MethodPost, "/v1/orgs/acme/members", map[string]string{"email": target, "role": "viewer"}); status != http.StatusForbidden || errCode(out) != "forbidden" {
			t.Errorf("admin degradando al owner %s: esperaba 403 forbidden, tengo %d %v", target, status, out)
		}
	}
	members = owner.must(http.MethodGet, "/v1/orgs/acme/members", nil, http.StatusOK)
	for _, m := range itemsOf(members) {
		if e := str(m["user"].(map[string]any), "email"); (e == "ana@acme.com" || e == "bob@acme.com") && m["role"] != "owner" {
			t.Errorf("%s debería seguir siendo owner: %v", e, m)
		}
	}
	// Un admin sí puede tocar a otro admin o a un viewer (rol <= al propio).
	mia.must(http.MethodPost, "/v1/orgs/acme/members", map[string]string{"email": "vic@acme.com", "role": "developer"}, http.StatusOK)
	// Y nadie otorga owner sin serlo.
	if status, _ := mia.do(http.MethodPost, "/v1/orgs/acme/members", map[string]string{"email": "vic@acme.com", "role": "owner"}); status != http.StatusForbidden {
		t.Errorf("admin otorgando owner: %d", status)
	}

	// Dan y Eva (admins) crean tokens admin.
	dan := env.newClient()
	dan.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "dan@acme.com"}, http.StatusOK)
	danTok := env.newClient()
	danTok.token = str(dan.must(http.MethodPost, "/v1/orgs/acme/tokens", map[string]any{"name": "dan-ci", "role": "admin"}, http.StatusCreated), "token")
	eva := env.newClient()
	eva.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "eva@acme.com"}, http.StatusOK)
	evaTok := env.newClient()
	evaTok.token = str(eva.must(http.MethodPost, "/v1/orgs/acme/tokens", map[string]any{"name": "eva-ci", "role": "admin"}, http.StatusCreated), "token")

	danTok.must(http.MethodGet, "/v1/orgs/acme", nil, http.StatusOK)
	danTok.must(http.MethodGet, "/v1/orgs/acme/tokens", nil, http.StatusOK) // admin
	// Con el token, Eva puede pedir un deploy a prod (queda awaiting_approval).
	evaTok.must(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/prod/deployments", map[string]string{"image": "sha-aaaaaaa"}, http.StatusCreated)

	// Sacan a Dan: su token deja de existir. La org es 404 para él y, como el
	// token se borró, cualquier request autenticado con él es 401.
	owner.must(http.MethodDelete, "/v1/orgs/acme/members/"+userID["dan@acme.com"], nil, http.StatusNoContent)
	if status, out := danTok.do(http.MethodGet, "/v1/orgs/acme", nil); status != http.StatusNotFound && status != http.StatusUnauthorized {
		t.Errorf("token de un ex miembro: esperaba 404 o 401, tengo %d %v", status, out)
	}
	if status, _ := danTok.do(http.MethodGet, "/v1/orgs/acme/apps", nil); status == http.StatusOK {
		t.Error("el token de un ex miembro sigue leyendo la org")
	}
	toks, err := env.srv.Store().ListAPITokens(context.Background(), fx.orgID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range toks {
		if tk.UserID == userID["dan@acme.com"] {
			t.Errorf("el token %s de Dan sobrevivió a la remoción", tk.Name)
		}
	}

	// Degradan a Eva a developer: su token admin baja con ella. Ya no puede
	// desplegar a prod ni listar tokens, pero sí seguir usando la org.
	owner.must(http.MethodPost, "/v1/orgs/acme/members", map[string]string{"email": "eva@acme.com", "role": "developer"}, http.StatusOK)
	if status, out := evaTok.do(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/prod/deployments", map[string]string{"image": "sha-bbbbbbb"}); status != http.StatusForbidden || errCode(out) != "forbidden" {
		t.Errorf("token degradado desplegando a prod: esperaba 403 forbidden, tengo %d %v", status, out)
	}
	if status, _ := evaTok.do(http.MethodGet, "/v1/orgs/acme/tokens", nil); status != http.StatusForbidden {
		t.Errorf("token degradado listando tokens (admin): %d", status)
	}
	evaTok.must(http.MethodGet, "/v1/orgs/acme/apps", nil, http.StatusOK)
	me := evaTok.must(http.MethodGet, "/v1/me", nil, http.StatusOK)
	if role := me["memberships"].([]any)[0].(map[string]any)["role"]; role != "developer" {
		t.Errorf("el rol del token guardado debería haber bajado a developer: %v", role)
	}
	toks, _ = env.srv.Store().ListAPITokens(context.Background(), fx.orgID)
	for _, tk := range toks {
		if tk.UserID == userID["eva@acme.com"] && tk.Role != "developer" {
			t.Errorf("el token de Eva quedó con rol %s", tk.Role)
		}
	}

	// El rol efectivo es el menor entre token y membresía también sin tocar el
	// token: si la membresía baja por debajo (viewer), el token no otorga developer.
	owner.must(http.MethodPost, "/v1/orgs/acme/members", map[string]string{"email": "eva@acme.com", "role": "viewer"}, http.StatusOK)
	if status, _ := evaTok.do(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/staging/deployments", map[string]string{"image": "sha-ccccccc"}); status != http.StatusForbidden {
		t.Errorf("token de un viewer desplegando: %d", status)
	}
}

// TestDeploymentLifecycleGuards cubre la máquina de estados frente al agente:
// cancel de un comando ya acked (409), acked huérfanos al reconectar, reaper,
// cancel que restaura el último healthy, approve con scope movido o repetido,
// y un scope en deleting que no se pisa con estados de deploy.
func TestDeploymentLifecycleGuards(t *testing.T) {
	env := newTestEnv(t)
	fx := newOrgFixture(t, env, "acme", "ana@acme.com")
	c := fx.owner
	const deployPath = "/v1/orgs/acme/apps/checkout-api/scopes/dev/deployments"

	// Tag inválido: 400 antes de tocar nada.
	if status, out := c.do(http.MethodPost, deployPath, map[string]string{"image": "v1:latest"}); status != http.StatusBadRequest || errCode(out) != "invalid" {
		t.Errorf("tag inválido: %d %v", status, out)
	}
	if status, _ := c.do(http.MethodPost, "/v1/orgs/acme/apps", map[string]string{"manifest": exampleManifest(t), "repo_url": "git@github.com:acme/x.git"}); status != http.StatusBadRequest {
		t.Errorf("repo_url no http(s): %d", status)
	}

	// 1. Deploy #1 completo: queda healthy.
	dep1 := str(c.must(http.MethodPost, deployPath, map[string]string{"image": "sha-0000001"}, http.StatusCreated), "id")
	agent := dialFakeAgent(t, env, fx.clusterToken, nil)
	if agent.welcome.PendingCommands != 1 {
		t.Fatalf("welcome: %+v", agent.welcome)
	}
	cmd1 := agent.readCommand()
	agent.ack(cmd1.ID)
	agent.send(protocol.KindResult, cmd1.ID, protocol.Result{CommandID: cmd1.ID, Status: protocol.ResultSucceeded, Workload: &protocol.WorkloadStatus{Kind: "Deployment", Phase: protocol.PhaseHealthy, Desired: 1, Ready: 1}})
	waitUntil(t, "dep1 healthy", func() bool { s, _ := env.deploymentStatus(fx.orgID, dep1); return s == store.DeploymentHealthy })

	// 2. Deploy #2: el agente hace ack y se cae sin result. Cancelar un
	// comando ya tomado es 409 y no toca nada.
	dep2 := str(c.must(http.MethodPost, deployPath, map[string]string{"image": "sha-0000002"}, http.StatusCreated), "id")
	cmd2 := agent.readCommand()
	agent.ack(cmd2.ID)
	waitUntil(t, "cmd2 acked", func() bool { return env.commandStatus(fx.orgID, dep2) == store.CommandAcked })
	if status, out := c.do(http.MethodPost, "/v1/orgs/acme/deployments/"+dep2+"/cancel", nil); status != http.StatusConflict || errCode(out) != "conflict" {
		t.Errorf("cancel con comando acked: esperaba 409 conflict, tengo %d %v", status, out)
	}
	if s, _ := env.deploymentStatus(fx.orgID, dep2); s != store.DeploymentQueued {
		t.Errorf("dep2 debería seguir queued tras el cancel rechazado: %s", s)
	}
	agent.crash()
	waitUntil(t, "cluster offline", func() bool {
		cl, err := env.srv.Store().GetCluster(context.Background(), fx.orgID, fx.clusterID)
		return err == nil && cl.Status == store.ClusterOffline
	})

	// 3. Reconecta sin cmd2 en curso: el hub lo da por perdido y falla el
	// deployment con el mensaje del protocolo. El welcome ya no lo anuncia.
	agent = dialFakeAgent(t, env, fx.clusterToken, []string{})
	if agent.welcome.PendingCommands != 0 {
		t.Errorf("tras reconciliar no debería haber pendientes: %+v", agent.welcome)
	}
	waitUntil(t, "dep2 failed por huérfano", func() bool { s, _ := env.deploymentStatus(fx.orgID, dep2); return s == store.DeploymentFailed })
	if _, msg := env.deploymentStatus(fx.orgID, dep2); msg != hub.OrphanMessage {
		t.Errorf("mensaje de dep2 = %q", msg)
	}
	if env.commandStatus(fx.orgID, dep2) != store.CommandFailed {
		t.Errorf("cmd2 debería estar failed: %s", env.commandStatus(fx.orgID, dep2))
	}
	evs := c.must(http.MethodGet, "/v1/orgs/acme/events?subject_type=deployment&subject_id="+dep2, nil, http.StatusOK)
	sawFailed := false
	for _, e := range itemsOf(evs) {
		if str(e, "kind") == "deployment.failed" {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Errorf("falta el evento deployment.failed del huérfano: %v", evs)
	}
	if sc := scopeByName(c, "acme", "checkout-api", "dev"); sc["status"] != "failed" {
		t.Errorf("scope dev tras el huérfano: %v", sc["status"])
	}

	// 3b. Un acked que SÍ está en curso no se toca al reconectar.
	dep3 := str(c.must(http.MethodPost, deployPath, map[string]string{"image": "sha-0000003"}, http.StatusCreated), "id")
	cmd3 := agent.readCommand()
	agent.ack(cmd3.ID)
	waitUntil(t, "cmd3 acked", func() bool { return env.commandStatus(fx.orgID, dep3) == store.CommandAcked })
	agent.crash()
	waitUntil(t, "offline", func() bool {
		cl, err := env.srv.Store().GetCluster(context.Background(), fx.orgID, fx.clusterID)
		return err == nil && cl.Status == store.ClusterOffline
	})
	agent = dialFakeAgent(t, env, fx.clusterToken, []string{cmd3.ID})
	time.Sleep(200 * time.Millisecond)
	if s, _ := env.deploymentStatus(fx.orgID, dep3); s != store.DeploymentQueued {
		t.Errorf("dep3 estaba en curso y debería seguir queued: %s", s)
	}
	agent.send(protocol.KindResult, cmd3.ID, protocol.Result{CommandID: cmd3.ID, Status: protocol.ResultSucceeded, Workload: &protocol.WorkloadStatus{Kind: "Deployment", Phase: protocol.PhaseHealthy}})
	waitUntil(t, "dep3 healthy", func() bool { s, _ := env.deploymentStatus(fx.orgID, dep3); return s == store.DeploymentHealthy })

	// 4. Deploy #4 enviado pero sin ack: cancel OK, el comando queda failed y
	// el scope vuelve al último healthy (dep3) en vez de quedar idle con un
	// current_deployment cancelado.
	dep4 := str(c.must(http.MethodPost, deployPath, map[string]string{"image": "sha-0000004"}, http.StatusCreated), "id")
	cmd4 := agent.readCommand()
	cancelled := c.must(http.MethodPost, "/v1/orgs/acme/deployments/"+dep4+"/cancel", nil, http.StatusOK)
	if cancelled["status"] != "cancelled" {
		t.Fatalf("cancel: %v", cancelled)
	}
	if env.commandStatus(fx.orgID, dep4) != store.CommandFailed {
		t.Errorf("cmd4 debería estar failed tras cancelar: %s", env.commandStatus(fx.orgID, dep4))
	}
	sc := scopeByName(c, "acme", "checkout-api", "dev")
	cur, _ := sc["current_deployment"].(map[string]any)
	if sc["status"] != "healthy" || cur == nil || str(cur, "id") != dep3 {
		t.Errorf("tras cancelar, el scope debería volver a dep3 healthy: status=%v current=%v", sc["status"], cur)
	}
	// Un result tardío del agente para el cancelado no lo revive.
	agent.ack(cmd4.ID)
	agent.send(protocol.KindResult, cmd4.ID, protocol.Result{CommandID: cmd4.ID, Status: protocol.ResultSucceeded})
	agent.send(protocol.KindHeartbeat, "", protocol.Heartbeat{Nodes: 1})
	time.Sleep(200 * time.Millisecond)
	if s, _ := env.deploymentStatus(fx.orgID, dep4); s != store.DeploymentCancelled {
		t.Errorf("dep4 debería seguir cancelled: %s", s)
	}

	// 5. Reaper: un comando acked cuya edad supera timeout_seconds (180 en
	// estos tests) más el margen se cierra como failed aunque el agente siga
	// conectado. Uno reciente no se toca.
	dep5 := str(c.must(http.MethodPost, deployPath, map[string]string{"image": "sha-0000005"}, http.StatusCreated), "id")
	cmd5 := agent.readCommand()
	agent.ack(cmd5.ID)
	waitUntil(t, "cmd5 acked", func() bool { return env.commandStatus(fx.orgID, dep5) == store.CommandAcked })
	reaped, err := env.srv.Backend().ReapStaleCommands(context.Background(), 3*time.Minute, ReaperGrace)
	if err != nil || reaped != 0 {
		t.Errorf("el reaper no debería tocar un acked reciente: n=%d err=%v", reaped, err)
	}
	if _, err := env.srv.Store().Pool().Exec(context.Background(), `UPDATE agent_commands SET acked_at = now() - interval '1 hour', sent_at = now() - interval '1 hour' WHERE id = $1`, cmd5.ID); err != nil {
		t.Fatal(err)
	}
	reaped, err = env.srv.Backend().ReapStaleCommands(context.Background(), 3*time.Minute, ReaperGrace)
	if err != nil || reaped != 1 {
		t.Fatalf("reaper: n=%d err=%v", reaped, err)
	}
	if s, msg := env.deploymentStatus(fx.orgID, dep5); s != store.DeploymentFailed || !strings.Contains(msg, hub.StaleMessage) {
		t.Errorf("dep5 tras el reaper: %s %q", s, msg)
	}
	if env.commandStatus(fx.orgID, dep5) != store.CommandFailed {
		t.Errorf("cmd5 debería estar failed: %s", env.commandStatus(fx.orgID, dep5))
	}
	// El result tardío del agente sobre el comando ya cerrado se ignora.
	agent.send(protocol.KindResult, cmd5.ID, protocol.Result{CommandID: cmd5.ID, Status: protocol.ResultSucceeded})
	agent.send(protocol.KindHeartbeat, "", protocol.Heartbeat{Nodes: 1})
	time.Sleep(200 * time.Millisecond)
	if s, _ := env.deploymentStatus(fx.orgID, dep5); s != store.DeploymentFailed {
		t.Errorf("dep5 no debería revivir con un result tardío: %s", s)
	}

	// 6. Approve: el scope se movió de cluster mientras esperaba aprobación -> 409.
	prod1 := c.must(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "eks-prod-1", "env": "prod", "cloud_account_id": fx.accID, "domain": "apps.acme.com"}, http.StatusCreated)
	prodDep := str(c.must(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/prod/deployments", map[string]string{"image": "sha-0000006"}, http.StatusCreated), "id")
	prod2 := c.must(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "eks-prod-2", "env": "prod", "cloud_account_id": fx.accID, "domain": "apps2.acme.com"}, http.StatusCreated)
	c.must(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/scopes/prod", map[string]string{"cluster_id": str(prod2, "id")}, http.StatusOK)
	if status, out := c.do(http.MethodPost, "/v1/orgs/acme/deployments/"+prodDep+"/approve", nil); status != http.StatusConflict {
		t.Errorf("approve con el scope movido a otro cluster: esperaba 409, tengo %d %v", status, out)
	}
	if s, _ := env.deploymentStatus(fx.orgID, prodDep); s != store.DeploymentAwaitingApproval {
		t.Errorf("el approve rechazado no debe cambiar el estado: %s", s)
	}
	c.must(http.MethodPost, "/v1/orgs/acme/deployments/"+prodDep+"/cancel", nil, http.StatusOK)
	// Approve repetido: la segunda vez el deployment ya no está en awaiting_approval.
	prodDep2 := str(c.must(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/prod/deployments", map[string]string{"image": "sha-0000007"}, http.StatusCreated), "id")
	c.must(http.MethodPost, "/v1/orgs/acme/deployments/"+prodDep2+"/approve", nil, http.StatusOK)
	if status, out := c.do(http.MethodPost, "/v1/orgs/acme/deployments/"+prodDep2+"/approve", nil); status != http.StatusConflict || !strings.Contains(fmt.Sprint(out), "queued") {
		t.Errorf("segundo approve: esperaba 409 mencionando queued, tengo %d %v", status, out)
	}
	cmds, _ := env.srv.Store().ListAgentCommandsForDeployment(context.Background(), fx.orgID, prodDep2)
	if len(cmds) != 1 || cmds[0].ClusterID != str(prod2, "id") {
		t.Errorf("un solo comando, en el cluster actual del scope: %+v", cmds)
	}
	_ = prod1

	// 7. Borrar la app con un scope desplegado es conflicto (chequeo y borrado atómicos).
	if status, _ := c.do(http.MethodDelete, "/v1/orgs/acme/apps/checkout-api", nil); status != http.StatusConflict {
		t.Errorf("borrar app con scopes desplegados: %d", status)
	}

	// 8. Un scope en deleting no se pisa con estados de deploy, ni por el
	// store ni encolando otro deploy.
	del := c.must(http.MethodDelete, "/v1/orgs/acme/apps/checkout-api/scopes/dev", nil, http.StatusAccepted)
	if del["status"] != "deleting" {
		t.Fatalf("delete scope: %v", del)
	}
	appRow, err := env.srv.Store().GetApplicationByName(context.Background(), fx.orgID, "checkout-api")
	if err != nil {
		t.Fatal(err)
	}
	devScope, err := env.srv.Store().GetScope(context.Background(), fx.orgID, appRow.ID, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.srv.Store().SetScopeDeployStatus(context.Background(), fx.orgID, devScope.ID, store.ScopeHealthy, dep3); err != store.ErrConflict {
		t.Errorf("SetScopeDeployStatus sobre deleting: esperaba ErrConflict, tengo %v", err)
	}
	if sc := scopeByName(c, "acme", "checkout-api", "dev"); sc["status"] != "deleting" {
		t.Errorf("el scope debería seguir deleting: %v", sc["status"])
	}
	if status, out := c.do(http.MethodPost, deployPath, map[string]string{"image": "sha-0000008"}); status != http.StatusConflict {
		t.Errorf("deploy sobre un scope en deleting: esperaba 409, tengo %d %v", status, out)
	}
	// El agente completa el undeploy: el scope permanente vuelve a idle.
	undeploy := agent.readCommand()
	if undeploy.Kind != protocol.CommandUndeploy {
		t.Fatalf("esperaba undeploy, tengo %+v", undeploy)
	}
	agent.ack(undeploy.ID)
	agent.send(protocol.KindResult, undeploy.ID, protocol.Result{CommandID: undeploy.ID, Status: protocol.ResultSucceeded})
	waitUntil(t, "scope idle tras undeploy", func() bool { return scopeByName(c, "acme", "checkout-api", "dev")["status"] == "idle" })
}

// TestRotateTokenAndDeleteClusterDisconnectAgent: la conexión viva del agente
// se corta al rotar el token o borrar el cluster; el token viejo ya no entra.
func TestRotateTokenAndDeleteClusterDisconnectAgent(t *testing.T) {
	env := newTestEnv(t)
	fx := newOrgFixture(t, env, "acme", "ana@acme.com")
	c := fx.owner

	agent := dialFakeAgent(t, env, fx.clusterToken, nil)
	waitUntil(t, "online", func() bool { return env.srv.Hub().Online(fx.clusterID) })
	rotated := c.must(http.MethodPost, "/v1/orgs/acme/clusters/"+fx.clusterID+"/rotate-token", nil, http.StatusOK)
	newToken := str(rotated, "token")
	if !strings.Contains(str(rotated, "install_command"), "--set-string image.repository=") {
		t.Errorf("install_command debería usar --set-string para image.*: %s", str(rotated, "install_command"))
	}
	if _, err := agent.readErr(); err == nil || websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Errorf("la conexión vieja debería cerrarse con PolicyViolation al rotar, tengo %v", err)
	}
	waitUntil(t, "offline tras rotar", func() bool { return !env.srv.Hub().Online(fx.clusterID) })

	// El token viejo ya no autentica; el nuevo sí.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/v1/agent/connect"
	if _, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + fx.clusterToken}}}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("token viejo tras rotar: err=%v resp=%v", err, resp)
	}
	agent = dialFakeAgent(t, env, newToken, nil)
	waitUntil(t, "online con el token nuevo", func() bool { return env.srv.Hub().Online(fx.clusterID) })

	// Borrar un cluster conectado también corta la conexión.
	acc := c.must(http.MethodPost, "/v1/orgs/acme/cloud-accounts", map[string]string{"provider": "aws", "name": "otra", "region": "us-east-1"}, http.StatusCreated)
	cl2 := c.must(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "eks-temp", "env": "nonprod", "cloud_account_id": str(acc, "id"), "domain": "tmp.acme.com"}, http.StatusCreated)
	agent2 := dialFakeAgent(t, env, str(cl2, "token"), nil)
	waitUntil(t, "cl2 online", func() bool { return env.srv.Hub().Online(str(cl2, "id")) })
	c.must(http.MethodDelete, "/v1/orgs/acme/clusters/"+str(cl2, "id"), nil, http.StatusNoContent)
	if _, err := agent2.readErr(); err == nil || websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Errorf("borrar el cluster debería cortar al agente con PolicyViolation, tengo %v", err)
	}
	_ = agent
}

// TestEventStreamClosesWhenAccessRevoked: el SSE re-chequea el acceso en cada
// ping y se corta si sacaron al miembro o borraron el token.
func TestEventStreamClosesWhenAccessRevoked(t *testing.T) {
	prev := httpapi.SSEPingInterval
	httpapi.SSEPingInterval = 100 * time.Millisecond
	t.Cleanup(func() { httpapi.SSEPingInterval = prev })

	env := newTestEnv(t)
	owner := env.newClient()
	owner.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "ana@sse.com"}, http.StatusOK)
	owner.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "sse", "name": "SSE"}, http.StatusCreated)
	owner.must(http.MethodPost, "/v1/orgs/sse/members", map[string]string{"email": "bob@sse.com", "role": "viewer"}, http.StatusOK)
	members := owner.must(http.MethodGet, "/v1/orgs/sse/members", nil, http.StatusOK)
	var bobID string
	for _, m := range itemsOf(members) {
		if u := m["user"].(map[string]any); str(u, "email") == "bob@sse.com" {
			bobID = str(u, "id")
		}
	}

	openStream := func(c *client) (<-chan string, func()) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, env.ts.URL+"/v1/orgs/sse/events/stream", nil)
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			cancel()
			t.Fatalf("stream: %d", resp.StatusCode)
		}
		lines := make(chan string, 64)
		go func() {
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				lines <- sc.Text()
			}
			close(lines)
		}()
		return lines, func() { resp.Body.Close(); cancel() }
	}
	waitClosed := func(lines <-chan string, what string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case _, ok := <-lines:
				if !ok {
					return
				}
			case <-deadline:
				t.Fatalf("el stream debería haberse cerrado: %s", what)
			}
		}
	}

	// Miembro: abre el stream, recibe pings, lo sacan, el stream se cierra.
	bob := env.newClient()
	bob.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "bob@sse.com"}, http.StatusOK)
	lines, done := openStream(bob)
	defer done()
	if l := <-lines; l != ": connected" {
		t.Fatalf("primera línea: %q", l)
	}
	waitUntil(t, "un ping", func() bool {
		select {
		case l := <-lines:
			return l == ": ping"
		default:
			return false
		}
	})
	owner.must(http.MethodDelete, "/v1/orgs/sse/members/"+bobID, nil, http.StatusNoContent)
	waitClosed(lines, "miembro removido")

	// Token: abre el stream, borran el token, el stream se cierra.
	tok := owner.must(http.MethodPost, "/v1/orgs/sse/tokens", map[string]any{"name": "ci", "role": "viewer"}, http.StatusCreated)
	tc := env.newClient()
	tc.token = str(tok, "token")
	lines2, done2 := openStream(tc)
	defer done2()
	if l := <-lines2; l != ": connected" {
		t.Fatalf("primera línea: %q", l)
	}
	owner.must(http.MethodDelete, "/v1/orgs/sse/tokens/"+str(tok, "id"), nil, http.StatusNoContent)
	waitClosed(lines2, "token borrado")
}
