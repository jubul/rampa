package manifest

import (
	"strings"
	"testing"
)

func TestLoadValid(t *testing.T) {
	app, err := LoadValid("testdata/valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if app.Metadata.Name != "checkout-api" || app.Spec.Port != 8080 {
		t.Fatalf("parseo inesperado: %+v", app.Metadata)
	}
	if got := app.Spec.Scopes["dev"].Strategy; got != "rolling" {
		t.Errorf("default de strategy: quiero rolling, tengo %q", got)
	}
	if got := app.Spec.Scopes["pr"].TTL; got != "72h" {
		t.Errorf("ttl: %q", got)
	}
}

func TestParametersForOverridesAndDrops(t *testing.T) {
	app, err := LoadValid("testdata/valid.yaml")
	if err != nil {
		t.Fatal(err)
	}

	// prod: LOG_LEVEL genérico, DB_URL desde el service.
	params, dropped := app.ParametersFor("prod")
	got := byName(params)
	if got["LOG_LEVEL"].Value != "info" {
		t.Errorf("prod LOG_LEVEL = %q, quiero info", got["LOG_LEVEL"].Value)
	}
	if got["DB_URL"].FromService != "checkout-db" {
		t.Errorf("prod DB_URL debería venir del service: %+v", got["DB_URL"])
	}
	if got["MAX_ITEMS"].Value != "50" {
		t.Errorf("un value numérico debe parsear como string: %+v", got["MAX_ITEMS"])
	}
	if len(dropped) != 0 {
		t.Errorf("prod no debería descartar nada: %+v", dropped)
	}

	// pr-12: override de LOG_LEVEL, DB_URL plano porque no hay services en efímeros.
	params, dropped = app.ParametersFor("pr-12")
	got = byName(params)
	if got["LOG_LEVEL"].Value != "debug" {
		t.Errorf("pr LOG_LEVEL = %q, quiero debug", got["LOG_LEVEL"].Value)
	}
	if !strings.HasPrefix(got["DB_URL"].Value, "postgres://") {
		t.Errorf("pr DB_URL debería ser el plano: %+v", got["DB_URL"])
	}
	if len(dropped) != 0 {
		t.Errorf("el override plano reemplaza al fromService, no debería haber descartes: %+v", dropped)
	}

	// staging: sin override plano, el service existe.
	params, _ = app.ParametersFor("staging")
	if byName(params)["DB_URL"].FromService != "checkout-db" {
		t.Errorf("staging DB_URL debería venir del service")
	}

	// Orden estable: primero los genéricos en orden de declaración.
	if params[0].Name != "LOG_LEVEL" {
		t.Errorf("orden inesperado: %s", params[0].Name)
	}
}

func TestDropsServiceParamWhereServiceMissing(t *testing.T) {
	app, err := Parse([]byte(`
apiVersion: rampa.dev/v1
kind: Application
metadata: {name: w, team: t}
spec:
  type: worker
  scopes: {dev: {}, prod: {}}
  parameters:
    - {name: Q_URL, fromService: q, key: url}
  services:
    - {name: q, type: sqs, scopes: [prod]}
`))
	if err != nil {
		t.Fatal(err)
	}
	if errs := app.Validate(); len(errs) > 0 {
		t.Fatalf("debería ser válido: %v", errs)
	}
	params, dropped := app.ParametersFor("dev")
	if len(params) != 0 || len(dropped) != 1 || dropped[0].Name != "Q_URL" {
		t.Errorf("dev debería descartar Q_URL: params=%v dropped=%v", params, dropped)
	}
}

func TestResolveScope(t *testing.T) {
	app, _ := LoadValid("testdata/valid.yaml")
	s, eph, err := app.ResolveScope("pr-42")
	if err != nil || !eph || s.Size != "S" {
		t.Errorf("pr-42: %+v %v %v", s, eph, err)
	}
	if _, _, err := app.ResolveScope("qa"); err == nil {
		t.Error("qa no está declarado, debería fallar")
	}
	if _, _, err := app.ResolveScope("pr"); err == nil {
		t.Error("pr es la plantilla, no un scope")
	}
}

