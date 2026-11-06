package server

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/provider"
)

// docsSpec es el ApplicationSpec de docs/api.md, como lo manda la web.
func docsSpec() map[string]any {
	return map[string]any{
		"name": "checkout-api", "team": "payments", "tier": 2, "description": "API de checkout",
		"type": "api", "runtime": "go", "port": 8080, "healthcheck": "/health", "schedule": "",
		"scopes": []map[string]any{
			{"name": "dev", "compute": "kubernetes", "size": "S", "replicas": 1, "strategy": "rolling", "domain": "", "autoscaling": nil, "ttl": ""},
			{"name": "prod", "compute": "kubernetes", "size": "M", "replicas": 3, "strategy": "canary", "domain": "checkout.acme.com", "autoscaling": map[string]int{"min": 3, "max": 10, "cpu": 70}, "ttl": ""},
			{"name": "pr", "compute": "kubernetes", "size": "S", "replicas": 1, "strategy": "rolling", "domain": "", "autoscaling": nil, "ttl": "72h"},
		},
		"parameters": []map[string]any{
			{"name": "LOG_LEVEL", "source": "value", "value": "info", "scopes": []string{}},
			{"name": "LOG_LEVEL", "source": "value", "value": "debug", "scopes": []string{"dev", "pr"}},
			{"name": "API_KEY", "source": "secret", "scopes": []string{}},
			{"name": "DB_URL", "source": "service", "service": "checkout-db", "key": "url", "scopes": []string{}},
		},
		"services": []map[string]any{{"name": "checkout-db", "type": "postgres", "size": "S", "scopes": []string{"prod"}}},
	}
}

