package manifest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// docsSpecJSON es el ejemplo de ApplicationSpec de docs/api.md.
const docsSpecJSON = `{
  "name": "checkout-api",
  "team": "payments",
  "tier": 2,
  "description": "API de checkout",
  "type": "api",
  "runtime": "go",
  "port": 8080,
  "healthcheck": "/health",
  "schedule": "",
  "scopes": [
    {"name": "dev", "compute": "kubernetes", "size": "S", "replicas": 1, "strategy": "rolling", "domain": "", "autoscaling": null, "ttl": ""},
    {"name": "prod", "compute": "kubernetes", "size": "M", "replicas": 3, "strategy": "canary", "domain": "checkout.acme.com",
     "autoscaling": {"min": 3, "max": 10, "cpu": 70}, "ttl": ""},
    {"name": "pr", "compute": "kubernetes", "size": "S", "replicas": 1, "strategy": "rolling", "domain": "", "autoscaling": null, "ttl": "72h"}
  ],
  "parameters": [
    {"name": "LOG_LEVEL", "source": "value", "value": "info", "scopes": []},
    {"name": "LOG_LEVEL", "source": "value", "value": "debug", "scopes": ["dev", "pr"]},
    {"name": "API_KEY", "source": "secret", "scopes": []},
    {"name": "DB_URL", "source": "service", "service": "checkout-db", "key": "url", "scopes": []}
  ],
  "services": [
    {"name": "checkout-db", "type": "postgres", "size": "S", "scopes": ["prod"]}
  ]
}`

