package render

import (
	"strings"
	"testing"

	"rampa.dev/pkg/manifest"
)

func load(t *testing.T) *manifest.Application {
	t.Helper()
	app, err := manifest.LoadValid("../manifest/testdata/valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func platform() *Platform {
	return &Platform{
		Registry: "123456789012.dkr.ecr.us-east-1.amazonaws.com",
		Clusters: map[string]Cluster{
			"nonprod": {Domain: "apps.dev.example.com", IngressClass: "nginx", Scopes: []string{"dev", "staging", "pr-*"}},
			"prod":    {Domain: "apps.example.com", IngressClass: "alb", Scopes: []string{"prod"}},
		},
	}
}

func TestProd(t *testing.T) {
	v, warns, err := Build(load(t), "prod", Options{Image: "sha-a1b2c3d", Platform: platform()})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("prod no debería avisar nada: %v", warns)
	}
	if v.Image.Repository != "123456789012.dkr.ecr.us-east-1.amazonaws.com/rampa/checkout-api" || v.Image.Tag != "sha-a1b2c3d" {
		t.Errorf("imagen: %+v", v.Image)
	}
	if v.Ingress.Host != "checkout.example.com" || URL(v) != "https://checkout.example.com" {
		t.Errorf("host/url: %q %q", v.Ingress.Host, URL(v))
	}
	if v.Autoscaling == nil || !v.Autoscaling.Enabled || v.Autoscaling.Max != 10 || v.Replicas != 3 {
		t.Errorf("autoscaling: %+v replicas=%d", v.Autoscaling, v.Replicas)
	}
	if v.Strategy != "canary" || v.Size != "M" || v.Cluster.IngressClass != "alb" {
		t.Errorf("strategy/size/cluster: %s %s %+v", v.Strategy, v.Size, v.Cluster)
	}
	wantSecrets := map[string]string{
		"API_KEY": "rampa/checkout-api/prod/API_KEY",
		"DB_URL":  "rampa/services/checkout-api/checkout-db/prod",
	}
	for _, s := range v.Secrets {
		if wantSecrets[s.Name] != s.RemoteKey {
			t.Errorf("secret %s: %q", s.Name, s.RemoteKey)
		}
		if s.Name == "DB_URL" && s.Property != "url" {
			t.Errorf("DB_URL property: %q", s.Property)
		}
	}
	if len(v.Secrets) != 2 {
		t.Errorf("quiero 2 secrets, tengo %d", len(v.Secrets))
	}
	if len(v.Env) != 2 { // LOG_LEVEL, MAX_ITEMS
		t.Errorf("env: %+v", v.Env)
	}
}

func TestEphemeral(t *testing.T) {
	v, _, err := Build(load(t), "pr-7", Options{Image: "sha-1234567", Platform: platform()})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Scope.Ephemeral || v.Scope.TTL != "72h" || v.Cluster.Domain != "apps.dev.example.com" {
		t.Errorf("scope/cluster: %+v %+v", v.Scope, v.Cluster)
	}
	if URL(v) != "https://checkout-api-pr-7.apps.dev.example.com" {
		t.Errorf("url: %s", URL(v))
	}
	env := map[string]string{}
	for _, e := range v.Env {
		env[e.Name] = e.Value
	}
	if env["LOG_LEVEL"] != "debug" || !strings.HasPrefix(env["DB_URL"], "postgres://") {
		t.Errorf("env: %v", env)
	}
	for _, s := range v.Secrets {
		if s.Name == "DB_URL" {
			t.Error("en efímeros DB_URL no viene de un service")
		}
	}
}

func TestWarningsWithoutPlatform(t *testing.T) {
	v, warns, err := Build(load(t), "dev", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if v.Image.Repository != "rampa/checkout-api" || v.Image.Tag != "latest" {
		t.Errorf("imagen sin platform: %+v", v.Image)
	}
	if len(warns) < 2 {
		t.Errorf("esperaba avisos por registry y tag: %v", warns)
	}
}

func TestFullImageReference(t *testing.T) {
	repo, tag := splitImage("1.dkr.ecr.us-east-1.amazonaws.com/rampa/x:sha-abc")
	if repo != "1.dkr.ecr.us-east-1.amazonaws.com/rampa/x" || tag != "sha-abc" {
		t.Errorf("%q %q", repo, tag)
	}
	if r, tg := splitImage("sha-abc"); r != "" || tg != "sha-abc" {
		t.Errorf("%q %q", r, tg)
	}
}

func TestClusterOptionWins(t *testing.T) {
	v, _, err := Build(load(t), "dev", Options{Image: "sha-1", Platform: platform(), Registry: "r.example.com",
		Cluster: &Cluster{Domain: "apps.cliente.com", IngressClass: "alb", SecretStore: "aws-sm"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Cluster.Domain != "apps.cliente.com" || v.Cluster.IngressClass != "alb" || v.Cluster.SecretStore != "aws-sm" {
		t.Errorf("Cluster debería pisar a Platform: %+v", v.Cluster)
	}
	if v.Image.Repository != "r.example.com/rampa/checkout-api" {
		t.Errorf("Registry debería pisar al de Platform: %s", v.Image.Repository)
	}
}
