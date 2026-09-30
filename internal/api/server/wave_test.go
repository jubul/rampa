package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"helm.sh/helm/v3/pkg/chart/loader"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/hub"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/protocol"
	"rampa.dev/pkg/provider"
	"rampa.dev/pkg/provider/digitalocean"
)

// fakeDigitalOcean simula GET /v2/account de la API de DigitalOcean y registra
// el último token que recibió.
func fakeDigitalOcean(t *testing.T, validToken string) (*httptest.Server, *string) {
	t.Helper()
	var last string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.URL.Path != "/v2/account" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if last != validToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"id":"Unauthorized","message":"Unable to authenticate you"}`))
			return
		}
		_, _ = w.Write([]byte(`{"account":{"email":"ops@acme.com","uuid":"u-1","status":"active"}}`))
	}))
	t.Cleanup(ts.Close)
	return ts, &last
}

// TestTokenProviderCredentials: cuenta de DigitalOcean con el token cifrado.
// Sin RAMPA_STATE_KEY el provider responde 501; con clave, el PATCH cifra el
// token (la fila no lo tiene en claro), ninguna respuesta lo devuelve,
// has_credentials pasa a true, verify lo usa contra la API falsa, volver a
// mandarlo rota y DELETE limpia.
func TestTokenProviderCredentials(t *testing.T) {
	const token1, token2 = "dop_v1_primer_token_secreto", "dop_v1_segundo_token_rotado"
	doAPI, lastToken := fakeDigitalOcean(t, token1)

	// Sin clave: 501 explicando qué falta.
	envNoKey := newTestEnv(t)
	provider.Register(digitalocean.New(digitalocean.Config{BaseURL: doAPI.URL}))
	c0 := envNoKey.newClient()
	c0.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "ana@acme.com"}, http.StatusOK)
	c0.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "acme", "name": "Acme"}, http.StatusCreated)
	status, out := c0.do(http.MethodPost, "/v1/orgs/acme/cloud-accounts", map[string]string{"provider": "digitalocean", "name": "acme-do", "region": "nyc3"})
	if status != http.StatusNotImplemented || errCode(out) != "not_implemented" || !strings.Contains(out["error"].(map[string]any)["message"].(string), "RAMPA_STATE_KEY") {
		t.Fatalf("sin clave: %d %v", status, out)
	}
	// Un provider desconocido sigue siendo invalid, con la lista completa.
	if status, out := c0.do(http.MethodPost, "/v1/orgs/acme/cloud-accounts", map[string]string{"provider": "linode", "name": "x"}); status != http.StatusBadRequest || !strings.Contains(out["error"].(map[string]any)["message"].(string), "digitalocean") {
		t.Errorf("provider desconocido: %d %v", status, out)
	}

	env := newTestEnvWith(t, map[string]string{"RAMPA_STATE_KEY": testStateKey()})
	provider.Register(digitalocean.New(digitalocean.Config{BaseURL: doAPI.URL}))
	c := env.newClient()
	c.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "ana@acme.com"}, http.StatusOK)
	c.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "acme", "name": "Acme"}, http.StatusCreated)

	// Un secreto mandado al crear no se guarda en settings.
	acc := c.must(http.MethodPost, "/v1/orgs/acme/cloud-accounts", map[string]any{"provider": "digitalocean", "name": "acme-do", "region": "nyc3", "settings": map[string]string{"api_token": "no-va-aca", "registry_name": "acme"}}, http.StatusCreated)
	accID := str(acc, "id")
	if acc["has_credentials"] != false || acc["settings"].(map[string]any)["api_token"] != nil || acc["settings"].(map[string]any)["registry_name"] != "acme" {
		t.Fatalf("cuenta creada: %v", acc)
	}
	ins, _ := acc["instructions"].(map[string]any)
	fields, _ := ins["fields"].([]any)
	if len(fields) != 2 || fields[0].(map[string]any)["name"] != "api_token" || fields[0].(map[string]any)["secret"] != true || fields[1].(map[string]any)["secret"] != false {
		t.Errorf("instructions.fields: %v", fields)
	}

	// Verify antes del token: invalid diciendo qué falta.
	status, out = c.do(http.MethodPost, "/v1/orgs/acme/cloud-accounts/"+accID+"/verify", nil)
	if status != http.StatusBadRequest || !strings.Contains(out["error"].(map[string]any)["message"].(string), "api_token") {
		t.Fatalf("verify sin token: %d %v", status, out)
	}
	// Un campo que no es secreto no va en credentials.
	if status, _ := c.do(http.MethodPatch, "/v1/orgs/acme/cloud-accounts/"+accID, map[string]any{"credentials": map[string]string{"registry_name": "x"}}); status != http.StatusBadRequest {
		t.Errorf("credentials con un campo no secreto: %d", status)
	}

	// PATCH con el token: se cifra, no vuelve, has_credentials true.
	acc = c.must(http.MethodPatch, "/v1/orgs/acme/cloud-accounts/"+accID, map[string]any{"credentials": map[string]string{"api_token": token1}}, http.StatusOK)
	raw, _ := json.Marshal(acc)
	if acc["has_credentials"] != true || strings.Contains(string(raw), token1) || acc["status"] != "pending" {
		t.Fatalf("PATCH con credenciales: %s", raw)
	}
	got := c.must(http.MethodGet, "/v1/orgs/acme/cloud-accounts/"+accID, nil, http.StatusOK)
	raw, _ = json.Marshal(got)
	if got["has_credentials"] != true || strings.Contains(string(raw), token1) || got["settings"].(map[string]any)["api_token"] != nil {
		t.Errorf("GET devuelve el token: %s", raw)
	}
	list := c.must(http.MethodGet, "/v1/orgs/acme/cloud-accounts", nil, http.StatusOK)
	if raw, _ := json.Marshal(list); strings.Contains(string(raw), token1) {
		t.Error("el listado devuelve el token")
	}
	// La fila está cifrada: el ciphertext existe y no contiene el token.
	orgRow, err := env.srv.Store().GetOrgBySlug(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	ct, err := env.srv.Store().GetCloudAccountCredentials(context.Background(), orgRow.ID, accID)
	if err != nil || len(ct) == 0 || bytes.Contains(ct, []byte(token1)) || bytes.Contains(ct, []byte("api_token")) {
		t.Fatalf("credentials_ciphertext: %v %d bytes", err, len(ct))
	}
	var settingsRaw string
	if err := env.srv.Store().Pool().QueryRow(context.Background(), `SELECT settings::text FROM cloud_accounts WHERE id = $1`, accID).Scan(&settingsRaw); err != nil || strings.Contains(settingsRaw, token1) || strings.Contains(settingsRaw, "no-va-aca") {
		t.Errorf("settings en la base: %v %s", err, settingsRaw)
	}
	// Ni los eventos lo llevan.
	evs := c.must(http.MethodGet, "/v1/orgs/acme/events?subject_type=cloud_account&subject_id="+accID, nil, http.StatusOK)
	if raw, _ := json.Marshal(evs); strings.Contains(string(raw), token1) {
		t.Error("un evento contiene el token")
	}

	// Verify usa el token descifrado contra la API falsa de DigitalOcean.
	acc = c.must(http.MethodPost, "/v1/orgs/acme/cloud-accounts/"+accID+"/verify", nil, http.StatusOK)
	if acc["status"] != "connected" || acc["verification"].(map[string]any)["identity"] != "ops@acme.com" || *lastToken != token1 {
		t.Fatalf("verify: %v (token recibido por DO: %q)", acc, *lastToken)
	}

	// Volver a mandar el token lo rota: el viejo deja de servir.
	c.must(http.MethodPatch, "/v1/orgs/acme/cloud-accounts/"+accID, map[string]any{"settings": map[string]string{"api_token": token2}}, http.StatusOK)
	acc = c.must(http.MethodPost, "/v1/orgs/acme/cloud-accounts/"+accID+"/verify", nil, http.StatusOK)
	if acc["status"] != "failed" || *lastToken != token2 {
		t.Fatalf("tras rotar, DO debería recibir el token nuevo y rechazarlo: %v %q", acc, *lastToken)
	}
	ct2, _ := env.srv.Store().GetCloudAccountCredentials(context.Background(), orgRow.ID, accID)
	if bytes.Equal(ct, ct2) || bytes.Contains(ct2, []byte(token2)) {
		t.Error("la rotación debería cambiar el cifrado sin exponer el token")
	}

	// Un developer no puede tocar credenciales; el registry sale de settings.
	tok := c.must(http.MethodPost, "/v1/orgs/acme/tokens", map[string]any{"name": "dev", "role": "developer"}, http.StatusCreated)
	dev := env.newClient()
	dev.token = str(tok, "token")
	if status, _ := dev.do(http.MethodPatch, "/v1/orgs/acme/cloud-accounts/"+accID, map[string]any{"credentials": map[string]string{"api_token": "x"}}); status != http.StatusForbidden {
		t.Errorf("developer rotando credenciales: %d", status)
	}

	// DELETE borra la fila y con ella el cifrado.
	c.must(http.MethodDelete, "/v1/orgs/acme/cloud-accounts/"+accID, nil, http.StatusNoContent)
	if _, err := env.srv.Store().GetCloudAccountCredentials(context.Background(), orgRow.ID, accID); err != store.ErrNotFound {
		t.Errorf("tras DELETE no debería quedar nada: %v", err)
	}
}

// TestBootstrapTokens: emisión de bootstrap tokens (admin), canje sin sesión
// que genera un token de cluster nuevo y corta al agente viejo, un solo uso,
// límite de intentos, install-command con bootstrapToken sin rotar el token.
func TestBootstrapTokens(t *testing.T) {
	env := newTestEnv(t)
	fx := newOrgFixture(t, env, "acme", "ana@acme.com")
	c := fx.owner
	ctx := context.Background()

	// POST /clusters acepta kind; vm queda registrado como tal.
	vm := c.must(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "droplet-dev", "kind": "vm", "env": "nonprod", "cloud_account_id": fx.accID, "domain": "vm.acme.com", "secret_store": "kubernetes"}, http.StatusCreated)
	if vm["kind"] != "vm" || vm["secret_store"] != "kubernetes" {
		t.Fatalf("cluster vm: %v", vm)
	}
	if status, _ := c.do(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "raro", "kind": "lambda", "env": "nonprod", "cloud_account_id": fx.accID, "domain": "d"}); status != http.StatusBadRequest {
		t.Errorf("kind inválido: %d", status)
	}
	cl := c.must(http.MethodGet, "/v1/orgs/acme/clusters/"+fx.clusterID, nil, http.StatusOK)
	if cl["kind"] != "kubernetes" {
		t.Errorf("kind por defecto: %v", cl["kind"])
	}

	// Agente conectado con el token original.
	agent := dialFakeAgent(t, env, fx.clusterToken, nil)
	waitUntil(t, "online", func() bool { return env.srv.Hub().Online(fx.clusterID) })
	before, err := env.srv.Store().GetCluster(ctx, fx.orgID, fx.clusterID)
	if err != nil {
		t.Fatal(err)
	}

	// install-command: bootstrap nuevo, sin rotar el token del cluster.
	ic := c.must(http.MethodGet, "/v1/orgs/acme/clusters/"+fx.clusterID+"/install-command", nil, http.StatusOK)
	install := str(ic, "install_command")
	if !strings.Contains(install, "--set-string cluster.bootstrapToken=rampa_bootstrap_") || strings.Contains(install, "cluster.token=") || ic["expires_at"] == nil {
		t.Fatalf("install_command: %v", ic)
	}
	if after, _ := env.srv.Store().GetCluster(ctx, fx.orgID, fx.clusterID); after.TokenHash != before.TokenHash {
		t.Error("install-command no debe rotar el token del cluster")
	}
	if !env.srv.Hub().Online(fx.clusterID) {
		t.Error("install-command no debe cortar al agente")
	}

	// Emitir un bootstrap token: admin sí, developer no.
	tok := c.must(http.MethodPost, "/v1/orgs/acme/tokens", map[string]any{"name": "dev", "role": "developer"}, http.StatusCreated)
	dev := env.newClient()
	dev.token = str(tok, "token")
	if status, _ := dev.do(http.MethodPost, "/v1/orgs/acme/clusters/"+fx.clusterID+"/bootstrap-token", nil); status != http.StatusForbidden {
		t.Errorf("developer emitiendo bootstrap: %d", status)
	}
	bt := c.must(http.MethodPost, "/v1/orgs/acme/clusters/"+fx.clusterID+"/bootstrap-token", nil, http.StatusCreated)
	bootstrap := str(bt, "bootstrap_token")
	if !strings.HasPrefix(bootstrap, "rampa_bootstrap_") || bt["expires_at"] == nil {
		t.Fatalf("bootstrap token: %v", bt)
	}
	exp, _ := time.Parse(time.RFC3339, str(bt, "expires_at"))
	if d := time.Until(exp); d < 110*time.Minute || d > 121*time.Minute {
		t.Errorf("vence en %s, quería 2 horas", d)
	}
	// Solo el hash queda en la base.
	var n int
	if err := env.srv.Store().Pool().QueryRow(ctx, `SELECT count(*) FROM cluster_bootstrap_tokens WHERE token_hash = $1 AND used_at IS NULL`, hashOf(bootstrap)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("fila del bootstrap: %v %d", err, n)
	}

	// Canje sin sesión: token de cluster nuevo, el agente viejo se corta.
	anon := env.newClient()
	status, out := anon.do(http.MethodPost, "/v1/agent/bootstrap", map[string]string{"bootstrap_token": bootstrap})
	if status != http.StatusOK || !strings.HasPrefix(str(out, "cluster_token"), "rampa_cluster_") || out["cluster_id"] != fx.clusterID || out["api_url"] != "http://api.test.local" {
		t.Fatalf("bootstrap: %d %v", status, out)
	}
	newToken := str(out, "cluster_token")
	if _, err := agent.readErr(); err == nil || websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Errorf("el agente viejo debería cortarse con PolicyViolation: %v", err)
	}
	waitUntil(t, "offline tras el canje", func() bool { return !env.srv.Hub().Online(fx.clusterID) })
	after, _ := env.srv.Store().GetCluster(ctx, fx.orgID, fx.clusterID)
	if after.TokenHash == before.TokenHash || after.TokenHash != hashOf(newToken) {
		t.Error("el canje debería rotar el token del cluster al nuevo")
	}
	// El token viejo ya no entra; el nuevo sí.
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/v1/agent/connect"
	if _, resp, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + fx.clusterToken}}}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("token viejo tras el canje: err=%v resp=%v", err, resp)
	}
	agent2 := dialFakeAgent(t, env, newToken, nil)
	waitUntil(t, "online con el token canjeado", func() bool { return env.srv.Hub().Online(fx.clusterID) })
	agent2.close()

	// Segundo canje del mismo bootstrap: 401. Un token inventado: 401.
	if status, out := anon.do(http.MethodPost, "/v1/agent/bootstrap", map[string]string{"bootstrap_token": bootstrap}); status != http.StatusUnauthorized || errCode(out) != "unauthorized" {
		t.Errorf("segundo canje: %d %v", status, out)
	}
	if status, _ := anon.do(http.MethodPost, "/v1/agent/bootstrap", map[string]string{"bootstrap_token": "rampa_bootstrap_" + strings.Repeat("0", 64)}); status != http.StatusUnauthorized {
		t.Errorf("bootstrap inventado: %d", status)
	}
	if status, _ := anon.do(http.MethodPost, "/v1/agent/bootstrap", map[string]string{"bootstrap_token": fx.clusterToken}); status != http.StatusUnauthorized {
		t.Errorf("un token de cluster no es un bootstrap: %d", status)
	}
	// Uno vencido: 401 aunque no se haya usado.
	expiredPlain, expiredHash, _ := auth.NewBootstrapToken()
	if _, err := env.srv.Store().CreateBootstrapToken(ctx, fx.orgID, fx.clusterID, expiredHash, time.Now().Add(-time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if status, _ := anon.do(http.MethodPost, "/v1/agent/bootstrap", map[string]string{"bootstrap_token": expiredPlain}); status != http.StatusUnauthorized {
		t.Errorf("bootstrap vencido: %d", status)
	}
	// El evento de rotación queda en el timeline del cluster.
	evs := c.must(http.MethodGet, "/v1/orgs/acme/events?subject_type=cluster&subject_id="+fx.clusterID, nil, http.StatusOK)
	sawRotation := false
	for _, e := range itemsOf(evs) {
		if str(e, "kind") == "cluster.token_rotated" && e["payload"].(map[string]any)["via"] == "bootstrap" {
			sawRotation = true
		}
	}
	if !sawRotation {
		t.Errorf("falta cluster.token_rotated via bootstrap: %v", evs)
	}

	// Límite: 10 intentos por minuto por IP; el resto responde 429.
	tries := 0
	for i := 0; i < 15; i++ {
		status, out := anon.do(http.MethodPost, "/v1/agent/bootstrap", map[string]string{"bootstrap_token": "rampa_bootstrap_" + strings.Repeat("f", 64)})
		if status == http.StatusTooManyRequests {
			if errCode(out) != "rate_limited" {
				t.Errorf("429 con código %s", errCode(out))
			}
			break
		}
		tries++
	}
	if tries > 10 {
		t.Errorf("el límite por IP debería cortar a los 10 intentos, hubo %d", tries)
	}
}

// TestScopeSecrets: PUT /secrets manda write_secret al agente por la conexión
// viva con los valores; la fila de agent_commands y el evento no los tienen;
// GET devuelve las claves; offline responde 409; prod requiere admin.
func TestScopeSecrets(t *testing.T) {
	env := newTestEnv(t)
	fx := newOrgFixture(t, env, "acme", "ana@acme.com")
	c := fx.owner
	ctx := context.Background()
	const secretsPath = "/v1/orgs/acme/apps/checkout-api/scopes/dev/secrets"
	const secretValue = "sk_live_SUPERSECRETO_123"

	// Sin agente: 409.
	if status, out := c.do(http.MethodPut, secretsPath, map[string]any{"values": map[string]string{"API_KEY": secretValue}}); status != http.StatusConflict || errCode(out) != "conflict" {
		t.Fatalf("offline: %d %v", status, out)
	}
	// Nombres inválidos y body vacío: 400.
	for _, bad := range []map[string]any{{"values": map[string]string{"api-key": "x"}}, {"values": map[string]string{"PORT": "x"}}, {"values": map[string]string{"RAMPA_X": "x"}}, {"values": map[string]string{}}} {
		if status, _ := c.do(http.MethodPut, secretsPath, bad); status != http.StatusBadRequest {
			t.Errorf("%v: esperaba 400, tengo %d", bad, status)
		}
	}
	// Scope no declarado: 400. Scope sin fila: GET da [].
	if status, _ := c.do(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/scopes/qa/secrets", map[string]any{"values": map[string]string{"A": "b"}}); status != http.StatusBadRequest {
		t.Error("scope no declarado debería ser 400")
	}
	if status, _ := c.do(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/scopes/staging/secrets", map[string]any{"values": map[string]string{"A": "b"}}); status != http.StatusBadRequest {
		t.Error("scope sin cluster debería ser 400")
	}
	if got := c.must(http.MethodGet, "/v1/orgs/acme/apps/checkout-api/scopes/staging/secrets", nil, http.StatusOK); len(got["keys"].([]any)) != 0 {
		t.Errorf("GET sin fila: %v", got)
	}

	agent := dialFakeAgent(t, env, fx.clusterToken, nil)
	waitUntil(t, "online", func() bool { return env.srv.Hub().Online(fx.clusterID) })

	out := c.must(http.MethodPut, secretsPath, map[string]any{"values": map[string]string{"API_KEY": secretValue, "DB_PASSWORD": "otro"}}, http.StatusAccepted)
	if keys := out["keys"].([]any); len(keys) != 2 || keys[0] != "API_KEY" || keys[1] != "DB_PASSWORD" {
		t.Fatalf("keys: %v", out)
	}
	// El agente recibe el comando con los valores y el target según secret_store.
	cmd := agent.readCommand()
	if cmd.Kind != protocol.CommandWriteSecret || cmd.WriteSecret == nil || cmd.WriteSecret.Values["API_KEY"] != secretValue || cmd.WriteSecret.Target != "cloud" || cmd.WriteSecret.Namespace != "checkout-api-dev" || cmd.WriteSecret.App != "checkout-api" || cmd.WriteSecret.Scope != "dev" {
		t.Fatalf("comando recibido: %+v", cmd)
	}
	// La fila de agent_commands no tiene los valores: tiene las claves.
	var payload string
	var kind, status string
	if err := env.srv.Store().Pool().QueryRow(ctx, `SELECT payload::text, kind, status FROM agent_commands WHERE id = $1`, cmd.ID).Scan(&payload, &kind, &status); err != nil {
		t.Fatal(err)
	}
	if kind != "write_secret" || strings.Contains(payload, secretValue) || strings.Contains(payload, "otro") || !strings.Contains(payload, `"keys"`) || !strings.Contains(payload, "API_KEY") || strings.Contains(payload, `"values"`) {
		t.Errorf("payload persistido: %s (%s)", payload, kind)
	}
	if status != store.CommandSent {
		t.Errorf("el comando debería estar sent: %s", status)
	}
	// GET devuelve las claves conocidas.
	got := c.must(http.MethodGet, secretsPath, nil, http.StatusOK)
	if keys := got["keys"].([]any); len(keys) != 2 || keys[0] != "API_KEY" {
		t.Errorf("GET keys: %v", got)
	}
	// El evento lleva solo las claves.
	appRow, _ := env.srv.Store().GetApplicationByName(ctx, fx.orgID, "checkout-api")
	devScope, _ := env.srv.Store().GetScope(ctx, fx.orgID, appRow.ID, "dev")
	evs := c.must(http.MethodGet, "/v1/orgs/acme/events?subject_type=scope&subject_id="+devScope.ID, nil, http.StatusOK)
	sawEvent := false
	for _, e := range itemsOf(evs) {
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), secretValue) {
			t.Fatalf("un evento contiene el valor: %s", raw)
		}
		if str(e, "kind") == "scope.secrets_written" {
			sawEvent = true
			p := e["payload"].(map[string]any)
			if p["app"] != "checkout-api" || p["scope"] != "dev" || p["target"] != "cloud" || len(p["keys"].([]any)) != 2 {
				t.Errorf("payload del evento: %v", p)
			}
		}
	}
	if !sawEvent {
		t.Errorf("falta scope.secrets_written: %v", evs)
	}
	// Tampoco en el log de la base entera queda el valor en claro.
	var leaks int
	if err := env.srv.Store().Pool().QueryRow(ctx, `SELECT count(*) FROM events WHERE payload::text LIKE '%'||$1||'%' OR message LIKE '%'||$1||'%'`, secretValue).Scan(&leaks); err != nil || leaks != 0 {
		t.Errorf("valores filtrados en events: %d %v", leaks, err)
	}
	// El agente confirma: el comando se cierra y el timeline lo anuncia.
	agent.ack(cmd.ID)
	agent.send(protocol.KindResult, cmd.ID, protocol.Result{CommandID: cmd.ID, Status: protocol.ResultSucceeded, Message: "2 claves escritas"})
	waitUntil(t, "comando done", func() bool {
		var st string
		_ = env.srv.Store().Pool().QueryRow(ctx, `SELECT status FROM agent_commands WHERE id = $1`, cmd.ID).Scan(&st)
		return st == store.CommandDone
	})
	// El comando no se reenvía al reconectar: no está en la cola.
	agent.close()
	waitUntil(t, "offline", func() bool { return !env.srv.Hub().Online(fx.clusterID) })
	agent = dialFakeAgent(t, env, fx.clusterToken, nil)
	if agent.welcome.PendingCommands != 0 {
		t.Errorf("write_secret no debería quedar pendiente: %+v", agent.welcome)
	}

	// Un cluster con secret_store kubernetes manda target kubernetes.
	prodCl := c.must(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "k3s-prod", "kind": "vm", "env": "prod", "cloud_account_id": fx.accID, "domain": "apps.acme.com", "secret_store": "kubernetes"}, http.StatusCreated)
	c.must(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/scopes/prod", map[string]string{"cluster_id": str(prodCl, "id")}, http.StatusOK)
	prodAgent := dialFakeAgent(t, env, str(prodCl, "token"), nil)
	waitUntil(t, "prod online", func() bool { return env.srv.Hub().Online(str(prodCl, "id")) })
	// prod requiere admin: un developer recibe 403.
	tok := c.must(http.MethodPost, "/v1/orgs/acme/tokens", map[string]any{"name": "dev", "role": "developer"}, http.StatusCreated)
	dev := env.newClient()
	dev.token = str(tok, "token")
	if status, _ := dev.do(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/scopes/prod/secrets", map[string]any{"values": map[string]string{"API_KEY": "x"}}); status != http.StatusForbidden {
		t.Errorf("developer escribiendo secretos de prod: %d", status)
	}
	dev.must(http.MethodGet, "/v1/orgs/acme/apps/checkout-api/scopes/prod/secrets", nil, http.StatusOK)
	c.must(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/scopes/prod/secrets", map[string]any{"values": map[string]string{"API_KEY": "prod-secret"}}, http.StatusAccepted)
	pcmd := prodAgent.readCommand()
	if pcmd.Kind != protocol.CommandWriteSecret || pcmd.WriteSecret.Target != "kubernetes" || pcmd.WriteSecret.Namespace != "checkout-api-prod" {
		t.Errorf("comando de prod: %+v", pcmd)
	}
	prodAgent.close()
	agent.close()
}

// TestScopeComputeAndServerless: la respuesta de scopes trae compute, y un
// deploy a un scope serverless responde 501.
func TestScopeComputeAndServerless(t *testing.T) {
	env := newTestEnv(t)
	fx := newOrgFixture(t, env, "acme", "ana@acme.com")
	c := fx.owner
	sc := scopeByName(c, "acme", "checkout-api", "dev")
	if sc["compute"] != "kubernetes" {
		t.Errorf("compute por defecto: %v", sc["compute"])
	}
	// Cambiamos staging a serverless por el spec.
	got := c.must(http.MethodGet, "/v1/orgs/acme/apps/checkout-api/spec", nil, http.StatusOK)
	spec := got["spec"].(map[string]any)
	for _, s := range spec["scopes"].([]any) {
		if m := s.(map[string]any); m["name"] == "staging" {
			m["compute"] = "serverless"
		}
	}
	c.must(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/spec", map[string]any{"spec": spec}, http.StatusOK)
	if sc := scopeByName(c, "acme", "checkout-api", "staging"); sc["compute"] != "serverless" {
		t.Errorf("staging debería ser serverless: %v", sc)
	}
	app := c.must(http.MethodGet, "/v1/orgs/acme/apps/checkout-api", nil, http.StatusOK)
	if !strings.Contains(str(app, "manifest"), "compute: serverless") {
		t.Errorf("el manifest guardado debería tener compute:\n%s", str(app, "manifest"))
	}
	status, out := c.do(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/staging/deployments", map[string]string{"image": "sha-abc1234"})
	if status != http.StatusNotImplemented || errCode(out) != "not_implemented" || !strings.Contains(out["error"].(map[string]any)["message"].(string), "ola dos") {
		t.Errorf("deploy a serverless: %d %v", status, out)
	}
	// Un compute inválido no pasa la validación del spec.
	for _, s := range spec["scopes"].([]any) {
		if m := s.(map[string]any); m["name"] == "staging" {
			m["compute"] = "lambda"
		}
	}
	if status, out := c.do(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/spec", map[string]any{"spec": spec}); status != http.StatusBadRequest {
		t.Errorf("compute inválido: %d %v", status, out)
	}
	// dev sigue desplegable.
	c.must(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/dev/deployments", map[string]string{"image": "sha-abc1234"}, http.StatusCreated)
}

// TestMeLocale: GET /me devuelve el idioma efectivo (usuario > Accept-Language > es)
// y PATCH /me lo guarda.
func TestMeLocale(t *testing.T) {
	env := newTestEnv(t)
	c := env.newClient()
	c.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "ana@acme.com"}, http.StatusOK)
	me := c.must(http.MethodGet, "/v1/me", nil, http.StatusOK)
	if me["locale"] != "es" || me["user"].(map[string]any)["locale"] != "" {
		t.Fatalf("locale por defecto: %v", me)
	}
	withLang := func(lang string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, env.ts.URL+"/v1/me", nil)
		req.Header.Set("Accept-Language", lang)
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	if got := withLang("en-US,en;q=0.9,es;q=0.8"); got["locale"] != "en" {
		t.Errorf("Accept-Language en: %v", got["locale"])
	}
	if got := withLang("fr-FR,de;q=0.9"); got["locale"] != "es" {
		t.Errorf("idioma no soportado cae a es: %v", got["locale"])
	}
	if got := withLang("fr, es-AR;q=0.5, en;q=0.9"); got["locale"] != "en" {
		t.Errorf("gana el q más alto soportado: %v", got["locale"])
	}
	if status, _ := c.do(http.MethodPatch, "/v1/me", map[string]string{"locale": "pt"}); status != http.StatusBadRequest {
		t.Errorf("locale inválido: %d", status)
	}
	me = c.must(http.MethodPatch, "/v1/me", map[string]string{"locale": "en"}, http.StatusOK)
	if me["locale"] != "en" || me["user"].(map[string]any)["locale"] != "en" {
		t.Errorf("PATCH locale: %v", me)
	}
	// La preferencia gana sobre el header.
	if got := withLang("es"); got["locale"] != "en" || got["user"].(map[string]any)["locale"] != "en" {
		t.Errorf("la preferencia guardada debería ganar: %v", got)
	}
	// Vaciarla vuelve al header.
	c.must(http.MethodPatch, "/v1/me", map[string]string{"locale": ""}, http.StatusOK)
	if got := withLang("es"); got["locale"] != "es" {
		t.Errorf("sin preferencia vuelve al header: %v", got)
	}
	// Un token no cambia preferencias.
	c.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "acme", "name": "Acme"}, http.StatusCreated)
	tok := c.must(http.MethodPost, "/v1/orgs/acme/tokens", map[string]any{"name": "ci", "role": "admin"}, http.StatusCreated)
	tc := env.newClient()
	tc.token = str(tok, "token")
	if status, _ := tc.do(http.MethodPatch, "/v1/me", map[string]string{"locale": "en"}); status != http.StatusForbidden {
		t.Errorf("token cambiando locale: %d", status)
	}
}

// TestAgentChartDownload: el chart del agente se sirve empaquetado, sin auth,
// cacheable, y Helm lo carga.
func TestAgentChartDownload(t *testing.T) {
	env := newTestEnv(t)
	resp, err := http.Get(env.ts.URL + AgentChartPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/gzip" || resp.Header.Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("chart: %d %v", resp.StatusCode, resp.Header)
	}
	ch, err := loader.LoadArchive(bytes.NewReader(body))
	if err != nil || ch.Name() != "rampa-agent" {
		t.Fatalf("LoadArchive: %v %v", err, ch)
	}
}

// bootstrapAttempt manda un canje inválido con un token distinto en cada
// llamada (así el límite por token no interviene) y los headers de reenvío
// dados. Devuelve el status.
func bootstrapAttempt(t *testing.T, env *testEnv, n int, headers map[string]string) int {
	t.Helper()
	body := fmt.Sprintf(`{"bootstrap_token":"rampa_bootstrap_%064x"}`, n)
	req, err := http.NewRequest(http.MethodPost, env.ts.URL+"/v1/agent/bootstrap", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestBootstrapLimiterIgnoresSpoofedForwardedFor: sin proxies de confianza, un
// X-Forwarded-For distinto en cada intento no evade el límite por IP.
func TestBootstrapLimiterIgnoresSpoofedForwardedFor(t *testing.T) {
	env := newTestEnv(t)
	limited := false
	for i := 0; i < 30 && !limited; i++ {
		ip := "198.51.100." + strconv.Itoa(i+1)
		limited = bootstrapAttempt(t, env, i, map[string]string{"X-Forwarded-For": ip, "X-Real-IP": ip}) == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("un X-Forwarded-For variable no debería evadir el límite por IP")
	}
}

// TestBootstrapLimiterTrustedProxy: desde un proxy de confianza se usa la IP
// de X-Forwarded-For: una IP se limita y otra sigue pasando.
func TestBootstrapLimiterTrustedProxy(t *testing.T) {
	env := newTestEnvWith(t, map[string]string{"RAMPA_TRUSTED_PROXIES": "127.0.0.0/8,::1/128"})
	n := 0
	limited := false
	for i := 0; i < 15 && !limited; i++ {
		n++
		limited = bootstrapAttempt(t, env, n, map[string]string{"X-Forwarded-For": "198.51.100.1"}) == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("la IP del header debería limitarse")
	}
	n++
	if got := bootstrapAttempt(t, env, n, map[string]string{"X-Forwarded-For": "198.51.100.2"}); got != http.StatusUnauthorized {
		t.Errorf("otra IP del cliente no debería estar limitada: %d", got)
	}
}

// TestRotateTokenInvalidatesPendingBootstrap: un bootstrap token emitido antes
// de rotar el token del cluster deja de canjearse.
func TestRotateTokenInvalidatesPendingBootstrap(t *testing.T) {
	env := newTestEnv(t)
	fx := newOrgFixture(t, env, "acme", "ana@acme.com")
	c := fx.owner
	bt := c.must(http.MethodPost, "/v1/orgs/acme/clusters/"+fx.clusterID+"/bootstrap-token", nil, http.StatusCreated)
	bootstrap := str(bt, "bootstrap_token")

	c.must(http.MethodPost, "/v1/orgs/acme/clusters/"+fx.clusterID+"/rotate-token", nil, http.StatusOK)

	var pending int
	if err := env.srv.Store().Pool().QueryRow(context.Background(), `SELECT count(*) FROM cluster_bootstrap_tokens WHERE cluster_id = $1 AND used_at IS NULL`, fx.clusterID).Scan(&pending); err != nil || pending != 0 {
		t.Errorf("bootstrap pendientes tras rotar: %d %v", pending, err)
	}
	status, out := env.newClient().do(http.MethodPost, "/v1/agent/bootstrap", map[string]string{"bootstrap_token": bootstrap})
	if status != http.StatusUnauthorized {
		t.Fatalf("canje tras rotar: %d %v", status, out)
	}
}

// TestPurgeBootstrapTokens: la purga borra los usados o vencidos hace más de
// keep y conserva los pendientes y los recientes; el purger la corre solo.
func TestPurgeBootstrapTokens(t *testing.T) {
	env := newTestEnv(t)
	fx := newOrgFixture(t, env, "acme", "ana@acme.com")
	ctx := context.Background()
	st := env.srv.Store()
	add := func(expires time.Time) string {
		_, hash, _ := auth.NewBootstrapToken()
		if _, err := st.CreateBootstrapToken(ctx, fx.orgID, fx.clusterID, hash, expires, ""); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	setUsed := func(hash string, ago time.Duration) {
		if _, err := st.Pool().Exec(ctx, `UPDATE cluster_bootstrap_tokens SET used_at = now() - make_interval(secs => $2) WHERE token_hash = $1`, hash, ago.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
	usedOld := add(time.Now().Add(time.Hour))
	setUsed(usedOld, 48*time.Hour)
	expiredOld := add(time.Now().Add(-48 * time.Hour))
	usedRecent := add(time.Now().Add(time.Hour))
	setUsed(usedRecent, time.Hour)
	pending := add(time.Now().Add(time.Hour))
	expiredRecent := add(time.Now().Add(-time.Hour))

	n, err := st.PurgeBootstrapTokens(ctx, 24*time.Hour)
	if err != nil || n != 2 {
		t.Fatalf("purga: %d %v", n, err)
	}
	for hash, want := range map[string]bool{usedOld: false, expiredOld: false, usedRecent: true, pending: true, expiredRecent: true} {
		_, err := st.GetBootstrapTokenByHash(ctx, hash)
		if (err == nil) != want {
			t.Errorf("token %s: presente=%v, esperaba %v", hash[:8], err == nil, want)
		}
	}

	// El purger la corre al arrancar sin esperar al ticker.
	old := add(time.Now().Add(-72 * time.Hour))
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { env.srv.runBootstrapPurger(pctx, time.Hour, 24*time.Hour); close(done) }()
	waitUntil(t, "purger", func() bool {
		_, err := st.GetBootstrapTokenByHash(ctx, old)
		return errors.Is(err, store.ErrNotFound)
	})
	cancel()
	<-done
}

// TestScopeSecretsFailedSendKeepsKeysHidden: si Send falla tras el chequeo de
// online (la conexión se cayó en el medio), la respuesta es 409 y GET no
// muestra claves que nunca se escribieron.
func TestScopeSecretsFailedSendKeepsKeysHidden(t *testing.T) {
	env := newTestEnv(t)
	fx := newOrgFixture(t, env, "acme", "ana@acme.com")
	c := fx.owner
	const secretsPath = "/v1/orgs/acme/apps/checkout-api/scopes/dev/secrets"
	agent := dialFakeAgent(t, env, fx.clusterToken, nil)
	defer agent.close()
	waitUntil(t, "online", func() bool { return env.srv.Hub().Online(fx.clusterID) })

	env.srv.API().SetCommandSender(func(string, protocol.Command) error { return hub.ErrOffline })
	defer env.srv.API().SetCommandSender(nil)
	status, out := c.do(http.MethodPut, secretsPath, map[string]any{"values": map[string]string{"API_KEY": "sk_live_no_entregado"}})
	if status != http.StatusConflict {
		t.Fatalf("envío fallido: %d %v", status, out)
	}
	if got := c.must(http.MethodGet, secretsPath, nil, http.StatusOK); len(got["keys"].([]any)) != 0 {
		t.Errorf("GET muestra claves que nunca se escribieron: %v", got)
	}
	var failed int
	if err := env.srv.Store().Pool().QueryRow(context.Background(), `SELECT count(*) FROM agent_commands WHERE kind = 'write_secret' AND status = $1`, store.CommandFailed).Scan(&failed); err != nil || failed != 1 {
		t.Errorf("el comando no entregado debería quedar failed: %d %v", failed, err)
	}

	// Con la entrega andando, las claves sí aparecen.
	env.srv.API().SetCommandSender(nil)
	c.must(http.MethodPut, secretsPath, map[string]any{"values": map[string]string{"API_KEY": "sk_live_entregado"}}, http.StatusAccepted)
	if got := c.must(http.MethodGet, secretsPath, nil, http.StatusOK); len(got["keys"].([]any)) != 1 {
		t.Errorf("GET tras entregar: %v", got)
	}
}

// TestUpdateCloudAccountValidatesBeforeWriting: sin RAMPA_STATE_KEY, un PATCH
// con credenciales responde 501 y no escribe settings ni external_ref; y con un
// provider desconocido se rechaza sin guardar secretos en settings.
func TestUpdateCloudAccountValidatesBeforeWriting(t *testing.T) {
	env := newTestEnv(t)
	provider.Register(digitalocean.New(digitalocean.Config{}))
	fx := newOrgFixture(t, env, "acme", "ana@acme.com")
	c := fx.owner
	ctx := context.Background()
	st := env.srv.Store()

	do, err := st.CreateCloudAccount(ctx, &store.CloudAccount{OrgID: fx.orgID, Provider: "digitalocean", Name: "acme-do", Region: "nyc3", ExternalRef: "ref-original", Settings: map[string]string{"registry_name": "original"}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/orgs/acme/cloud-accounts/" + do.ID
	status, out := c.do(http.MethodPatch, path, map[string]any{
		"external_ref": "ref-nuevo", "settings": map[string]string{"registry_name": "nuevo"}, "credentials": map[string]string{"api_token": "dop_v1_algo_secreto"},
	})
	if status != http.StatusNotImplemented {
		t.Fatalf("PATCH sin clave: %d %v", status, out)
	}
	got, err := st.GetCloudAccount(ctx, fx.orgID, do.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExternalRef != "ref-original" || got.Settings["registry_name"] != "original" || got.HasCredentials {
		t.Errorf("el 501 no debería haber escrito nada: %+v", got)
	}

	// Provider que esta versión no conoce: 501 y nada guardado.
	ghost, err := st.CreateCloudAccount(ctx, &store.CloudAccount{OrgID: fx.orgID, Provider: "oci", Name: "acme-oci", Region: "x", Settings: map[string]string{"a": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	status, _ = c.do(http.MethodPatch, "/v1/orgs/acme/cloud-accounts/"+ghost.ID, map[string]any{"settings": map[string]string{"api_token": "en-claro-no"}})
	if status != http.StatusNotImplemented {
		t.Fatalf("provider desconocido: %d", status)
	}
	got, err = st.GetCloudAccount(ctx, fx.orgID, ghost.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := got.Settings["api_token"]; has {
		t.Errorf("un secreto quedó en settings en claro: %v", got.Settings)
	}
}
