package gcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"rampa.dev/pkg/provider"
)

func account() provider.CloudAccount {
	return provider.CloudAccount{
		Name:        "acme-gcp",
		Kind:        provider.GCP,
		ExternalRef: "acme-prod-123456",
		Region:      "southamerica-east1",
		Settings: map[string]string{
			"service_account_email":      "rampa-connect@acme-prod-123456.iam.gserviceaccount.com",
			"workload_identity_provider": "projects/1/locations/global/workloadIdentityPools/rampa-acme/providers/rampa",
		},
	}
}

func TestBasics(t *testing.T) {
	p := New()
	if p.Kind() != provider.GCP || p.Available() || p.DisplayName() == "" {
		t.Fatalf("kind=%s available=%v", p.Kind(), p.Available())
	}
	if _, ok := provider.Get(provider.GCP); !ok {
		t.Error("gcp no se registró en init")
	}
}

func TestVerifyNotImplemented(t *testing.T) {
	_, err := New().Verify(context.Background(), account())
	if !errors.Is(err, provider.ErrNotImplemented) {
		t.Fatalf("esperaba ErrNotImplemented, dio %v", err)
	}
	if !strings.Contains(err.Error(), "acme-gcp") {
		t.Errorf("el error no tiene contexto: %v", err)
	}
}

func TestRegistry(t *testing.T) {
	p := New()
	got, err := p.Registry(account())
	if err != nil || got != "southamerica-east1-docker.pkg.dev/acme-prod-123456" {
		t.Errorf("Registry = %q, %v", got, err)
	}

	acc := account()
	acc.ExternalRef = ""
	acc.Settings["project_id"] = "from-settings"
	got, err = p.Registry(acc)
	if err != nil || got != "southamerica-east1-docker.pkg.dev/from-settings" {
		t.Errorf("Registry con fallback = %q, %v", got, err)
	}

	acc.Settings = nil
	if _, err := p.Registry(acc); err == nil {
		t.Error("sin proyecto debería fallar")
	}
	acc = account()
	acc.Region = ""
	if _, err := p.Registry(acc); err == nil {
		t.Error("sin región debería fallar")
	}
}

func TestConnectInstructions(t *testing.T) {
	ins := New().ConnectInstructions(provider.ConnectRequest{OrgSlug: "acme", ExternalID: "ext-123", CallbackURL: "https://api.rampa.dev/"})
	if ins.URL != "" {
		t.Error("GCP no tiene link de un click")
	}
	if !strings.Contains(ins.Description, "Workload Identity Federation") || !strings.Contains(ins.Description, "https://api.rampa.dev") {
		t.Errorf("descripción: %s", ins.Description)
	}
	for _, s := range []string{"workload-identity-pools create rampa-acme", "--issuer-uri=https://api.rampa.dev", "--allowed-audiences=ext-123", "roles/iam.workloadIdentityUser", "subject/ext-123"} {
		if !strings.Contains(ins.Command, s) {
			t.Errorf("el comando no contiene %q", s)
		}
	}
	names := map[string]bool{}
	for _, f := range ins.Fields {
		names[f.Name] = f.Required
	}
	for _, n := range []string{"external_ref", "service_account_email", "workload_identity_provider"} {
		if !names[n] {
			t.Errorf("falta el field requerido %s (fields=%v)", n, names)
		}
	}
}

func TestSecretStoreSpecAndAnnotations(t *testing.T) {
	p := New()
	spec := p.SecretStoreSpec(account(), provider.ClusterInfo{Name: "gke-prod", Region: "southamerica-east1"})
	if spec.Name != "gcp-secret-manager" || spec.Object["kind"] != "ClusterSecretStore" {
		t.Errorf("spec = %+v", spec)
	}
	gcpsm := spec.Object["spec"].(map[string]any)["provider"].(map[string]any)["gcpsm"].(map[string]any)
	if gcpsm["projectID"] != "acme-prod-123456" {
		t.Errorf("projectID = %v", gcpsm["projectID"])
	}
	wi := gcpsm["auth"].(map[string]any)["workloadIdentity"].(map[string]any)
	if wi["clusterName"] != "gke-prod" || wi["clusterLocation"] != "southamerica-east1" {
		t.Errorf("workloadIdentity = %v", wi)
	}
	ref := wi["serviceAccountRef"].(map[string]any)
	if ref["name"] != "rampa-agent" || ref["namespace"] != "rampa-system" {
		t.Errorf("serviceAccountRef = %v", ref)
	}

	ann := p.WorkloadIdentityAnnotations(account(), "app@acme.iam.gserviceaccount.com")
	if ann["iam.gke.io/gcp-service-account"] != "app@acme.iam.gserviceaccount.com" {
		t.Errorf("annotations = %v", ann)
	}
	ref2, ok := p.ServiceModule("postgres")
	if ok || !strings.Contains(ref2.Source, "//terraform/modules/gcp/postgres") {
		t.Errorf("ServiceModule = %v, %v", ref2, ok)
	}
}
