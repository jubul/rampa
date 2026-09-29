package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	"rampa.dev/pkg/provider"
)

func TestCredentialsAssumesRoleWithExternalIDAndRunSession(t *testing.T) {
	f := &fakeSTS{identityARN: "arn:aws:sts::123456789012:assumed-role/RampaConnect-RampaConnect/rampa-infra-abc"}
	p := newTestProvider(f)
	var cp provider.CredentialsProvider = p
	ctx := provider.WithRunID(context.Background(), "0f1e2d3c-4b5a-6978-8899-aabbccddeeff")
	creds, err := cp.Credentials(ctx, account())
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if creds.AccessKeyID != "ASIAFAKE" || creds.SecretAccessKey != "secret" || creds.SessionToken != "token" || creds.Expires.IsZero() {
		t.Errorf("credenciales: %+v", creds)
	}
	if creds.Env["AWS_REGION"] != "us-east-1" {
		t.Errorf("Env: %v", creds.Env)
	}
	if f.gotRoleARN != account().ExternalRef || f.gotExternalID != "ext-abc123" {
		t.Errorf("AssumeRole con rol %q y external id %q", f.gotRoleARN, f.gotExternalID)
	}
	if f.gotSession != "rampa-infra-0f1e2d3c" {
		t.Errorf("nombre de sesión: %q", f.gotSession)
	}
}

func TestCredentialsErrors(t *testing.T) {
	f := &fakeSTS{assumeErr: errors.New("AccessDenied: not authorized")}
	p := newTestProvider(f)
	if _, err := p.Credentials(context.Background(), account()); err == nil || !strings.Contains(err.Error(), "no pude asumir el rol") {
		t.Errorf("AssumeRole rechazado debería devolver error, tengo %v", err)
	}
	acc := account()
	acc.ExternalRef = "no-es-un-arn"
	if _, err := p.Credentials(context.Background(), acc); err == nil || !strings.Contains(err.Error(), "ARN") {
		t.Errorf("ARN inválido: %v", err)
	}
	acc = account()
	acc.Settings = map[string]string{}
	if _, err := p.Credentials(context.Background(), acc); err == nil || !strings.Contains(err.Error(), "external_id") {
		t.Errorf("sin external_id: %v", err)
	}
	noTrust := New(Config{STSClient: f})
	if _, err := noTrust.Credentials(context.Background(), account()); err == nil || !strings.Contains(err.Error(), EnvTrustedPrincipalARN) {
		t.Errorf("sin identidad del plano de control: %v", err)
	}
	if got := infraSessionName(""); got != "rampa-infra" {
		t.Errorf("sesión sin run: %q", got)
	}
	if got := infraSessionName("raro!id"); got != "rampa-infra-raro-id" {
		t.Errorf("sesión con caracteres raros: %q", got)
	}
}