func TestSpecEndpoints(t *testing.T) {
	env := newTestEnv(t)
	c := env.newClient()
	c.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "spec@acme.com"}, http.StatusOK)
	c.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "spec", "name": "Spec"}, http.StatusCreated)

	// Preview con errores: 200, valid false y details.
	bad := docsSpec()
	bad["type"] = "lambda"
	bad["parameters"] = []map[string]any{{"name": "X", "source": "magic"}}
	prev := c.must(http.MethodPost, "/v1/orgs/spec/apps/preview", map[string]any{"spec": bad}, http.StatusOK)
	details, _ := prev["details"].([]any)
	if prev["valid"] != false || len(details) < 2 || !strings.Contains(str(prev, "manifest"), "type: lambda") {
		t.Fatalf("preview inválido: %v", prev)
	}
	joined := ""
	for _, d := range details {
		joined += d.(string) + "\n"
	}
	if !strings.Contains(joined, `source "magic"`) || !strings.Contains(joined, "spec.type") {
		t.Errorf("details: %s", joined)
	}
	// Preview válido.
	prev = c.must(http.MethodPost, "/v1/orgs/spec/apps/preview", map[string]any{"spec": docsSpec()}, http.StatusOK)
	if prev["valid"] != true || len(prev["details"].([]any)) != 0 || !strings.HasPrefix(str(prev, "manifest"), manifest.ManifestHeader) {
		t.Fatalf("preview válido: %v", prev)
	}
	if status, out := c.do(http.MethodPost, "/v1/orgs/spec/apps/preview", map[string]any{}); status != http.StatusBadRequest {
		t.Errorf("sin spec: %d %v", status, out)
	}
	// Nada se guardó.
	if apps := c.must(http.MethodGet, "/v1/orgs/spec/apps", nil, http.StatusOK); len(itemsOf(apps)) != 0 {
		t.Errorf("preview no debería guardar: %v", apps)
	}

	// from-spec: inválido 400 con details; válido 201.
	if status, out := c.do(http.MethodPost, "/v1/orgs/spec/apps/from-spec", map[string]any{"spec": bad}); status != http.StatusBadRequest || errCode(out) != "invalid" {
		t.Errorf("from-spec inválido: %d %v", status, out)
	}
	if status, out := c.do(http.MethodPost, "/v1/orgs/spec/apps/from-spec", map[string]any{"spec": docsSpec(), "repo_url": "git@github.com:acme/x.git"}); status != http.StatusBadRequest {
		t.Errorf("repo_url inválida: %d %v", status, out)
	}
	app := c.must(http.MethodPost, "/v1/orgs/spec/apps/from-spec", map[string]any{"spec": docsSpec(), "repo_url": "https://github.com/acme/checkout-api"}, http.StatusCreated)
	if app["name"] != "checkout-api" || app["team"] != "payments" || app["type"] != "api" || app["runtime"] != "go" || app["repo_url"] != "https://github.com/acme/checkout-api" {
		t.Fatalf("app: %v", app)
	}
	if got := app["scopes_declared"]; !reflect.DeepEqual(got, []any{"dev", "pr", "prod"}) {
		t.Errorf("scopes_declared: %v", got)
	}
	if !strings.HasPrefix(str(app, "manifest"), manifest.ManifestHeader) || str(app, "manifest_sha") == "" {
		t.Errorf("manifest guardado: %v", app["manifest"])
	}
	if status, out := c.do(http.MethodPost, "/v1/orgs/spec/apps/from-spec", map[string]any{"spec": docsSpec()}); status != http.StatusConflict {
		t.Errorf("from-spec repetido: %d %v", status, out)
	}

	// GET spec: igual al que mandamos.
	got := c.must(http.MethodGet, "/v1/orgs/spec/apps/checkout-api/spec", nil, http.StatusOK)
	if str(got, "manifest") != str(app, "manifest") {
		t.Errorf("manifest del GET spec distinto del guardado")
	}
	var gotSpec manifest.ApplicationSpec
	b, _ := json.Marshal(got["spec"])
	if err := json.Unmarshal(b, &gotSpec); err != nil {
		t.Fatal(err)
	}
	var wantSpec manifest.ApplicationSpec
	b, _ = json.Marshal(docsSpec())
	_ = json.Unmarshal(b, &wantSpec)
	if !reflect.DeepEqual(gotSpec, wantSpec) {
		t.Errorf("spec:\n%+v\nquería:\n%+v", gotSpec, wantSpec)
	}

	// PUT spec: el nombre no cambia; tier sí.
	renamed := docsSpec()
	renamed["name"] = "otra"
	if status, out := c.do(http.MethodPut, "/v1/orgs/spec/apps/checkout-api/spec", map[string]any{"spec": renamed}); status != http.StatusBadRequest {
		t.Errorf("PUT spec con otro nombre: %d %v", status, out)
	}
	changed := docsSpec()
	changed["tier"] = 1
	changed["description"] = "cambiada"
	updated := c.must(http.MethodPut, "/v1/orgs/spec/apps/checkout-api/spec", map[string]any{"spec": changed}, http.StatusOK)
	if updated["tier"] != float64(1) || updated["description"] != "cambiada" || str(updated, "manifest_sha") == str(app, "manifest_sha") {
		t.Errorf("PUT spec: %v", updated)
	}
	if status, out := c.do(http.MethodPut, "/v1/orgs/spec/apps/checkout-api/spec", map[string]any{"spec": bad}); status != http.StatusBadRequest || errCode(out) != "invalid" {
		t.Errorf("PUT spec inválido: %d %v", status, out)
	}
	// Un manifest pegado a mano también se puede leer como spec.
	c.must(http.MethodPut, "/v1/orgs/spec/apps/checkout-api/manifest", map[string]string{"manifest": exampleManifest(t)}, http.StatusOK)
	got = c.must(http.MethodGet, "/v1/orgs/spec/apps/checkout-api/spec", nil, http.StatusOK)
	sp, _ := got["spec"].(map[string]any)
	scopes, _ := sp["scopes"].([]any)
	if len(scopes) != 4 || scopes[0].(map[string]any)["name"] != "dev" || scopes[3].(map[string]any)["name"] != "pr" {
		t.Errorf("spec derivado del manifest pegado: %v", sp["scopes"])
	}
	if str(got, "manifest") != exampleManifest(t) {
		t.Error("el manifest pegado se devuelve tal cual")
	}
	// Los viewers no pueden previsualizar ni crear.
	tok := c.must(http.MethodPost, "/v1/orgs/spec/tokens", map[string]any{"name": "ro", "role": "viewer"}, http.StatusCreated)
	viewer := env.newClient()
	viewer.token = str(tok, "token")
	if status, _ := viewer.do(http.MethodPost, "/v1/orgs/spec/apps/preview", map[string]any{"spec": docsSpec()}); status != http.StatusForbidden {
		t.Errorf("viewer preview: %d", status)
	}
	viewer.must(http.MethodGet, "/v1/orgs/spec/apps/checkout-api/spec", nil, http.StatusOK)
}

