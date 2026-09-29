package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/protocol"
)

func loadExample(t *testing.T) *manifest.Application {
	t.Helper()
	path := filepath.Join("..", "..", "..", "examples", "checkout-api", "rampa.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leyendo el ejemplo: %v", err)
	}
	app, err := manifest.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if errs := app.Validate(); len(errs) > 0 {
		t.Fatalf("el ejemplo no valida: %v", errs)
	}
	return app
}

var target = Target{
	Registry:     "123456789012.dkr.ecr.us-east-1.amazonaws.com",
	Domain:       "apps.dev.acme.com",
	IngressClass: "nginx",
	SecretStore:  "aws-secrets-manager",
}

func kinds(r *Rendered) []string {
	out := append([]string(nil), r.Kinds...)
	sort.Strings(out)
	return out
}

func TestLoadChart(t *testing.T) {
	ch, err := LoadChart()
	if err != nil {
		t.Fatal(err)
	}
	if ch.Name() != "rampa-app" {
		t.Errorf("nombre del chart: %s", ch.Name())
	}
	if len(ch.Templates) < 5 {
		t.Errorf("pocos templates cargados: %d", len(ch.Templates))
	}
	for _, tpl := range ch.Templates {
		if filepath.IsAbs(tpl.Name) || tpl.Name[:len("templates/")] != "templates/" {
			t.Errorf("template con ruta inesperada (falta quitar el prefijo rampa-app/?): %s", tpl.Name)
		}
	}
}

func TestRenderDev(t *testing.T) {
	p, err := New(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	app := loadExample(t)
	v, warns, err := p.Values(app, "dev", "sha-abc1234", target)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("no esperaba avisos: %v", warns)
	}
	if v.Image.Repository != target.Registry+"/rampa/checkout-api" || v.Image.Tag != "sha-abc1234" {
		t.Errorf("imagen: %+v", v.Image)
	}
	r, err := p.RenderObjects(v)
	if err != nil {
		t.Fatal(err)
	}
	got := kinds(r)
	// dev tiene una sola réplica, así que el chart no genera PodDisruptionBudget.
	want := []string{"Deployment", "ExternalSecret", "Ingress", "Service", "ServiceAccount"}
	if !equal(got, want) {
		t.Errorf("kinds en dev = %v, quería %v", got, want)
	}
	for _, k := range got {
		if k == "Rollout" || k == "HorizontalPodAutoscaler" {
			t.Errorf("dev no debería tener %s", k)
		}
	}
	// Todos los objetos tienen metadata.name y el label de app.
	for i, obj := range r.Objects {
		var o struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(obj, &o); err != nil {
			t.Fatalf("objeto %d no es JSON válido: %v", i, err)
		}
		if o.Metadata.Name == "" || o.Metadata.Labels["rampa.dev/app"] != "checkout-api" || o.Metadata.Labels["rampa.dev/scope"] != "dev" {
			t.Errorf("objeto %s sin nombre o labels: %+v", o.Kind, o.Metadata)
		}
	}
}

func TestRenderProdHasRolloutAndHPA(t *testing.T) {
	p, err := New(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	app := loadExample(t)
	v, _, err := p.Values(app, "prod", "sha-abc1234", Target{Registry: target.Registry, Domain: "apps.prod.acme.com", IngressClass: "nginx", SecretStore: "aws-secrets-manager"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Ingress.Host != "checkout.example.com" {
		t.Errorf("prod debería usar el domain del manifest: %s", v.Ingress.Host)
	}
	r, err := p.RenderObjects(v)
	if err != nil {
		t.Fatal(err)
	}
	got := kinds(r)
	want := []string{"ExternalSecret", "HorizontalPodAutoscaler", "Ingress", "PodDisruptionBudget", "Rollout", "Service", "ServiceAccount"}
	if !equal(got, want) {
		t.Errorf("kinds en prod = %v, quería %v", got, want)
	}
	for _, k := range got {
		if k == "Deployment" {
			t.Error("prod con canary no debería tener Deployment")
		}
	}
}

func TestRenderEphemeralHasQuota(t *testing.T) {
	p, err := New(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	app := loadExample(t)
	v, _, err := p.Values(app, "pr-42", "sha-abc1234", target)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.RenderObjects(v)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range r.Kinds {
		if k == "ResourceQuota" {
			found = true
		}
	}
	if !found {
		t.Errorf("pr-42 debería tener ResourceQuota: %v", r.Kinds)
	}
}

func TestRenderFromStoredValues(t *testing.T) {
	p, err := New(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	app := loadExample(t)
	v, _, err := p.Values(app, "staging", "sha-abc1234", target)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ValuesToMap(v)
	if err != nil {
		t.Fatal(err)
	}
	// Ida y vuelta por JSON, como cuando se guarda en la columna values.
	b, _ := json.Marshal(m)
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	r1, err := p.RenderObjects(v)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := p.RenderObjectsFromMap(back, "checkout-api", "checkout-api-staging")
	if err != nil {
		t.Fatal(err)
	}
	if !equal(kinds(r1), kinds(r2)) {
		t.Errorf("render desde values guardados difiere: %v vs %v", kinds(r1), kinds(r2))
	}
}

func TestBuildCommand(t *testing.T) {
	p, err := New(7 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	objs := []json.RawMessage{json.RawMessage(`{"kind":"Service"}`)}
	cmd := p.BuildCommand(CommandInput{DeploymentID: "dep-1", App: "checkout-api", Scope: "dev", Objects: objs})
	if cmd.ID == "" || cmd.Kind != protocol.CommandDeploy || cmd.Deploy == nil {
		t.Fatalf("comando mal armado: %+v", cmd)
	}
	d := cmd.Deploy
	if d.Namespace != "checkout-api-dev" || d.TimeoutSeconds != 420 || len(d.Objects) != 1 {
		t.Errorf("deploy command: %+v", d)
	}
	wantLabels := map[string]string{
		"rampa.dev/app":                "checkout-api",
		"rampa.dev/scope":              "dev",
		"rampa.dev/deployment":         "dep-1",
		"app.kubernetes.io/managed-by": "rampa",
	}
	for k, v := range wantLabels {
		if d.Labels[k] != v {
			t.Errorf("label %s = %q, quería %q", k, d.Labels[k], v)
		}
	}
	u := UndeployCommand("checkout-api", "pr-3")
	if u.Kind != protocol.CommandUndeploy || u.Undeploy.Namespace != "checkout-api-pr-3" || !u.Undeploy.Wait {
		t.Errorf("undeploy: %+v", u)
	}
}

func TestSplitDocs(t *testing.T) {
	docs := docSeparator.Split("a: 1\n---\n# solo comentario\n---\n\n---\nb: 2\n", -1)
	nonEmpty := 0
	for _, d := range docs {
		if !isEmptyDoc(d) {
			nonEmpty++
		}
	}
	if nonEmpty != 2 {
		t.Errorf("esperaba 2 documentos con contenido, tengo %d", nonEmpty)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
