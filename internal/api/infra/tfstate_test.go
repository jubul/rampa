package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/store"
)

// stateEnv arma el servicio, un run con token conocido y un servidor con el backend montado.
type stateEnv struct {
	f     *fixture
	svc   *Service
	ts    *httptest.Server
	run   *store.InfraRun
	token string
}

func newStateEnv(t *testing.T) *stateEnv {
	t.Helper()
	f := newFixture(t)
	svc := New(Config{TofuBin: "sh", StateKey: testKey(7), PublicURL: "http://api.test"}, f.st, f.bus, nil)
	if err := svc.Ready(); err != nil {
		t.Fatal(err)
	}
	token, hash, err := newStateToken()
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.st.CreateInfraRun(context.Background(), &store.InfraRun{
		OrgID: f.orgID, Kind: "cluster", Provider: "aws", Module: "aws/eks-cluster", CloudAccountID: f.acc.ID, Name: "c1", StateTokenHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/v1/tfstate", svc.StateHandler())
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return &stateEnv{f: f, svc: svc, ts: ts, run: run, token: token}
}

func (e *stateEnv) do(method, path, authHeader string, body string) (int, string) {
	e.f.t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if err != nil {
		e.f.t.Fatal(err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestStateBackendAuthAndRoundTrip(t *testing.T) {
	e := newStateEnv(t)
	path := "/v1/tfstate/" + e.run.ID
	bearer := "Bearer " + e.token

	if code, _ := e.do(http.MethodGet, path, "", ""); code != http.StatusUnauthorized {
		t.Errorf("sin token esperaba 401, tengo %d", code)
	}
	if code, _ := e.do(http.MethodGet, path, "Bearer rampa_state_"+strings.Repeat("0", 64), ""); code != http.StatusUnauthorized {
		t.Errorf("token desconocido esperaba 401, tengo %d", code)
	}
	if code, _ := e.do(http.MethodGet, path, "Bearer rampa_abc", ""); code != http.StatusUnauthorized {
		t.Errorf("token de otro tipo esperaba 401, tengo %d", code)
	}
	// El token vale solo para su run: otro id en la ruta es 401.
	if code, _ := e.do(http.MethodGet, "/v1/tfstate/00000000-0000-0000-0000-000000000000", bearer, ""); code != http.StatusUnauthorized {
		t.Errorf("otro run con mi token esperaba 401, tengo %d", code)
	}
	if code, _ := e.do(http.MethodGet, path, bearer, ""); code != http.StatusNotFound {
		t.Errorf("sin estado esperaba 404, tengo %d", code)
	}

	state := `{"version":4,"serial":7,"lineage":"abc","resources":[{"type":"aws_vpc","name":"this"}]}`
	if code, body := e.do(http.MethodPost, path, bearer, state); code != http.StatusOK {
		t.Fatalf("POST: %d %s", code, body)
	}
	code, body := e.do(http.MethodGet, path, bearer, "")
	if code != http.StatusOK || body != state {
		t.Fatalf("GET: %d %q", code, body)
	}
	// Basic auth con el token como contraseña (lo que manda tofu con TF_HTTP_USERNAME/PASSWORD).
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+path, nil)
	req.SetBasicAuth("rampa", e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Basic auth: %d", resp.StatusCode)
	}

	// La fila en la base está cifrada: no contiene el texto plano.
	row, err := e.f.st.GetInfraState(context.Background(), e.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(row.Ciphertext, []byte("aws_vpc")) || bytes.Contains(row.Ciphertext, []byte("lineage")) || len(row.Ciphertext) <= len(state) {
		t.Errorf("el estado guardado parece texto plano: %q", row.Ciphertext)
	}
	plain, err := e.svc.cipher.Open(row.Ciphertext, []byte(e.run.ID))
	if err != nil || string(plain) != state {
		t.Errorf("descifrado: %v %q", err, plain)
	}

	if code, _ := e.do(http.MethodDelete, path, bearer, ""); code != http.StatusOK {
		t.Errorf("DELETE: %d", code)
	}
	if code, _ := e.do(http.MethodGet, path, bearer, ""); code != http.StatusNotFound {
		t.Errorf("tras DELETE esperaba 404, tengo %d", code)
	}
	if code, _ := e.do(http.MethodPost, path, bearer, "   "); code != http.StatusBadRequest {
		t.Errorf("POST vacío esperaba 400, tengo %d", code)
	}
}

func TestStateBackendLocking(t *testing.T) {
	e := newStateEnv(t)
	path := "/v1/tfstate/" + e.run.ID
	bearer := "Bearer " + e.token
	lockA := `{"ID":"lock-a","Operation":"OperationTypeApply","Who":"juan@laptop"}`
	lockB := `{"ID":"lock-b","Operation":"OperationTypePlan","Who":"ci"}`

	if code, _ := e.do(MethodLock, path, "", lockA); code != http.StatusUnauthorized {
		t.Errorf("LOCK sin token: %d", code)
	}
	if code, _ := e.do(MethodLock, path, bearer, `{"no":"id"}`); code != http.StatusBadRequest {
		t.Errorf("LOCK sin ID: %d", code)
	}
	if code, body := e.do(MethodLock, path, bearer, lockA); code != http.StatusOK || !strings.Contains(body, "lock-a") {
		t.Fatalf("LOCK a: %d %s", code, body)
	}
	// Mismo dueño repite: idempotente.
	if code, _ := e.do(MethodLock, path, bearer, lockA); code != http.StatusOK {
		t.Errorf("LOCK repetido del mismo ID: %d", code)
	}
	// Otro dueño: 423 con el lock vigente en el cuerpo.
	code, body := e.do(MethodLock, path, bearer, lockB)
	if code != http.StatusLocked {
		t.Fatalf("LOCK b con a tomado esperaba 423, tengo %d %s", code, body)
	}
	var held lockInfo
	if err := json.Unmarshal([]byte(body), &held); err != nil || held.ID != "lock-a" || held.Who != "juan@laptop" {
		t.Errorf("cuerpo del 423: %s", body)
	}
	// POST con ID incorrecto o sin ID: 409. Con el ID correcto: 200.
	state := `{"version":4,"serial":1}`
	if code, _ := e.do(http.MethodPost, path+"?ID=lock-b", bearer, state); code != http.StatusConflict {
		t.Errorf("POST con otro ID esperaba 409, tengo %d", code)
	}
	if code, _ := e.do(http.MethodPost, path, bearer, state); code != http.StatusConflict {
		t.Errorf("POST sin ID con lock tomado esperaba 409, tengo %d", code)
	}
	if code, _ := e.do(http.MethodPost, path+"?ID=lock-a", bearer, state); code != http.StatusOK {
		t.Errorf("POST con el ID del lock esperaba 200, tengo %d", code)
	}
	// UNLOCK con otro ID: 409. Con el correcto: 200, y después LOCK b anda.
	if code, _ := e.do(MethodUnlock, path, bearer, `{"ID":"lock-b"}`); code != http.StatusConflict {
		t.Errorf("UNLOCK con otro ID esperaba 409, tengo %d", code)
	}
	if code, _ := e.do(MethodUnlock, path, bearer, `{"ID":"lock-a"}`); code != http.StatusOK {
		t.Errorf("UNLOCK: %d", code)
	}
	if code, _ := e.do(MethodUnlock, path, bearer, `{"ID":"lock-a"}`); code != http.StatusOK {
		t.Errorf("UNLOCK repetido debería ser idempotente: %d", code)
	}
	if code, _ := e.do(MethodLock, path, bearer, lockB); code != http.StatusOK {
		t.Errorf("LOCK b tras liberar: %d", code)
	}
	// El estado guardado sigue ahí después de los locks.
	if code, body := e.do(http.MethodGet, path, bearer, ""); code != http.StatusOK || body != state {
		t.Errorf("GET tras locks: %d %s", code, body)
	}
	// Un token de otro run no ve este estado aunque adivine el id.
	otherToken, otherHash, _ := newStateToken()
	if _, err := e.f.st.CreateInfraRun(context.Background(), &store.InfraRun{OrgID: e.f.orgID, Kind: "cluster", Provider: "aws", Module: "aws/eks-cluster", CloudAccountID: e.f.acc.ID, Name: "c2", StateTokenHash: otherHash}); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.do(http.MethodGet, path, "Bearer "+otherToken, ""); code != http.StatusUnauthorized {
		t.Errorf("token de otro run esperaba 401, tengo %d", code)
	}
	_ = auth.HashToken
}