func TestScaffoldZip(t *testing.T) {
	env := newTestEnv(t)
	c := env.newClient()
	c.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "zip@acme.com"}, http.StatusOK)
	c.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "zip", "name": "Zip"}, http.StatusCreated)
	c.must(http.MethodPost, "/v1/orgs/zip/apps", map[string]string{"manifest": exampleManifest(t)}, http.StatusCreated)

	fetch := func(query string) (map[string][]byte, *http.Response) {
		t.Helper()
		resp, err := c.http.Get(env.ts.URL + "/v1/orgs/zip/apps/checkout-api/scaffold.zip" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("scaffold.zip: %d %s", resp.StatusCode, body)
		}
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		for _, f := range zr.File {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			files[f.Name] = b
		}
		return files, resp
	}

	files, resp := fetch("")
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" || !strings.Contains(resp.Header.Get("Content-Disposition"), `checkout-api.zip`) {
		t.Errorf("headers: %s %s", ct, resp.Header.Get("Content-Disposition"))
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	want := []string{
		"checkout-api/.dockerignore", "checkout-api/.github/workflows/ci.yml", "checkout-api/.github/workflows/pr-cleanup.yml", "checkout-api/.gitignore",
		"checkout-api/Dockerfile", "checkout-api/README.md", "checkout-api/go.mod", "checkout-api/main.go", "checkout-api/main_test.go", "checkout-api/rampa.yaml",
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("archivos del zip:\n%v\nquería:\n%v", names, want)
	}
	// El rampa.yaml es el real de la app (mismo spec que el manifest importado).
	got, err := manifest.Parse(files["checkout-api/rampa.yaml"])
	if err != nil {
		t.Fatalf("rampa.yaml del zip: %v\n%s", err, files["checkout-api/rampa.yaml"])
	}
	orig, _ := manifest.Parse([]byte(exampleManifest(t)))
	if !reflect.DeepEqual(manifest.ToSpec(got), manifest.ToSpec(orig)) {
		t.Errorf("rampa.yaml del zip distinto del manifest:\n%s", files["checkout-api/rampa.yaml"])
	}
	if !bytes.HasPrefix(files["checkout-api/rampa.yaml"], []byte(manifest.ManifestHeader)) {
		t.Error("el rampa.yaml del zip lleva la cabecera generada")
	}
	// Sin github_org ni settings: CHANGEME.
	for _, wf := range []string{"checkout-api/.github/workflows/ci.yml", "checkout-api/.github/workflows/pr-cleanup.yml"} {
		if !bytes.Contains(files[wf], []byte("CHANGEME/rampa/.github/workflows/")) || bytes.Contains(files[wf], []byte("[[")) {
			t.Errorf("%s:\n%s", wf, files[wf])
		}
	}
	if !bytes.Contains(files["checkout-api/.github/workflows/ci.yml"], []byte("app: checkout-api")) {
		t.Error("ci.yml debería tener el nombre de la app")
	}
	// settings.github_org de la org.
	c.must(http.MethodPatch, "/v1/orgs/zip", map[string]any{"settings": map[string]any{"require_approval_prod": true, "github_org": "acme-gh"}}, http.StatusOK)
	org := c.must(http.MethodGet, "/v1/orgs/zip", nil, http.StatusOK)
	if org["settings"].(map[string]any)["github_org"] != "acme-gh" {
		t.Errorf("settings: %v", org["settings"])
	}
	files, _ = fetch("")
	if !bytes.Contains(files["checkout-api/.github/workflows/ci.yml"], []byte("acme-gh/rampa/.github/workflows/reusable-build.yml@main")) {
		t.Errorf("ci.yml con settings.github_org:\n%s", files["checkout-api/.github/workflows/ci.yml"])
	}
	// La query gana.
	files, _ = fetch("?github_org=otra-org")
	if !bytes.Contains(files["checkout-api/.github/workflows/pr-cleanup.yml"], []byte("otra-org/rampa/.github/workflows/reusable-undeploy.yml@main")) {
		t.Errorf("pr-cleanup.yml con query:\n%s", files["checkout-api/.github/workflows/pr-cleanup.yml"])
	}
	// Sin golden path para la combinación: invalid con las disponibles.
	worker := map[string]any{"name": "cola", "team": "payments", "type": "worker", "runtime": "node", "scopes": []map[string]any{{"name": "dev"}}}
	c.must(http.MethodPost, "/v1/orgs/zip/apps/from-spec", map[string]any{"spec": worker}, http.StatusCreated)
	status, out := c.do(http.MethodGet, "/v1/orgs/zip/apps/cola/scaffold.zip", nil)
	if status != http.StatusBadRequest || errCode(out) != "invalid" || !strings.Contains(out["error"].(map[string]any)["message"].(string), "api-go") {
		t.Errorf("sin golden path: %d %v", status, out)
	}
}

