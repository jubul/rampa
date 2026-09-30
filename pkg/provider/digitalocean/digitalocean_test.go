package digitalocean

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rampa.dev/pkg/provider"
)

func account() provider.CloudAccount {
	return provider.CloudAccount{
		ID:          "acc-do",
		Kind:        provider.DigitalOcean,
		Name:        "acme-do",
		Region:      "nyc3",
		Settings:    map[string]string{"registry_name": "acme"},
		Credentials: map[string]string{"api_token": "dop_v1_fake_token"},
	}
}

// fakeDO simula GET /v2/account y registra el header Authorization recibido.
func fakeDO(t *testing.T, status int, body string) (*httptest.Server, *string) {
	t.Helper()
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/account" || r.Method != http.MethodGet {
			http.Error(w, "ruta inesperada", http.StatusNotFound)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts, &gotAuth
}

func TestBasics(t *testing.T) {
	p := New(Config{})
	if p.Kind() != provider.DigitalOcean || !p.Available() || p.Auth() != provider.AuthToken || p.DisplayName() != "DigitalOcean" {
		t.Fatalf("kind=%s available=%v auth=%s", p.Kind(), p.Available(), p.Auth())
	}
	if _, ok := provider.Get(provider.DigitalOcean); !ok {
		t.Error("digitalocean no se registró en init")
	}
	if p.cfg.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL default: %s", p.cfg.BaseURL)
	}
	if New(Config{BaseURL: "http://x/"}).cfg.BaseURL != "http://x" {
		t.Error("BaseURL debería quedar sin barra final")
	}
	term := p.Terminology()
	if term.VM.ES != "Droplet" || term.Cluster.EN != "DOKS cluster" || term.SecretStore.ES != "Secrets de Kubernetes" || term.Registry.EN != "DOCR" {
		t.Errorf("terminología: %+v", term)
	}
}

func TestVerifyOK(t *testing.T) {
	ts, gotAuth := fakeDO(t, http.StatusOK, `{"account":{"email":"ops@acme.com","uuid":"u-1","status":"active","email_verified":true}}`)
	p := New(Config{BaseURL: ts.URL})
	v, err := p.Verify(context.Background(), account())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !v.OK || v.Identity != "ops@acme.com" {
		t.Errorf("verification: %+v", v)
	}
	if *gotAuth != "Bearer dop_v1_fake_token" {
		t.Errorf("Authorization = %q", *gotAuth)
	}
	// Sin email, la identidad es el uuid.
	ts2, _ := fakeDO(t, http.StatusOK, `{"account":{"uuid":"u-2","status":"locked"}}`)
	v, err = New(Config{BaseURL: ts2.URL}).Verify(context.Background(), account())
	if err != nil || !v.OK || v.Identity != "u-2" || !strings.Contains(v.Message, "locked") {
		t.Errorf("identidad por uuid: %+v %v", v, err)
	}
}

func TestVerifyRejected(t *testing.T) {
	ts, _ := fakeDO(t, http.StatusUnauthorized, `{"id":"Unauthorized","message":"Unable to authenticate you"}`)
	v, err := New(Config{BaseURL: ts.URL}).Verify(context.Background(), account())
	if err != nil {
		t.Fatalf("un rechazo de DigitalOcean no es un error nuestro: %v", err)
	}
	if v.OK || !strings.Contains(v.Message, "401") || strings.Contains(v.Message, "dop_v1_fake_token") {
		t.Errorf("verification: %+v", v)
	}
	ts5, _ := fakeDO(t, http.StatusInternalServerError, `oops`)
	if v, err := New(Config{BaseURL: ts5.URL}).Verify(context.Background(), account()); err != nil || v.OK || !strings.Contains(v.Message, "500") {
		t.Errorf("500: %+v %v", v, err)
	}
}

