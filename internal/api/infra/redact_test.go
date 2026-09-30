package infra

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/provider"
)

func TestRedactor(t *testing.T) {
	var r redactor
	r.add("supersecret-token", "corto", "", "supersecret-token-largo", "supersecret-token")
	got := r.redact("a supersecret-token-largo b supersecret-token c corto")
	if got != "a *** b *** c corto" {
		t.Errorf("redact: %q", got)
	}
	var nilR *redactor
	if nilR.redact("x") != "x" {
		t.Error("un redactor nil no cambia nada")
	}
}

// TestRunnerRedactsSecrets: un tofu que imprime las credenciales y los tokens
// del entorno no las deja en el log, en log_tail, en run.error ni en eventos.
func TestRunnerRedactsSecrets(t *testing.T) {
	const doToken = "dop_v1_TOKEN_DE_DIGITALOCEAN_123"
	e := newRunnerEnv(t)
	e.svc.SetCredentialsFunc(func(ctx context.Context, acc provider.CloudAccount) (provider.Credentials, error) {
		return provider.Credentials{
			AccessKeyID: fakeAccessKey, SecretAccessKey: fakeSecret, SessionToken: fakeSession, Expires: time.Now().Add(time.Hour),
			Env: map[string]string{"DIGITALOCEAN_TOKEN": doToken},
		}, nil
	})
	run, _ := e.createCluster("leak-secrets")
	e.startWorker()
	failed := e.waitStatus(run.ID, store.InfraRunFailed)

	logText := e.logText(run.ID)
	tail, err := e.f.st.InfraRunLogTail(context.Background(), e.f.orgID, run.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logText, "debug DIGITALOCEAN_TOKEN=***") || !strings.Contains(logText, "debug TF_VAR_bootstrap_token=***") || !strings.Contains(logText, "debug TF_HTTP_PASSWORD=***") {
		t.Errorf("el log debería mostrar ***:\n%s", logText)
	}
	if !strings.Contains(failed.Error, "AWS_SECRET_ACCESS_KEY=***") || !strings.Contains(failed.Error, "corto=x") {
		t.Errorf("run.error: %q", failed.Error)
	}
	all := []string{logText, failed.Error, strings.Join(tail, "\n")}
	e.mu.Lock()
	for _, ev := range e.events {
		raw, _ := json.Marshal(ev)
		all = append(all, string(raw))
	}
	e.mu.Unlock()
	joined := strings.Join(all, "\n")
	for _, secret := range []string{doToken, fakeSecret, fakeSession} {
		if strings.Contains(joined, secret) {
			t.Errorf("se filtró el secreto %q", secret)
		}
	}
	if !strings.Contains(strings.Join(tail, "\n"), "***") {
		t.Errorf("log_tail debería tener ***: %v", tail)
	}
	// Ni los tokens del run: el bootstrap token y el del backend de estado.
	if strings.Contains(joined, "rampa_bootstrap_") || strings.Contains(joined, StateTokenPrefix) {
		t.Error("se filtró un token del run")
	}
}