func TestOnboarding(t *testing.T) {
	env := newTestEnv(t)
	c := env.newClient()
	c.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "ana@acme.com"}, http.StatusOK)
	c.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "acme", "name": "Acme"}, http.StatusCreated)

	steps := func() (map[string]map[string]any, map[string]any) {
		t.Helper()
		out := c.must(http.MethodGet, "/v1/orgs/acme/onboarding", nil, http.StatusOK)
		by := map[string]map[string]any{}
		raw, _ := out["steps"].([]any)
		if len(raw) != 5 {
			t.Fatalf("esperaba 5 pasos: %v", out)
		}
		for i, key := range []string{"cloud", "cluster", "agent", "app", "deploy"} {
			s := raw[i].(map[string]any)
			if str(s, "key") != key || str(s, "title") == "" {
				t.Fatalf("paso %d: %v", i, s)
			}
			by[key] = s
		}
		return by, out
	}

	by, out := steps()
	if by["cloud"]["done"] != false || out["next"] != "cloud" || out["complete"] != false || str(by["cloud"], "detail") != "" {
		t.Fatalf("estado inicial: %v", out)
	}

	acc := c.must(http.MethodPost, "/v1/orgs/acme/cloud-accounts", map[string]string{"provider": "aws", "name": "acme-nonprod", "region": "us-east-1"}, http.StatusCreated)
	by, out = steps()
	if by["cloud"]["done"] != false || str(by["cloud"], "detail") != "acme-nonprod (aws, pending)" || out["next"] != "cloud" {
		t.Fatalf("con cuenta pending: %v", out)
	}
	c.must(http.MethodPatch, "/v1/orgs/acme/cloud-accounts/"+str(acc, "id"), map[string]any{"external_ref": "arn:aws:iam::123456789012:role/RampaConnect"}, http.StatusOK)
	c.must(http.MethodPost, "/v1/orgs/acme/cloud-accounts/"+str(acc, "id")+"/verify", nil, http.StatusOK)
	by, out = steps()
	if by["cloud"]["done"] != true || str(by["cloud"], "detail") != "acme-nonprod (aws, connected)" || out["next"] != "cluster" {
		t.Fatalf("con cuenta connected: %v", out)
	}

	cl := c.must(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "eks-nonprod", "env": "nonprod", "cloud_account_id": str(acc, "id"), "domain": "apps.dev.acme.com"}, http.StatusCreated)
	by, out = steps()
	if by["cluster"]["done"] != true || str(by["cluster"], "detail") != "eks-nonprod" || by["agent"]["done"] != false || str(by["agent"], "detail") != "eks-nonprod está offline" || out["next"] != "agent" {
		t.Fatalf("con cluster offline: %v", out)
	}

	agent := dialFakeAgent(t, env, str(cl, "token"), nil)
	waitUntil(t, "cluster online", func() bool {
		got := c.must(http.MethodGet, "/v1/orgs/acme/clusters/"+str(cl, "id"), nil, http.StatusOK)
		return got["status"] == "online"
	})
	by, out = steps()
	if by["agent"]["done"] != true || str(by["agent"], "detail") != "eks-nonprod está online" || out["next"] != "app" {
		t.Fatalf("con agente online: %v", out)
	}
	agent.close()

	c.must(http.MethodPost, "/v1/orgs/acme/apps", map[string]string{"manifest": exampleManifest(t)}, http.StatusCreated)
	waitUntil(t, "cluster offline", func() bool {
		got := c.must(http.MethodGet, "/v1/orgs/acme/clusters/"+str(cl, "id"), nil, http.StatusOK)
		return got["status"] == "offline"
	})
	by, out = steps()
	if by["app"]["done"] != true || str(by["app"], "detail") != "checkout-api" || by["deploy"]["done"] != false || out["next"] != "agent" {
		// El agente se desconectó: vuelve a ser el paso pendiente aunque app esté hecho.
		t.Fatalf("con app y agente offline: %v", out)
	}

	c.must(http.MethodPut, "/v1/orgs/acme/apps/checkout-api/scopes/dev", map[string]string{"cluster_id": str(cl, "id")}, http.StatusOK)
	dep := c.must(http.MethodPost, "/v1/orgs/acme/apps/checkout-api/scopes/dev/deployments", map[string]string{"image": "sha-abc1234"}, http.StatusCreated)
	runFakeAgent(t, env, str(cl, "token"), str(dep, "id"))
	by, out = steps()
	if by["deploy"]["done"] != true || str(by["deploy"], "detail") != "checkout-api/dev healthy" {
		t.Fatalf("con deploy healthy: %v", out)
	}
	// El agente ya se desconectó (runFakeAgent cierra), así que agent vuelve a pendiente.
	if out["complete"] != false || out["next"] != "agent" {
		t.Errorf("con agente offline no está completo: %v", out)
	}
	agent = dialFakeAgent(t, env, str(cl, "token"), nil)
	waitUntil(t, "cluster online otra vez", func() bool {
		_, out := steps()
		return out["complete"] == true
	})
	_, out = steps()
	if out["next"] != "" {
		t.Errorf("completo: %v", out)
	}
	agent.close()
}

