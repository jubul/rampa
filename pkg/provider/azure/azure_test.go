package azure

import (
	"context"
	"errors"
	"strings"
	"testing"

	"rampa.dev/pkg/provider"
)

func account() provider.CloudAccount {
	return provider.CloudAccount{
		Name:        "acme-azure",
		Kind:        provider.Azure,
		ExternalRef: "11111111-2222-3333-4444-555555555555",
		Region:      "brazilsouth",
		Settings: map[string]string{
			"tenant_id": "aaaaaaaa-0000-0000-0000-000000000000",
			"client_id": "bbbbbbbb-0000-0000-0000-000000000000",
			"acr_name":  "AcmeRampa",
		},
	}
}

func TestBasics(t *testing.T) {
	p := New()
	if p.Kind() != provider.Azure || p.Available() || p.DisplayName() == "" {
		t.Fatalf("kind=%s available=%v", p.Kind(), p.Available())
	}
	if _, ok := provider.Get(provider.Azure); !ok {
		t.Error("azure no se registró en init")
	}
}

func TestVerifyNotImplemented(t *testing.T) {
	_, err := New().Verify(context.Background(), account())
	if !errors.Is(err, provider.ErrNotImplemented) {
		t.Fatalf("esperaba ErrNotImplemented, dio %v", err)
	}
	if !strings.Contains(err.Error(), "acme-azure") {
		t.Errorf("el error no tiene contexto: %v", err)
	}
}

func TestRegistry(t *testing.T) {
	p := New()
	got, err := p.Registry(account())
	if err != nil || got != "acmerampa.azurecr.io" {
		t.Errorf("Registry = %q, %v", got, err)
	}
	for _, bad := range []string{"", "abc", "acme.azurecr.io", "acme-rampa", strings.Repeat("a", 51)} {
		acc := account()
		acc.Settings["acr_name"] = bad
		if _, err := p.Registry(acc); err == nil {
			t.Errorf("acr_name %q debería ser inválido", bad)
		}
	}
	acc := account()
	acc.Settings = nil
	if _, err := p.Registry(acc); err == nil {
		t.Error("sin settings debería fallar")
	}
}

func TestConnectInstructions(t *testing.T) {
	ins := New().ConnectInstructions(provider.ConnectRequest{OrgSlug: "acme", ExternalID: "ext-123", CallbackURL: "https://api.rampa.dev"})
	if ins.URL != "" {
		t.Error("Azure no tiene link de un click")
	}
	if !strings.Contains(ins.Description, "credencial federada") || !strings.Contains(ins.Description, "https://api.rampa.dev") {
		t.Errorf("descripción: %s", ins.Description)
	}
	for _, s := range []string{"az ad app create --display-name rampa-connect-acme", "federated-credential create", `"issuer":"https://api.rampa.dev"`, `"subject":"ext-123"`, "api://AzureADTokenExchange", "AcrPush"} {
		if !strings.Contains(ins.Command, s) {
			t.Errorf("el comando no contiene %q", s)
		}
	}
	names := map[string]bool{}
	for _, f := range ins.Fields {
		names[f.Name] = f.Required
	}
	for _, n := range []string{"external_ref", "tenant_id", "client_id", "acr_name"} {
		if !names[n] {
			t.Errorf("falta el field requerido %s (fields=%v)", n, names)
		}
	}
}

func TestSecretStoreSpecAndAnnotations(t *testing.T) {
	p := New()
	spec := p.SecretStoreSpec(account(), provider.ClusterInfo{Name: "aks-prod", Region: "brazilsouth"})
	if spec.Name != "azure-key-vault" || spec.Object["kind"] != "ClusterSecretStore" {
		t.Errorf("spec = %+v", spec)
	}
	kv := spec.Object["spec"].(map[string]any)["provider"].(map[string]any)["azurekv"].(map[string]any)
	if kv["authType"] != "WorkloadIdentity" || kv["vaultUrl"] != "https://rampa-aks-prod.vault.azure.net" || kv["tenantId"] != "aaaaaaaa-0000-0000-0000-000000000000" {
		t.Errorf("azurekv = %v", kv)
	}
	ref := kv["serviceAccountRef"].(map[string]any)
	if ref["name"] != "rampa-agent" || ref["namespace"] != "rampa-system" {
		t.Errorf("serviceAccountRef = %v", ref)
	}
	acc := account()
	acc.Settings["key_vault_name"] = "acme-secrets"
	kv = p.SecretStoreSpec(acc, provider.ClusterInfo{Name: "aks"}).Object["spec"].(map[string]any)["provider"].(map[string]any)["azurekv"].(map[string]any)
	if kv["vaultUrl"] != "https://acme-secrets.vault.azure.net" {
		t.Errorf("vaultUrl con key_vault_name = %v", kv["vaultUrl"])
	}

	ann := p.WorkloadIdentityAnnotations(account(), "cccccccc-0000-0000-0000-000000000000")
	if ann["azure.workload.identity/client-id"] != "cccccccc-0000-0000-0000-000000000000" {
		t.Errorf("annotations = %v", ann)
	}
	ref2, ok := p.ServiceModule("redis")
	if ok || !strings.Contains(ref2.Source, "//terraform/modules/azure/redis") {
		t.Errorf("ServiceModule = %v, %v", ref2, ok)
	}
}