func docsSpec(t *testing.T) ApplicationSpec {
	t.Helper()
	var spec ApplicationSpec
	if err := json.Unmarshal([]byte(docsSpecJSON), &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

// TestSpecRoundTrip: spec -> manifest -> Parse -> ToSpec da el mismo spec.
func TestSpecRoundTrip(t *testing.T) {
	spec := docsSpec(t)
	app, err := FromSpec(spec)
	if err != nil {
		t.Fatalf("FromSpec: %v", err)
	}
	if errs := app.Validate(); len(errs) > 0 {
		t.Fatalf("el spec de docs/api.md debería ser válido: %v", errs)
	}
	raw, err := MarshalManifest(app)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, ManifestHeader) {
		t.Errorf("falta la cabecera:\n%s", text)
	}
	// Orden estable de scopes: dev antes que prod, prod antes que pr.
	iDev, iProd, iPR := strings.Index(text, "\n    dev:"), strings.Index(text, "\n    prod:"), strings.Index(text, "\n    pr:")
	if iDev < 0 || iProd < 0 || iPR < 0 || !(iDev < iProd && iProd < iPR) {
		t.Errorf("orden de scopes inesperado (dev=%d prod=%d pr=%d):\n%s", iDev, iProd, iPR, text)
	}
	// Indentación de 2 espacios, como el resto de los rampa.yaml del repo.
	if !strings.Contains(text, "\n    dev:\n      compute: kubernetes\n      size: S\n") {
		t.Errorf("indentación inesperada:\n%s", text)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse del manifest generado: %v\n%s", err, text)
	}
	if errs := parsed.Validate(); len(errs) > 0 {
		t.Fatalf("el manifest generado no valida: %v\n%s", errs, text)
	}
	got := ToSpec(parsed)
	if !reflect.DeepEqual(got, spec) {
		gb, _ := json.MarshalIndent(got, "", "  ")
		sb, _ := json.MarshalIndent(spec, "", "  ")
		t.Errorf("el spec no sobrevivió la ida y vuelta.\nOriginal:\n%s\nObtenido:\n%s\nManifest:\n%s", sb, gb, text)
	}
	// El JSON del spec obtenido lista [] y null como en docs/api.md, nunca omite scopes.
	gb, _ := json.Marshal(got)
	if !strings.Contains(string(gb), `"scopes":[]`) || !strings.Contains(string(gb), `"autoscaling":null`) {
		t.Errorf("JSON del spec: %s", gb)
	}
}

// TestManifestToSpecAndBack: el ejemplo del repo -> ToSpec -> FromSpec conserva
// el resultado de Validate y de ParametersFor en todos los scopes.
func TestManifestToSpecAndBack(t *testing.T) {
	original, err := LoadValid("../../examples/checkout-api/rampa.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := ToSpec(original)
	if names := scopeNames(spec); !reflect.DeepEqual(names, []string{"dev", "staging", "prod", "pr"}) {
		t.Errorf("orden de scopes: %v", names)
	}
	rebuilt, err := FromSpec(spec)
	if err != nil {
		t.Fatalf("FromSpec: %v", err)
	}
	if errs := rebuilt.Validate(); len(errs) != 0 {
		t.Fatalf("Validate tras la ida y vuelta: %v", errs)
	}
	if !reflect.DeepEqual(rebuilt.Spec.Scopes, original.Spec.Scopes) {
		t.Errorf("scopes distintos:\n%+v\n%+v", rebuilt.Spec.Scopes, original.Spec.Scopes)
	}
	if rebuilt.Metadata != original.Metadata {
		t.Errorf("metadata distinta: %+v vs %+v", rebuilt.Metadata, original.Metadata)
	}
	for _, scope := range []string{"dev", "staging", "prod", "pr-7"} {
		p1, d1 := original.ParametersFor(scope)
		p2, d2 := rebuilt.ParametersFor(scope)
		if !sameParams(p1, p2) || !sameParams(d1, d2) {
			t.Errorf("ParametersFor(%s) cambió:\n%+v\n%+v", scope, p1, p2)
		}
	}
	// Y el manifest generado desde ese spec vuelve a dar el mismo spec.
	raw, err := MarshalManifest(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if got := ToSpec(parsed); !reflect.DeepEqual(got, spec) {
		t.Errorf("spec distinto tras generar el manifest:\n%+v\n%+v", got, spec)
	}
	// Un value numérico sigue siendo string.
	if !strings.Contains(string(raw), `value: "50"`) {
		t.Errorf("MAX_ITEMS debería serializarse como string:\n%s", raw)
	}
}

func TestFromSpecDefaults(t *testing.T) {
	app, err := FromSpec(ApplicationSpec{
		Name: "w", Team: "t", Type: "api",
		Scopes:     []SpecScope{{Name: "dev"}, {Name: "pr"}},
		Parameters: []SpecParameter{{Name: "X", Source: SourceSecret}},
		Services:   []SpecService{{Name: "db", Type: "postgres"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if errs := app.Validate(); len(errs) > 0 {
		t.Fatalf("debería validar con defaults: %v", errs)
	}
	if app.Metadata.Tier != 2 || app.Spec.Port != 8080 || app.Spec.Healthcheck != "/health" {
		t.Errorf("defaults de metadata/spec: %+v %+v", app.Metadata, app.Spec)
	}
	dev := app.Spec.Scopes["dev"]
	if dev.Size != "S" || dev.Replicas != 1 || dev.Strategy != "rolling" || dev.Compute != ComputeKubernetes {
		t.Errorf("defaults de scope: %+v", dev)
	}
	if app.Spec.Scopes["pr"].TTL != "72h" {
		t.Errorf("ttl por defecto de pr: %+v", app.Spec.Scopes["pr"])
	}
	if !app.Spec.Parameters[0].Secret || app.Spec.Services[0].Size != "S" {
		t.Errorf("parameter/service: %+v %+v", app.Spec.Parameters, app.Spec.Services)
	}
}

func TestFromSpecErrors(t *testing.T) {
	_, err := FromSpec(ApplicationSpec{
		Name: "w", Team: "t", Type: "api",
		Scopes: []SpecScope{{Name: "dev"}, {Name: "dev"}, {Name: ""}},
		Parameters: []SpecParameter{
			{Name: "A", Source: "magic"},
			{Name: "B"},
			{Name: "C", Source: SourceValue, Value: "x", Service: "db"},
			{Name: "D", Source: SourceSecret, Value: "x"},
			{Name: "E", Source: SourceService, Service: "db", Key: "url", Value: "x"},
		},
	})
	if err == nil {
		t.Fatal("esperaba errores")
	}
	msg := err.Error()
	for _, want := range []string{"repetido", "name es requerido", `source "magic" inválido`, "source es requerido", "service y key solo aplican", "un secret no lleva", "value no aplica con source service"} {
		if !strings.Contains(msg, want) {
			t.Errorf("falta un error con %q en:\n%s", want, msg)
		}
	}
	// errors.Join permite listar cada problema por separado.
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 7 {
		t.Errorf("esperaba 7 errores separados, tengo %v", err)
	}
}

func TestToSpecSourcesAndOrder(t *testing.T) {
	app, err := Parse([]byte(`
apiVersion: rampa.dev/v1
kind: Application
metadata: {name: w, team: t}
spec:
  type: worker
  scopes: {prod: {}, pr: {}, dev: {}, staging: {}, qa: {}}
  parameters:
    - {name: A, value: "1"}
    - {name: B, secret: true, scopes: [prod]}
    - {name: C, fromService: q, key: url}
  services:
    - {name: q, type: sqs, scopes: [prod]}
`))
	if err != nil {
		t.Fatal(err)
	}
	spec := ToSpec(app)
	if got := scopeNames(spec); !reflect.DeepEqual(got, []string{"dev", "staging", "prod", "pr", "qa"}) {
		t.Errorf("orden: %v", got)
	}
	if spec.Parameters[0].Source != SourceValue || spec.Parameters[1].Source != SourceSecret || spec.Parameters[2].Source != SourceService || spec.Parameters[2].Service != "q" || spec.Parameters[2].Key != "url" {
		t.Errorf("sources: %+v", spec.Parameters)
	}
	if spec.Parameters[0].Scopes == nil || spec.Services[0].Scopes[0] != "prod" || spec.Port != 0 {
		t.Errorf("spec: %+v", spec)
	}
}

func scopeNames(spec ApplicationSpec) []string {
	out := make([]string, 0, len(spec.Scopes))
	for _, s := range spec.Scopes {
		out = append(out, s.Name)
	}
	return out
}

func sameParams(a, b []Parameter) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Name != y.Name || x.Value != y.Value || x.Secret != y.Secret || x.FromService != y.FromService || x.Key != y.Key {
			return false
		}
	}
	return true
}