func TestInfraNotConfigured(t *testing.T) {
	env := newTestEnv(t)
	c := env.newClient()
	c.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "i@acme.com"}, http.StatusOK)
	c.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "noinfra", "name": "x"}, http.StatusCreated)

	// Sin RAMPA_STATE_KEY: 501 con qué falta. GET /infra/modules responde igual.
	status, out := c.do(http.MethodGet, "/v1/orgs/noinfra/infra/runs", nil)
	msg, _ := out["error"].(map[string]any)["message"].(string)
	if status != http.StatusNotImplemented || errCode(out) != "not_implemented" || !strings.Contains(msg, "RAMPA_STATE_KEY") {
		t.Fatalf("sin clave: %d %v", status, out)
	}
	if status, _ := c.do(http.MethodPost, "/v1/orgs/noinfra/infra/clusters", map[string]string{"name": "x"}); status != http.StatusNotImplemented {
		t.Errorf("POST /infra/clusters sin runner: %d", status)
	}
	mods := c.must(http.MethodGet, "/v1/orgs/noinfra/infra/modules", nil, http.StatusOK)
	items := itemsOf(mods)
	if mods["runner_ready"] != false || len(items) != 9 || items[0]["module"] != "aws/eks-cluster" || items[0]["available"] != true || items[3]["available"] != true || items[4]["available"] != false {
		t.Errorf("modules: %v", mods)
	}
	if items[3]["module"] != "digitalocean/droplet-k3s" || items[3]["kind"] != "vm" || items[3]["secret_store"] != "kubernetes" {
		t.Errorf("módulo de droplet: %v", items[3])
	}
	if inputs, _ := items[0]["inputs"].([]any); len(inputs) == 0 || inputs[0].(map[string]any)["name"] != "kubernetes_version" {
		t.Errorf("inputs del módulo: %v", items[0]["inputs"])
	}

	// Sin tofu: el mensaje lo dice.
	env2 := newTestEnvWith(t, map[string]string{"RAMPA_STATE_KEY": testStateKey(), "RAMPA_TOFU_BIN": "/no/existe/tofu"})
	c2 := env2.newClient()
	c2.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "i@acme.com"}, http.StatusOK)
	c2.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "notofu", "name": "x"}, http.StatusCreated)
	status, out = c2.do(http.MethodGet, "/v1/orgs/notofu/infra/runs", nil)
	msg, _ = out["error"].(map[string]any)["message"].(string)
	if status != http.StatusNotImplemented || !strings.Contains(msg, "RAMPA_TOFU_BIN") || strings.Contains(msg, "RAMPA_STATE_KEY") {
		t.Errorf("sin tofu: %d %v", status, out)
	}
	mods = c2.must(http.MethodGet, "/v1/orgs/notofu/infra/modules", nil, http.StatusOK)
	if mods["runner_ready"] != false || !strings.Contains(str(mods, "runner_error"), "RAMPA_TOFU_BIN") {
		t.Errorf("modules sin tofu: %v", mods)
	}
}

func testStateKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
}

func fakeTofuPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "infra", "testdata", "fake-tofu.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestInfraClusterFlow: crear un cluster con OpenTofu (falso) de punta a punta
// por HTTP: validaciones, run en 202, log en vivo por SSE, outputs, cluster con
// agent_role_arn, install_command con IRSA, retry, destroy y aislamiento.
func TestInfraClusterFlow(t *testing.T) {
	env := newTestEnvWith(t, map[string]string{
		"RAMPA_STATE_KEY": testStateKey(), "RAMPA_TOFU_BIN": fakeTofuPath(t), "RAMPA_INFRA_WORKDIR": t.TempDir(),
		"RAMPA_AGENT_IMAGE": "ghcr.io/acme/rampa-agent:2.0",
	})
	runner := env.srv.Infra()
	runner.SetStateBaseURL(env.ts.URL)
	runner.SetCredentialsFunc(func(context.Context, provider.CloudAccount) (provider.Credentials, error) {
		return provider.Credentials{AccessKeyID: "AKIAFAKEHTTP", SecretAccessKey: "secretHTTP", SessionToken: "sessionHTTP", Expires: time.Now().Add(time.Hour)}, nil
	})
	wctx, wcancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() { runner.Run(wctx); close(workerDone) }()
	t.Cleanup(func() { wcancel(); <-workerDone })

	c := env.newClient()
	c.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "ops@acme.com", "name": "Ops"}, http.StatusOK)
	c.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "acme", "name": "Acme"}, http.StatusCreated)
	acc := c.must(http.MethodPost, "/v1/orgs/acme/cloud-accounts", map[string]string{"provider": "aws", "name": "acme-aws", "region": "us-east-1"}, http.StatusCreated)
	accID := str(acc, "id")

	// Cuenta sin conectar: invalid.
	body := map[string]any{"cloud_account_id": accID, "name": "rampa-nonprod", "env": "nonprod", "domain": "apps.dev.acme.com", "kubernetes_version": "1.33", "admin_principal_arns": []string{"arn:aws:iam::123456789012:user/juan"}}
	if status, out := c.do(http.MethodPost, "/v1/orgs/acme/infra/clusters", body); status != http.StatusBadRequest || !strings.Contains(out["error"].(map[string]any)["message"].(string), "no está conectada") {
		t.Fatalf("cuenta pending: %d %v", status, out)
	}
	c.must(http.MethodPatch, "/v1/orgs/acme/cloud-accounts/"+accID, map[string]any{"external_ref": "arn:aws:iam::123456789012:role/RampaConnect"}, http.StatusOK)
	c.must(http.MethodPost, "/v1/orgs/acme/cloud-accounts/"+accID+"/verify", nil, http.StatusOK)

	// Validaciones del formulario.
	for name, mutate := range map[string]func(m map[string]any){
		"nombre":                  func(m map[string]any) { m["name"] = "1abc" },
		"env":                     func(m map[string]any) { m["env"] = "qa" },
		"version":                 func(m map[string]any) { m["kubernetes_version"] = "1.20" },
		"vpc":                     func(m map[string]any) { m["vpc_cidr"] = "abc" },
		"vpc /8":                  func(m map[string]any) { m["vpc_cidr"] = "10.0.0.0/8" },
		"arn":                     func(m map[string]any) { m["admin_principal_arns"] = []string{"juan"} },
		"dominio":                 func(m map[string]any) { m["domain"] = "" },
		"cuenta":                  func(m map[string]any) { m["cloud_account_id"] = "" },
		"sin body":                func(m map[string]any) { clear(m) },
		"módulo de otro provider": func(m map[string]any) { m["module"] = "digitalocean/droplet-k3s" },
		"módulo desconocido":      func(m map[string]any) { m["module"] = "aws/nada" },
		"input desconocido":       func(m map[string]any) { m["inputs"] = map[string]any{"size": "s-2vcpu-2gb"} },
		"input mal tipado":        func(m map[string]any) { m["inputs"] = map[string]any{"vpc_cidr": 10} },
		"input fuera de opciones": func(m map[string]any) { m["inputs"] = map[string]any{"kubernetes_version": "1.20"} },
	} {
		m := map[string]any{}
		for k, v := range body {
			m[k] = v
		}
		mutate(m)
		if status, out := c.do(http.MethodPost, "/v1/orgs/acme/infra/clusters", m); status != http.StatusBadRequest {
			t.Errorf("%s: esperaba 400, tengo %d %v", name, status, out)
		}
	}
	// Un developer no puede.
	tok := c.must(http.MethodPost, "/v1/orgs/acme/tokens", map[string]any{"name": "dev", "role": "developer"}, http.StatusCreated)
	dev := env.newClient()
	dev.token = str(tok, "token")
	if status, _ := dev.do(http.MethodPost, "/v1/orgs/acme/infra/clusters", body); status != http.StatusForbidden {
		t.Errorf("developer: %d", status)
	}

	body["vpc_cidr"] = "10.50.0.0/16"
	run := c.must(http.MethodPost, "/v1/orgs/acme/infra/clusters", body, http.StatusAccepted)
	runID := str(run, "id")
	clusterID := str(run, "cluster_id")
	if run["status"] != "queued" || run["kind"] != "cluster" || run["provider"] != "aws" || run["module"] != "aws/eks-cluster" || run["name"] != "rampa-nonprod" || clusterID == "" || run["cloud_account_id"] != accID {
		t.Fatalf("run: %v", run)
	}
	if rb, _ := run["requested_by"].(map[string]any); rb == nil || rb["email"] != "ops@acme.com" {
		t.Errorf("requested_by: %v", run["requested_by"])
	}
	inputs, _ := run["inputs"].(map[string]any)
	if inputs["api_url"] != "https://api.rampa-test.example.com" || inputs["vpc_cidr"] != "10.50.0.0/16" || inputs["agent_image_tag"] != "2.0" || inputs["kubernetes_version"] != "1.33" || inputs["agent_chart_url"] != "https://api.rampa-test.example.com/public/charts/rampa-agent.tgz" {
		t.Errorf("inputs: %v", inputs)
	}
	if _, has := inputs["agent_token"]; has {
		t.Error("inputs no lleva el token")
	}
	if _, has := inputs["bootstrap_token"]; has {
		t.Error("inputs no lleva el bootstrap token")
	}
	if status, _ := c.do(http.MethodPost, "/v1/orgs/acme/infra/clusters", body); status != http.StatusConflict {
		t.Errorf("mismo nombre de cluster: %d", status)
	}
	cl := c.must(http.MethodGet, "/v1/orgs/acme/clusters/"+clusterID, nil, http.StatusOK)
	if cl["infra_run_id"] != runID || cl["status"] != "offline" || cl["agent_role_arn"] != "" || cl["domain"] != "apps.dev.acme.com" || cl["kind"] != "kubernetes" || cl["secret_store"] != "aws-secrets-manager" {
		t.Fatalf("cluster recién creado: %v", cl)
	}

	// Termina bien.
	var done map[string]any
	waitUntil(t, "run succeeded", func() bool {
		done = c.must(http.MethodGet, "/v1/orgs/acme/infra/runs/"+runID, nil, http.StatusOK)
		return done["status"] == "succeeded" || done["status"] == "failed"
	})
	if done["status"] != "succeeded" || done["error"] != "" || done["started_at"] == nil || done["finished_at"] == nil {
		t.Fatalf("run terminado: %v", done)
	}
	outputs, _ := done["outputs"].(map[string]any)
	if outputs["agent_role_arn"] != "arn:aws:iam::123456789012:role/rampa-nonprod-rampa-agent" {
		t.Errorf("outputs: %v", outputs)
	}
	tail, _ := done["log_tail"].([]any)
	if len(tail) == 0 || tail[len(tail)-1] != "== rampa: apply completo ==" {
		t.Errorf("log_tail: %v", tail)
	}
	cl = c.must(http.MethodGet, "/v1/orgs/acme/clusters/"+clusterID, nil, http.StatusOK)
	if cl["agent_role_arn"] != "arn:aws:iam::123456789012:role/rampa-nonprod-rampa-agent" || cl["status"] != "offline" {
		t.Errorf("cluster tras el apply: %v", cl)
	}
	rotated := c.must(http.MethodPost, "/v1/orgs/acme/clusters/"+clusterID+"/rotate-token", nil, http.StatusOK)
	if !strings.Contains(str(rotated, "install_command"), `--set-string 'serviceAccount.annotations.eks\.amazonaws\.com/role-arn=arn:aws:iam::123456789012:role/rampa-nonprod-rampa-agent'`) {
		t.Errorf("install_command con IRSA: %s", str(rotated, "install_command"))
	}
	// POST /clusters también acepta agent_role_arn a mano.
	manual := c.must(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "manual", "env": "prod", "cloud_account_id": accID, "domain": "apps.acme.com", "agent_role_arn": "arn:aws:iam::123456789012:role/manual-agent"}, http.StatusCreated)
	if manual["agent_role_arn"] != "arn:aws:iam::123456789012:role/manual-agent" || !strings.Contains(str(manual, "install_command"), "role-arn=arn:aws:iam::123456789012:role/manual-agent") {
		t.Errorf("cluster manual: %v", manual)
	}
	if status, _ := c.do(http.MethodPost, "/v1/orgs/acme/clusters", map[string]string{"name": "manual2", "env": "prod", "cloud_account_id": accID, "domain": "apps.acme.com", "agent_role_arn": "no-arn"}); status != http.StatusBadRequest {
		t.Errorf("agent_role_arn inválido: %d", status)
	}

	list := c.must(http.MethodGet, "/v1/orgs/acme/infra/runs?limit=5", nil, http.StatusOK)
	if items := itemsOf(list); len(items) != 1 || items[0]["id"] != runID {
		t.Errorf("listado: %v", list)
	}
	if status, _ := c.do(http.MethodGet, "/v1/orgs/acme/infra/runs?limit=0", nil); status != http.StatusBadRequest {
		t.Errorf("limit inválido: %d", status)
	}

	// Log en texto plano.
	resp, err := c.http.Get(env.ts.URL + "/v1/orgs/acme/infra/runs/" + runID + "/log")
	if err != nil {
		t.Fatal(err)
	}
	logText, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("log: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{"$ tofu init", "Plan: 12 to add", "Apply complete!", "== rampa: apply completo =="} {
		if !strings.Contains(string(logText), want) {
			t.Errorf("log sin %q:\n%s", want, logText)
		}
	}
	for _, secret := range []string{"AKIAFAKEHTTP", "secretHTTP", "sessionHTTP", "rampa_cluster_", "rampa_state_"} {
		if strings.Contains(string(logText), secret) {
			t.Errorf("el log contiene %q", secret)
		}
	}

	// Log por SSE de un run terminado: líneas, status y done.
	sseCtx, sseCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer sseCancel()
	req, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, env.ts.URL+"/v1/orgs/acme/infra/runs/"+runID+"/log?stream=1", nil)
	resp, err = c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream content-type: %s", resp.Header.Get("Content-Type"))
	}
	var events []string
	sc := bufio.NewScanner(resp.Body)
	current := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			current = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			events = append(events, current+"="+strings.TrimPrefix(line, "data: "))
		}
	}
	if len(events) < 5 || events[len(events)-1] != "done=succeeded" || events[len(events)-2] != "status=succeeded" {
		t.Errorf("SSE: %v", events)
	}
	lineEvents := 0
	for _, e := range events {
		if strings.HasPrefix(e, "line=") {
			lineEvents++
		}
	}
	if lineEvents != strings.Count(strings.TrimSpace(string(logText)), "\n")+1 {
		t.Errorf("el stream debería traer todas las líneas guardadas: %d eventos line vs %d líneas", lineEvents, strings.Count(string(logText), "\n"))
	}

	// El backend de estado existe en el server y exige el token del run.
	if status, out := c.do(http.MethodGet, "/v1/tfstate/"+runID, nil); status != http.StatusUnauthorized {
		t.Errorf("tfstate sin token: %d %v", status, out)
	}

	// Retry de un succeeded: conflict. Otro usuario de otra org: 404.
	if status, out := c.do(http.MethodPost, "/v1/orgs/acme/infra/runs/"+runID+"/retry", nil); status != http.StatusConflict {
		t.Errorf("retry: %d %v", status, out)
	}
	other := env.newClient()
	other.must(http.MethodPost, "/v1/auth/dev-login", map[string]string{"email": "otro@otra.com"}, http.StatusOK)
	other.must(http.MethodPost, "/v1/orgs", map[string]string{"slug": "otra", "name": "Otra"}, http.StatusCreated)
	if status, _ := other.do(http.MethodGet, "/v1/orgs/otra/infra/runs/"+runID, nil); status != http.StatusNotFound {
		t.Errorf("run de otra org: %d", status)
	}
	if status, _ := other.do(http.MethodGet, "/v1/orgs/acme/infra/runs", nil); status != http.StatusNotFound {
		t.Errorf("org ajena: %d", status)
	}

	// Destroy: developer no puede; admin sí. Al terminar, el cluster desaparece.
	if status, _ := dev.do(http.MethodPost, "/v1/orgs/acme/infra/runs/"+runID+"/destroy", nil); status != http.StatusForbidden {
		t.Errorf("developer destroy: %d", status)
	}
	d := c.must(http.MethodPost, "/v1/orgs/acme/infra/runs/"+runID+"/destroy", nil, http.StatusAccepted)
	if d["status"] != "destroying" || d["operation"] != "destroy" {
		t.Fatalf("destroy: %v", d)
	}
	waitUntil(t, "run destroyed", func() bool {
		done = c.must(http.MethodGet, "/v1/orgs/acme/infra/runs/"+runID, nil, http.StatusOK)
		return done["status"] == "destroyed" || done["status"] == "failed"
	})
	if done["status"] != "destroyed" {
		t.Fatalf("destroy terminado: %v", done)
	}
	if status, _ := c.do(http.MethodGet, "/v1/orgs/acme/clusters/"+clusterID, nil); status != http.StatusNotFound {
		t.Errorf("el cluster debería haberse borrado: %d", status)
	}
	if done["cluster_id"] != "" {
		t.Errorf("cluster_id del run tras borrar el cluster: %v", done["cluster_id"])
	}

	// Eventos persistidos del run: status sí, line no.
	evs := c.must(http.MethodGet, "/v1/orgs/acme/events?subject_type=infra_run&subject_id="+runID+"&limit=200", nil, http.StatusOK)
	kinds := map[string]int{}
	for _, e := range itemsOf(evs) {
		kinds[str(e, "kind")]++
	}
	if kinds["infra_run.status"] < 6 || kinds["infra_run.line"] != 0 {
		t.Errorf("eventos del run: %v", kinds)
	}
	clEvs := c.must(http.MethodGet, "/v1/orgs/acme/events?subject_type=cluster&subject_id="+clusterID, nil, http.StatusOK)
	kinds = map[string]int{}
	for _, e := range itemsOf(clEvs) {
		kinds[str(e, "kind")]++
	}
	if kinds["cluster.created"] != 1 || kinds["cluster.infra_ready"] != 1 || kinds["cluster.deleted"] != 1 {
		t.Errorf("eventos del cluster: %v", kinds)
	}

	// Un run que falla deja el cluster en failed y se puede reintentar.
	failBody := map[string]any{"cloud_account_id": accID, "name": "fail-plan", "env": "nonprod", "domain": "apps.dev.acme.com"}
	fr := c.must(http.MethodPost, "/v1/orgs/acme/infra/clusters", failBody, http.StatusAccepted)
	waitUntil(t, "run failed", func() bool {
		done = c.must(http.MethodGet, "/v1/orgs/acme/infra/runs/"+str(fr, "id"), nil, http.StatusOK)
		return done["status"] == "failed"
	})
	if !strings.Contains(str(done, "error"), "ManageInfrastructure=true") {
		t.Errorf("error del run fallido: %v", done["error"])
	}
	fcl := c.must(http.MethodGet, "/v1/orgs/acme/clusters/"+str(fr, "cluster_id"), nil, http.StatusOK)
	if fcl["status"] != "failed" || fcl["infra_run_id"] != str(fr, "id") {
		t.Errorf("cluster fallido: %v", fcl)
	}
	retried := c.must(http.MethodPost, "/v1/orgs/acme/infra/runs/"+str(fr, "id")+"/retry", nil, http.StatusAccepted)
	if retried["status"] != "queued" || retried["attempt"] != float64(1) {
		t.Errorf("retry: %v", retried)
	}
	waitUntil(t, "segundo intento", func() bool {
		done = c.must(http.MethodGet, "/v1/orgs/acme/infra/runs/"+str(fr, "id"), nil, http.StatusOK)
		return done["status"] == "failed" && done["attempt"] == float64(2)
	})
	// Se puede borrar el cluster fallido a mano (no tiene scopes).
	c.must(http.MethodDelete, "/v1/orgs/acme/clusters/"+str(fr, "cluster_id"), nil, http.StatusNoContent)
	done = c.must(http.MethodGet, "/v1/orgs/acme/infra/runs/"+str(fr, "id"), nil, http.StatusOK)
	if done["cluster_id"] != "" {
		t.Errorf("el run sobrevive al cluster con cluster_id vacío: %v", done["cluster_id"])
	}
	// La fila de estado del run destruido quedó vacía; nada en la base es texto plano.
	st, err := env.srv.Store().GetInfraState(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Ciphertext) != 0 {
		t.Errorf("estado tras destroy: %d bytes", len(st.Ciphertext))
	}
}
