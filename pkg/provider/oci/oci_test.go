package oci

import (
	"context"
	"errors"
	"strings"
	"testing"

	"rampa.dev/pkg/provider"
)

func account() provider.CloudAccount {
	return provider.CloudAccount{
		Name: "acme-oci", Kind: provider.OCI, ExternalRef: "ocid1.tenancy.oc1..aaaa", Region: "sa-saopaulo-1",
		Settings: map[string]string{"object_storage_namespace": "axaxnpcrorw5", "compartment_ocid": "ocid1.compartment.oc1..bbbb", "vault_ocid": "ocid1.vault.oc1..cccc"},
	}
}

func TestBasics(t *testing.T) {
	p := New()
	if p.Kind() != provider.OCI || p.Available() || p.Auth() != provider.AuthToken || p.DisplayName() == "" {
		t.Fatalf("kind=%s available=%v auth=%s", p.Kind(), p.Available(), p.Auth())
	}
	if _, ok := provider.Get(provider.OCI); !ok {
		t.Error("oci no se registró en init")
	}
	term := p.Terminology()
	if term.Account.EN != "OCI tenancy" || term.Project.ES != "compartment" || term.Cluster.ES != "cluster OKE" || term.Registry.ES != "OCIR" || term.SecretStore.EN != "OCI Vault" {
		t.Errorf("terminología: %+v", term)
	}
	if _, err := p.Verify(context.Background(), account()); !errors.Is(err, provider.ErrNotImplemented) {
		t.Errorf("Verify: %v", err)
	}
}

func TestInstructionsRegistryAndStore(t *testing.T) {
	p := New()
	ins := p.ConnectInstructions(provider.ConnectRequest{OrgSlug: "acme"})
	if !strings.Contains(ins.Description, "clave de API") || !strings.Contains(ins.Command, "rampa-connect-acme") {
		t.Errorf("instrucciones: %+v", ins)
	}
	secrets := 0
	for _, f := range ins.Fields {
		if f.Secret {
			secrets++
			if f.Name != "private_key" {
				t.Errorf("campo secreto inesperado: %s", f.Name)
			}
		}
	}
	if secrets != 1 {
		t.Errorf("esperaba un solo campo secreto, hay %d", secrets)
	}
	got, err := p.Registry(account())
	if err != nil || got != "sa-saopaulo-1.ocir.io/axaxnpcrorw5" {
		t.Errorf("Registry = %q, %v", got, err)
	}
	acc := account()
	acc.Settings = nil
	if _, err := p.Registry(acc); err == nil {
		t.Error("sin namespace debería fallar")
	}
	spec := p.SecretStoreSpec(account(), provider.ClusterInfo{Name: "oke"})
	oracle := spec.Object["spec"].(map[string]any)["provider"].(map[string]any)["oracle"].(map[string]any)
	if spec.Name != "oci-vault" || oracle["vault"] != "ocid1.vault.oc1..cccc" || oracle["region"] != "sa-saopaulo-1" {
		t.Errorf("SecretStoreSpec = %+v", spec)
	}
	if len(p.WorkloadIdentityAnnotations(account(), "x")) != 0 {
		t.Error("OKE no usa anotaciones")
	}
	if _, ok := p.ServiceModule("postgres"); ok {
		t.Error("ningún módulo de OCI está disponible")
	}
}
