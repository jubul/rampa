package provider_test

import (
	"encoding/json"
	"strings"
	"testing"

	"rampa.dev/pkg/provider"
	_ "rampa.dev/pkg/provider/aws"
	_ "rampa.dev/pkg/provider/azure"
	_ "rampa.dev/pkg/provider/digitalocean"
	_ "rampa.dev/pkg/provider/gcp"
	_ "rampa.dev/pkg/provider/oci"
	_ "rampa.dev/pkg/provider/ovh"
)

// TestAllProvidersHaveTerminology recorre los seis providers registrados y
// exige que cada término tenga español e inglés, y que Auth sea uno de los
// dos valores conocidos.
func TestAllProvidersHaveTerminology(t *testing.T) {
	want := map[provider.Kind]provider.AuthKind{
		provider.AWS: provider.AuthFederated, provider.GCP: provider.AuthFederated, provider.Azure: provider.AuthFederated,
		provider.DigitalOcean: provider.AuthToken, provider.OCI: provider.AuthToken, provider.OVH: provider.AuthToken,
	}
	seen := map[provider.Kind]bool{}
	for _, p := range provider.All() {
		auth, known := want[p.Kind()]
		if !known {
			continue // providers falsos de otros tests
		}
		seen[p.Kind()] = true
		if p.Auth() != auth {
			t.Errorf("%s: auth = %s, quería %s", p.Kind(), p.Auth(), auth)
		}
		for name, term := range p.Terminology().Terms() {
			if term.ES == "" || term.EN == "" {
				t.Errorf("%s: el término %s está incompleto: %+v", p.Kind(), name, term)
			}
		}
		if p.DisplayName() == "" {
			t.Errorf("%s: sin DisplayName", p.Kind())
		}
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("el provider %s no está registrado", k)
		}
	}
}

// TestTerminologyJSON fija los nombres de campo que consume la web.
func TestTerminologyJSON(t *testing.T) {
	p, ok := provider.Get(provider.DigitalOcean)
	if !ok {
		t.Fatal("digitalocean no registrado")
	}
	b, err := json.Marshal(p.Terminology())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"account", "project", "cluster", "vm", "registry", "secret_store", "region"} {
		if m[key]["es"] == "" || m[key]["en"] == "" {
			t.Errorf("falta %s.es/en en %s", key, b)
		}
	}
	if m["vm"]["es"] != "Droplet" {
		t.Errorf("vm.es = %q", m["vm"]["es"])
	}
	// Field.Secret y CloudAccount.Credentials: el primero sale en JSON, el segundo nunca.
	fb, _ := json.Marshal(provider.Field{Name: "api_token", Secret: true})
	if string(fb) != `{"name":"api_token","label":"","required":false,"secret":true}` {
		t.Errorf("Field JSON: %s", fb)
	}
	ab, _ := json.Marshal(provider.CloudAccount{ID: "1", Credentials: map[string]string{"api_token": "dop_v1_x"}})
	if strings.Contains(string(ab), "dop_v1_x") || strings.Contains(string(ab), "credentials") {
		t.Errorf("CloudAccount JSON filtra credenciales: %s", ab)
	}
}