func TestVerifyInputErrors(t *testing.T) {
	ts, gotAuth := fakeDO(t, http.StatusOK, `{}`)
	p := New(Config{BaseURL: ts.URL})
	acc := account()
	acc.Credentials = nil
	_, err := p.Verify(context.Background(), acc)
	if err == nil || !strings.Contains(err.Error(), "api_token") {
		t.Errorf("sin token debería fallar con error: %v", err)
	}
	if *gotAuth != "" {
		t.Error("sin token no debería llamar a la API")
	}
	// Red caída: error, no Verification.
	down := New(Config{BaseURL: "http://127.0.0.1:1"})
	if _, err := down.Verify(context.Background(), account()); err == nil {
		t.Error("sin red debería devolver error")
	}
}

func TestRegistry(t *testing.T) {
	p := New(Config{})
	got, err := p.Registry(account())
	if err != nil || got != "registry.digitalocean.com/acme" {
		t.Errorf("Registry = %q, %v", got, err)
	}
	acc := account()
	acc.Settings = nil
	if _, err := p.Registry(acc); err == nil || !strings.Contains(err.Error(), "registry_name") {
		t.Errorf("sin registry_name: %v", err)
	}
	for _, bad := range []string{"Acme", "-acme", "acme-", "a b", strings.Repeat("a", 64)} {
		acc := account()
		acc.Settings["registry_name"] = bad
		if _, err := p.Registry(acc); err == nil {
			t.Errorf("registry_name %q debería ser inválido", bad)
		}
	}
}

func TestConnectInstructions(t *testing.T) {
	ins := New(Config{}).ConnectInstructions(provider.ConnectRequest{OrgSlug: "acme", ExternalID: "ext-1", CallbackURL: "https://api.rampa.dev"})
	if ins.URL != TokensURL || !strings.Contains(ins.Description, "rampa-acme") || !strings.Contains(ins.Description, "Custom Scopes") || !strings.Contains(ins.Description, "cifrado") {
		t.Errorf("instrucciones: %+v", ins)
	}
	if !strings.Contains(ins.Command, "/v2/account") {
		t.Errorf("comando: %s", ins.Command)
	}
	var token, registry *provider.Field
	for i := range ins.Fields {
		switch ins.Fields[i].Name {
		case "api_token":
			token = &ins.Fields[i]
		case "registry_name":
			registry = &ins.Fields[i]
		}
	}
	if token == nil || !token.Secret || !token.Required {
		t.Errorf("api_token debería ser secreto y requerido: %+v", token)
	}
	if registry == nil || registry.Secret || registry.Required {
		t.Errorf("registry_name debería ser opcional y no secreto: %+v", registry)
	}
	for _, f := range ins.Fields {
		if f.Name == "external_ref" {
			t.Error("DigitalOcean no pide external_ref")
		}
	}
}

func TestSecretStoreIdentityModulesAndCredentials(t *testing.T) {
	p := New(Config{})
	spec := p.SecretStoreSpec(account(), provider.ClusterInfo{Name: "doks"})
	if spec.Name != "kubernetes" || spec.Object == nil || len(spec.Object) != 0 {
		t.Errorf("SecretStoreSpec = %+v", spec)
	}
	if ann := p.WorkloadIdentityAnnotations(account(), "x"); ann == nil || len(ann) != 0 {
		t.Errorf("annotations = %v", ann)
	}
	if ref, ok := p.ServiceModule("postgres"); ok || !strings.Contains(ref.Source, "//terraform/modules/digitalocean/postgres") {
		t.Errorf("ServiceModule = %v, %v", ref, ok)
	}
	var cp provider.CredentialsProvider = p
	creds, err := cp.Credentials(context.Background(), account())
	if err != nil || creds.Env["DIGITALOCEAN_TOKEN"] != "dop_v1_fake_token" || creds.AccessKeyID != "" || !creds.Expires.IsZero() {
		t.Errorf("Credentials = %+v, %v", creds, err)
	}
	acc := account()
	acc.Credentials = map[string]string{}
	if _, err := cp.Credentials(context.Background(), acc); err == nil {
		t.Error("sin token debería fallar")
	}
}