func TestValidateErrors(t *testing.T) {
	app, err := Parse([]byte(`
apiVersion: rampa.dev/v2
kind: Application
metadata:
  name: Checkout_API
  team: payments
  tier: 5
spec:
  type: lambda
  scopes:
    qa: {size: XXL, replicas: -1, strategy: yolo}
    pr: {domain: nope.com}
  parameters:
    - {name: lower, value: a, secret: true}
    - {name: DB, fromService: nope}
    - {name: PORT, value: "1"}
  services:
    - {name: db, type: oracle, scopes: [pr, prod]}
`))
	if err != nil {
		t.Fatal(err)
	}
	errs := app.Validate()
	want := []string{
		"apiVersion", "metadata.name", "metadata.tier", "spec.type",
		"spec.scopes.qa: nombre", "size \"XXL\"", "replicas", "strategy \"yolo\"",
		"spec.scopes.pr.domain", "al menos un scope permanente",
		"MAYUSCULAS", "exactamente uno", "fromService \"nope\" no existe", "necesita key",
		"reservado", "type \"oracle\"", "efímeros no reciben", "\"prod\" no está declarado",
	}
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("falta un error que contenga %q.\nErrores:\n%s", w, joined)
		}
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	_, err := Parse([]byte(`
apiVersion: rampa.dev/v1
kind: Application
metadata: {name: a, team: b}
spec:
  type: api
  replicas: 3
  scopes: {dev: {}}
`))
	if err == nil || !strings.Contains(err.Error(), "replicas") {
		t.Errorf("un campo desconocido debe fallar con su nombre, tengo: %v", err)
	}
}

func byName(ps []Parameter) map[string]Parameter {
	m := map[string]Parameter{}
	for _, p := range ps {
		m[p.Name] = p
	}
	return m
}

func TestScopeCompute(t *testing.T) {
	app, err := Parse([]byte(`
apiVersion: rampa.dev/v1
kind: Application
metadata: {name: a, team: b}
spec:
  type: api
  scopes:
    dev: {}
    prod: {compute: serverless, size: M}
    pr: {compute: serverless}
`))
	if err != nil {
		t.Fatal(err)
	}
	if errs := app.Validate(); len(errs) > 0 {
		t.Fatalf("debería validar: %v", errs)
	}
	// Default kubernetes; lo declarado se respeta; los efímeros heredan el de pr.
	if app.Spec.Scopes["dev"].Compute != ComputeKubernetes || app.Spec.Scopes["prod"].Compute != ComputeServerless {
		t.Errorf("compute: %+v", app.Spec.Scopes)
	}
	pr, eph, err := app.ResolveScope("pr-12")
	if err != nil || !eph || pr.Compute != ComputeServerless {
		t.Errorf("pr-12 debería heredar serverless de pr: %+v %v %v", pr, eph, err)
	}
	// Un valor desconocido falla con el nombre del campo.
	bad, _ := Parse([]byte(`
apiVersion: rampa.dev/v1
kind: Application
metadata: {name: a, team: b}
spec:
  type: api
  scopes:
    dev: {compute: lambda}
`))
	found := false
	for _, e := range bad.Validate() {
		if strings.Contains(e.Error(), "spec.scopes.dev.compute \"lambda\"") {
			found = true
		}
	}
	if !found {
		t.Errorf("compute inválido debería reportarse: %v", bad.Validate())
	}
	// El manifest generado conserva compute y vuelve igual.
	raw, err := MarshalManifest(app)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "compute: serverless") {
		t.Errorf("el manifest generado no lleva compute:\n%s", raw)
	}
	again, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if again.Spec.Scopes["prod"].Compute != ComputeServerless || again.Spec.Scopes["dev"].Compute != ComputeKubernetes {
		t.Errorf("round trip: %+v", again.Spec.Scopes)
	}
	// Y el spec estructurado también.
	spec := ToSpec(app)
	for _, sc := range spec.Scopes {
		want := ComputeKubernetes
		if sc.Name == "prod" || sc.Name == "pr" {
			want = ComputeServerless
		}
		if sc.Compute != want {
			t.Errorf("ToSpec %s: compute %q", sc.Name, sc.Compute)
		}
	}
	rebuilt, err := FromSpec(spec)
	if err != nil || rebuilt.Spec.Scopes["prod"].Compute != ComputeServerless {
		t.Errorf("FromSpec: %v %+v", err, rebuilt.Spec.Scopes)
	}
}

func TestValidateParameterName(t *testing.T) {
	for _, ok := range []string{"API_KEY", "A", "LOG_LEVEL2", "X_1_Y"} {
		if err := ValidateParameterName(ok); err != nil {
			t.Errorf("%q debería ser válido: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "lower", "1ABC", "API-KEY", "API KEY", "PORT", "RAMPA_X"} {
		if err := ValidateParameterName(bad); err == nil {
			t.Errorf("%q debería ser inválido", bad)
		}
	}
}
