package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	bootstrapTok = "rampa_bootstrap_SECRETO_BOOTSTRAP_123"
	clusterTok   = "rampa_cluster_SECRETO_CLUSTER_456"
	envTok       = "rampa_cluster_SECRETO_ENV_789"
)

// fakeAPI es la API de canje: responde según la secuencia de códigos y cuenta llamadas.
type fakeAPI struct {
	t        *testing.T
	calls    atomic.Int32
	statuses []int // código por llamada; la última se repite
	onCall   func(n int)
	srv      *httptest.Server
}

func newFakeAPI(t *testing.T, statuses ...int) *fakeAPI {
	t.Helper()
	a := &fakeAPI{t: t, statuses: statuses}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(a.calls.Add(1))
		if r.Method != http.MethodPost || r.URL.Path != Path {
			t.Errorf("petición inesperada: %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type: %q", ct)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["bootstrap_token"] != bootstrapTok {
			t.Errorf("body del canje: %v %v", body, err)
		}
		if a.onCall != nil {
			a.onCall(n)
		}
		status := a.statuses[len(a.statuses)-1]
		if n-1 < len(a.statuses) {
			status = a.statuses[n-1]
		}
		w.Header().Set("Content-Type", "application/json")
		switch status {
		case http.StatusOK:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"cluster_token": clusterTok, "cluster_id": "cl-1", "api_url": a.srv.URL})
		case http.StatusUnauthorized:
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bootstrap token usado o vencido"}}`))
		default:
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"todavía no"}}`))
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

// logBuffer captura los logs para verificar que ningún token aparece.
type logBuffer struct{ bytes.Buffer }

func (b *logBuffer) assertNoTokens(t *testing.T) {
	t.Helper()
	out := b.String()
	for _, tok := range []string{bootstrapTok, clusterTok, envTok} {
		if strings.Contains(out, tok) {
			t.Errorf("el log contiene un token: %s", out)
		}
	}
}

func testConfig(api *fakeAPI, cs *k8sfake.Clientset, buf *logBuffer) Config {
	cfg := Config{
		Namespace:  "rampa-system",
		Client:     cs,
		Logger:     slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		MinBackoff: time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
		MaxWait:    2 * time.Second,
	}
	if api != nil {
		cfg.APIURL = api.srv.URL
		cfg.BootstrapToken = bootstrapTok
	}
	return cfg
}

func secretObj(ns, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Type: corev1.SecretTypeOpaque, Data: data}
}

func getSecret(t *testing.T, cs *k8sfake.Clientset) *corev1.Secret {
	t.Helper()
	s, err := cs.CoreV1().Secrets("rampa-system").Get(context.Background(), SecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	return s
}

func TestEnvTokenWins(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	cs := k8sfake.NewClientset(secretObj("rampa-system", SecretName, map[string][]byte{"token": []byte("otro")}))
	buf := &logBuffer{}
	cfg := testConfig(api, cs, buf)
	cfg.ClusterToken = " " + envTok + " "

	tok, err := Resolve(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != envTok || tok.Source != SourceEnv || tok.Persisted {
		t.Errorf("token: %+v", tok)
	}
	if api.calls.Load() != 0 {
		t.Errorf("no debería canjear con RAMPA_CLUSTER_TOKEN seteado: %d llamadas", api.calls.Load())
	}
	buf.assertNoTokens(t)

	// Con env token no hace falta ni cluster.
	tok, err = Resolve(context.Background(), Config{ClusterToken: envTok, Logger: slog.New(slog.NewTextHandler(buf, nil))})
	if err != nil || tok.Value != envTok {
		t.Errorf("sin cluster: %+v %v", tok, err)
	}
}

func TestSecretWinsOverBootstrap(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	cs := k8sfake.NewClientset(secretObj("rampa-system", SecretName, map[string][]byte{"token": []byte(clusterTok + "\n")}))
	buf := &logBuffer{}

	tok, err := Resolve(context.Background(), testConfig(api, cs, buf))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != clusterTok || tok.Source != SourceSecret || !tok.Persisted {
		t.Errorf("token: %+v", tok)
	}
	if api.calls.Load() != 0 {
		t.Errorf("no debería canjear si el Secret existe: %d llamadas", api.calls.Load())
	}
	buf.assertNoTokens(t)
}

func TestExchangeAndPersist(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	cs := k8sfake.NewClientset()
	buf := &logBuffer{}

	tok, err := Resolve(context.Background(), testConfig(api, cs, buf))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != clusterTok || tok.Source != SourceBootstrap || tok.ClusterID != "cl-1" || !tok.Persisted {
		t.Errorf("token: %+v", tok)
	}
	if api.calls.Load() != 1 {
		t.Errorf("llamadas al canje: %d", api.calls.Load())
	}
	s := getSecret(t, cs)
	if string(s.Data["token"]) != clusterTok || s.Type != corev1.SecretTypeOpaque {
		t.Errorf("secret guardado: %+v", s)
	}
	if s.Labels["app.kubernetes.io/managed-by"] != "rampa" || s.Labels["rampa.dev/component"] != "agent" {
		t.Errorf("labels: %v", s.Labels)
	}
	buf.assertNoTokens(t)

	// Un segundo arranque toma el Secret y no vuelve a canjear.
	tok, err = Resolve(context.Background(), testConfig(api, cs, buf))
	if err != nil || tok.Source != SourceSecret || tok.Value != clusterTok {
		t.Errorf("segundo arranque: %+v %v", tok, err)
	}
	if api.calls.Load() != 1 {
		t.Errorf("el segundo arranque no debería canjear: %d llamadas", api.calls.Load())
	}
}

func TestExchangeUpdatesExistingSecret(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	// El Secret existe pero sin la clave: se actualiza en vez de fallar por AlreadyExists.
	cs := k8sfake.NewClientset(secretObj("rampa-system", SecretName, map[string][]byte{"otra": []byte("x")}))
	buf := &logBuffer{}

	tok, err := Resolve(context.Background(), testConfig(api, cs, buf))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != clusterTok || !tok.Persisted {
		t.Errorf("token: %+v", tok)
	}
	s := getSecret(t, cs)
	if string(s.Data["token"]) != clusterTok || string(s.Data["otra"]) != "x" {
		t.Errorf("merge del secret: %v", s.Data)
	}
	buf.assertNoTokens(t)
}

func TestRetriesUntilAPIResponds(t *testing.T) {
	api := newFakeAPI(t, http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusOK)
	cs := k8sfake.NewClientset()
	buf := &logBuffer{}

	tok, err := Resolve(context.Background(), testConfig(api, cs, buf))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != clusterTok || api.calls.Load() != 3 {
		t.Errorf("token %+v, llamadas %d", tok, api.calls.Load())
	}
	if !strings.Contains(buf.String(), "todavía no (internal)") {
		t.Errorf("el log debería traer el error estructurado de la API: %s", buf.String())
	}
	buf.assertNoTokens(t)
}

func TestUsedBootstrapWaitsForSecret(t *testing.T) {
	cs := k8sfake.NewClientset()
	api := newFakeAPI(t, http.StatusUnauthorized)
	// Simula que otro proceso canjeó el bootstrap y guardó el Secret.
	api.onCall = func(n int) {
		go func() {
			time.Sleep(20 * time.Millisecond)
			_, _ = cs.CoreV1().Secrets("rampa-system").Create(context.Background(),
				secretObj("rampa-system", SecretName, map[string][]byte{"token": []byte(clusterTok)}), metav1.CreateOptions{})
		}()
	}
	buf := &logBuffer{}

	tok, err := Resolve(context.Background(), testConfig(api, cs, buf))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != clusterTok || tok.Source != SourceSecret {
		t.Errorf("token: %+v", tok)
	}
	if api.calls.Load() != 1 {
		t.Errorf("tras un 401 no se vuelve a canjear: %d llamadas", api.calls.Load())
	}
	if !strings.Contains(buf.String(), "ya se usó o venció") {
		t.Errorf("el log debería explicar el 401: %s", buf.String())
	}
	buf.assertNoTokens(t)
}

func TestUsedBootstrapGivesUpAfterMaxWait(t *testing.T) {
	api := newFakeAPI(t, http.StatusUnauthorized)
	cs := k8sfake.NewClientset()
	buf := &logBuffer{}
	cfg := testConfig(api, cs, buf)
	cfg.MaxWait = 50 * time.Millisecond

	_, err := Resolve(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "ya se usó o venció") {
		t.Fatalf("esperaba error de bootstrap usado, fue %v", err)
	}
	if api.calls.Load() != 1 {
		t.Errorf("llamadas: %d", api.calls.Load())
	}
	buf.assertNoTokens(t)
}

func TestAPIDownGivesUpAfterMaxWait(t *testing.T) {
	api := newFakeAPI(t, http.StatusServiceUnavailable)
	cs := k8sfake.NewClientset()
	buf := &logBuffer{}
	cfg := testConfig(api, cs, buf)
	cfg.MaxWait = 50 * time.Millisecond

	_, err := Resolve(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "no pude canjear") {
		t.Fatalf("esperaba error de canje, fue %v", err)
	}
	if api.calls.Load() < 2 {
		t.Errorf("debería haber reintentado: %d llamadas", api.calls.Load())
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	api := newFakeAPI(t, http.StatusServiceUnavailable)
	cs := k8sfake.NewClientset()
	cfg := testConfig(api, cs, &logBuffer{})
	cfg.MinBackoff, cfg.MaxBackoff = time.Second, time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := Resolve(ctx, cfg)
	if err == nil || ctx.Err() == nil {
		t.Fatalf("esperaba cancelación, fue %v", err)
	}
}

func TestForbiddenReadingSecret(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	cs := k8sfake.NewClientset()
	cs.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, SecretName, nil)
	})
	buf := &logBuffer{}

	_, err := Resolve(context.Background(), testConfig(api, cs, buf))
	if err == nil || !strings.Contains(err.Error(), "no tiene permiso para leer el Secret rampa-system/rampa-agent-token") {
		t.Fatalf("esperaba error de permisos claro, fue %v", err)
	}
	if api.calls.Load() != 0 {
		t.Errorf("sin poder leer el Secret no se canjea: %d llamadas", api.calls.Load())
	}
}

func TestForbiddenSavingSecretKeepsToken(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	cs := k8sfake.NewClientset()
	cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, SecretName, nil)
	})
	buf := &logBuffer{}

	tok, err := Resolve(context.Background(), testConfig(api, cs, buf))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != clusterTok || tok.Source != SourceBootstrap || tok.Persisted {
		t.Errorf("token: %+v", tok)
	}
	if !strings.Contains(buf.String(), "no tiene permiso para escribir el Secret") {
		t.Errorf("el log debería avisar que no se guardó: %s", buf.String())
	}
	buf.assertNoTokens(t)
}

func TestTransientSaveErrorRetries(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	cs := k8sfake.NewClientset()
	var fails atomic.Int32
	cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		if fails.Add(1) <= 2 {
			return true, nil, apierrors.NewServiceUnavailable("api server ocupado")
		}
		return false, nil, nil
	})
	buf := &logBuffer{}

	tok, err := Resolve(context.Background(), testConfig(api, cs, buf))
	if err != nil {
		t.Fatal(err)
	}
	if !tok.Persisted || api.calls.Load() != 1 {
		t.Errorf("token %+v, llamadas %d (el canje no se repite)", tok, api.calls.Load())
	}
	if string(getSecret(t, cs).Data["token"]) != clusterTok {
		t.Error("el Secret no quedó guardado")
	}
}

func TestNothingConfigured(t *testing.T) {
	cs := k8sfake.NewClientset()
	cfg := testConfig(nil, cs, &logBuffer{})
	cfg.APIURL = "https://api.rampa.dev"

	_, err := Resolve(context.Background(), cfg)
	if err == nil {
		t.Fatal("esperaba error")
	}
	for _, want := range []string{"RAMPA_CLUSTER_TOKEN", "rampa-system/rampa-agent-token", "RAMPA_BOOTSTRAP_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("el error debería mencionar %s: %v", want, err)
		}
	}

	_, err = Resolve(context.Background(), Config{APIURL: "https://api.rampa.dev", BootstrapToken: bootstrapTok})
	if err == nil || !strings.Contains(err.Error(), "Kubernetes") {
		t.Errorf("sin clientset debería fallar claro: %v", err)
	}
}

func TestBootstrapURL(t *testing.T) {
	cases := map[string]string{
		"https://api.rampa.dev":        "https://api.rampa.dev/v1/agent/bootstrap",
		"https://api.rampa.dev/":       "https://api.rampa.dev/v1/agent/bootstrap",
		"http://localhost:8080/base/":  "http://localhost:8080/base/v1/agent/bootstrap",
		"wss://api.rampa.dev?x=1#frag": "https://api.rampa.dev/v1/agent/bootstrap",
		"ws://localhost:8080":          "http://localhost:8080/v1/agent/bootstrap",
	}
	for in, want := range cases {
		got, err := bootstrapURL(in)
		if err != nil || got != want {
			t.Errorf("%s: %s %v (esperaba %s)", in, got, err, want)
		}
	}
	for _, bad := range []string{"ftp://x", "api.rampa.dev", "https://"} {
		if _, err := bootstrapURL(bad); err == nil {
			t.Errorf("%q debería ser inválida", bad)
		}
	}
	if _, err := Resolve(context.Background(), Config{APIURL: "ftp://x", BootstrapToken: bootstrapTok, Client: k8sfake.NewClientset()}); err == nil {
		t.Error("URL inválida debería fallar antes de canjear")
	}
}

func TestBackoffBounds(t *testing.T) {
	cfg := Config{MinBackoff: time.Second, MaxBackoff: 30 * time.Second}.withDefaults()
	for attempt := 0; attempt < 40; attempt++ {
		d := cfg.backoff(attempt)
		if d < time.Second || d > 30*time.Second {
			t.Errorf("intento %d fuera de rango: %s", attempt, d)
		}
	}
	if d := cfg.backoff(10); d < 15*time.Second {
		t.Errorf("con 10 intentos debería estar cerca del máximo: %s", d)
	}
	def := Config{}.withDefaults()
	if def.MinBackoff != time.Second || def.MaxBackoff != 30*time.Second || def.MaxWait != 10*time.Minute || def.Namespace != "rampa-system" || def.SecretName != "rampa-agent-token" || def.SecretKey != "token" {
		t.Errorf("defaults: %+v", def)
	}
}

func TestTransientSecretReadErrorWithoutBootstrap(t *testing.T) {
	cs := k8sfake.NewClientset()
	cs.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("api server arrancando")
	})
	cfg := testConfig(nil, cs, &logBuffer{})
	cfg.APIURL = "https://api.rampa.dev"
	cfg.MaxWait = 30 * time.Millisecond

	_, err := Resolve(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "no pude leer el Secret rampa-system/rampa-agent-token") {
		t.Fatalf("esperaba error de lectura del Secret, fue %v", err)
	}
}
