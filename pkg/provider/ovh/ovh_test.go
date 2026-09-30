package ovh

import (
	"context"
	"errors"
	"strings"
	"testing"

	"rampa.dev/pkg/provider"
)

func account() provider.CloudAccount {
	return provider.CloudAccount{
		Name: "acme-ovh", Kind: provider.OVH, ExternalRef: "0123456789abcdef", Region: "GRA9",
		Settings: map[string]string{"application_key": "ak", "registry_url": "https://abcd.c1.gra9.container-registry.ovh.net/"},
	}
}

func TestBasics(t *testing.T) {
	p := New()
	if p.Kind() != provider.OVH || p.Available() || p.Auth() != provider.AuthToken || p.DisplayName() != "OVHcloud" {
		t.Fatalf("kind=%s available=%v auth=%s", p.Kind(), p.Available(), p.Auth())
	}
	if _, ok := provider.Get(provider.OVH); !ok {
		t.Error("ovh no se registró en init")
	}
	term := p.Terminology()
	if term.Project.ES != "proyecto de Public Cloud" || term.Cluster.EN != "Managed Kubernetes" || term.Registry.ES != "Managed Private Registry" || term.SecretStore.EN != "Kubernetes Secrets" || term.VM.ES != "instancia" {
		t.Errorf("terminología: %+v", term)
	}
	if _, err := p.Verify(context.Background(), account()); !errors.Is(err, provider.ErrNotImplemented) {
		t.Errorf("Verify: %v", err)
	}
}

func TestInstructionsRegistryAndStore(t *testing.T) {
	p := New()
	ins := p.ConnectInstructions(provider.ConnectRequest{OrgSlug: "acme"})
	if ins.URL != TokensURL || !strings.Contains(ins.Description, "rampa-acme") || !strings.Contains(ins.Description, "/cloud/project/<proyecto>/kube/*") {
		t.Errorf("instrucciones: %+v", ins)
	}
	secrets := map[string]bool{}
	for _, f := range ins.Fields {
		if f.Secret {
			secrets[f.Name] = true
		}
	}
	if len(secrets) != 2 || !secrets["application_secret"] || !secrets["consumer_key"] {
		t.Errorf("campos secretos: %v", secrets)
	}
	got, err := p.Registry(account())
	if err != nil || got != "abcd.c1.gra9.container-registry.ovh.net" {
		t.Errorf("Registry = %q, %v", got, err)
	}
	acc := account()
	acc.Settings = nil
	if _, err := p.Registry(acc); err == nil {
		t.Error("sin registry_url debería fallar")
	}
	spec := p.SecretStoreSpec(account(), provider.ClusterInfo{})
	if spec.Name != "kubernetes" || len(spec.Object) != 0 {
		t.Errorf("SecretStoreSpec = %+v", spec)
	}
	if len(p.WorkloadIdentityAnnotations(account(), "x")) != 0 {
		t.Error("OVH no usa anotaciones")
	}
	if _, ok := p.ServiceModule("postgres"); ok {
		t.Error("ningún módulo de OVH está disponible")
	}
}
