package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/protocol"
)

// fakeBackend es un Backend en memoria que aplica las mismas transiciones que
// StoreBackend para el deployment asociado a cada comando.
type fakeBackend struct {
	mu          sync.Mutex
	token       string
	agent       *Agent
	commands    []protocol.Command
	status      map[string]string // commandID -> estado del agent_command
	deployments map[string]string // deploymentID -> estado del deployment
	byCommand   map[string]string // commandID -> deploymentID
	events      []protocol.Event
	results     []protocol.Result
	online      bool
	heartbeats  int
	disconnects []string
	lastHello   protocol.Hello
	// resultFailures es cuántas veces CommandResult falla con un error
	// transitorio antes de aceptar; resultCalls cuenta las llamadas por comando.
	resultFailures int
	resultCalls    map[string]int
}

func newFake() *fakeBackend {
	return &fakeBackend{
		token:       "rampa_cluster_" + strings.Repeat("a", 64),
		agent:       &Agent{ClusterID: "cl-1", ClusterName: "eks-nonprod", OrgID: "org-1", OrgSlug: "acme"},
		status:      map[string]string{},
		deployments: map[string]string{},
		byCommand:   map[string]string{},
		resultCalls: map[string]int{},
	}
}

func (f *fakeBackend) enqueueDeploy(commandID, deploymentID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, protocol.Command{
		ID:   commandID,
		Kind: protocol.CommandDeploy,
		Deploy: &protocol.DeployCommand{
			DeploymentID: deploymentID, App: "checkout-api", Scope: "dev", Namespace: "checkout-api-dev",
			Labels:  map[string]string{"rampa.dev/deployment": deploymentID},
			Objects: []json.RawMessage{json.RawMessage(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"checkout-api"}}`)},
		},
	})
	f.status[commandID] = store.CommandQueued
	f.deployments[deploymentID] = store.DeploymentQueued
	f.byCommand[commandID] = deploymentID
}

func (f *fakeBackend) Authenticate(_ context.Context, token string) (*Agent, error) {
	if token != f.token {
		return nil, ErrUnauthorized
	}
	return f.agent, nil
}

func (f *fakeBackend) Connected(_ context.Context, _ *Agent, hello protocol.Hello) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.online = true
	f.lastHello = hello
	return nil
}

func (f *fakeBackend) Disconnected(_ context.Context, _ *Agent, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.online = false
	f.disconnects = append(f.disconnects, reason)
	return nil
}

func (f *fakeBackend) Heartbeat(_ context.Context, _ *Agent, _ protocol.Heartbeat) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeats++
	return nil
}

func (f *fakeBackend) PendingCommands(_ context.Context, a *Agent, includeSent bool) ([]protocol.Command, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a.ClusterID != f.agent.ClusterID {
		return nil, errors.New("cluster ajeno")
	}
	var out []protocol.Command
	for _, c := range f.commands {
		st := f.status[c.ID]
		if st == store.CommandQueued || (includeSent && st == store.CommandSent) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeBackend) CommandSent(_ context.Context, _ *Agent, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[id] = store.CommandSent
	return nil
}

func (f *fakeBackend) CommandAcked(_ context.Context, _ *Agent, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.status[id]; !ok {
		return store.ErrNotFound
	}
	f.status[id] = store.CommandAcked
	return nil
}

func (f *fakeBackend) CommandEvent(_ context.Context, _ *Agent, ev protocol.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
	if dep, ok := f.byCommand[ev.CommandID]; ok {
		if st := DeploymentStatusForPhase(ev.Phase); st != "" {
			f.deployments[dep] = st
		}
	}
	return nil
}

func (f *fakeBackend) CommandResult(_ context.Context, _ *Agent, res protocol.Result) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resultCalls[res.CommandID]++
	if _, ok := f.status[res.CommandID]; !ok {
		return store.ErrNotFound
	}
	if f.resultFailures > 0 {
		f.resultFailures--
		return errors.New("la base no responde")
	}
	f.results = append(f.results, res)
	f.status[res.CommandID] = CommandStatusForResult(res)
	if dep, ok := f.byCommand[res.CommandID]; ok {
		st, _ := DeploymentStatusForResult(res)
		f.deployments[dep] = st
	}
	return nil
}

func (f *fakeBackend) snapshot() (map[string]string, map[string]string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := map[string]string{}
	for k, v := range f.status {
		st[k] = v
	}
	dp := map[string]string{}
	for k, v := range f.deployments {
		dp[k] = v
	}
	return st, dp, f.online
}

// fakeAgent es el cliente WebSocket de prueba.
type fakeAgent struct {
	t    *testing.T
	conn *websocket.Conn
}

func dialAgent(t *testing.T, srv *httptest.Server, token string) (*fakeAgent, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/agent/connect"
	hdr := http.Header{}
	if token != "" {
		hdr.Set("Authorization", "Bearer "+token)
	}
	c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, resp, err
	}
	return &fakeAgent{t: t, conn: c}, resp, nil
}

func (a *fakeAgent) send(kind protocol.Kind, commandID string, payload any) {
	a.t.Helper()
	env, err := protocol.NewEnvelope(kind, "m", commandID, payload)
	if err != nil {
		a.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, a.conn, env); err != nil {
		a.t.Fatalf("mandando %s: %v", kind, err)
	}
}

func (a *fakeAgent) read() protocol.Envelope {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var env protocol.Envelope
	if err := wsjson.Read(ctx, a.conn, &env); err != nil {
		a.t.Fatalf("leyendo: %v", err)
	}
	return env
}

func (a *fakeAgent) hello() protocol.Welcome {
	a.t.Helper()
	a.send(protocol.KindHello, "", protocol.Hello{Protocol: protocol.Version, AgentVersion: "test", KubernetesVersion: "v1.30.0", Capabilities: []string{"deploy"}})
	env := a.read()
	if env.Kind != protocol.KindWelcome {
		a.t.Fatalf("esperaba welcome, tengo %s", env.Kind)
	}
	var w protocol.Welcome
	if err := json.Unmarshal(env.Payload, &w); err != nil {
		a.t.Fatal(err)
	}
	return w
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout esperando: %s", what)
}

func newServer(t *testing.T, f *fakeBackend, opts Options) (*Hub, *httptest.Server) {
	t.Helper()
	h := New(f, nil, opts)
	mux := http.NewServeMux()
	mux.Handle("/v1/agent/connect", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		h.Shutdown(ctx)
		srv.Close()
	})
	return h, srv
}

func TestRejectsBadToken(t *testing.T) {
	f := newFake()
	_, srv := newServer(t, f, Options{})
	for _, tok := range []string{"", "rampa_notcluster", "rampa_cluster_wrong"} {
		_, resp, err := dialAgent(t, srv, tok)
		if err == nil {
			t.Fatalf("token %q: esperaba rechazo", tok)
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: esperaba 401, tengo %v", tok, resp)
		}
	}
}

func TestDeployFlow(t *testing.T) {
	f := newFake()
	f.enqueueDeploy("cmd-1", "dep-1")
	h, srv := newServer(t, f, Options{})

	agent, _, err := dialAgent(t, srv, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.conn.Close(websocket.StatusNormalClosure, "")

	w := agent.hello()
	if w.ClusterID != "cl-1" || w.OrgSlug != "acme" || w.PendingCommands != 1 || w.HeartbeatSeconds != protocol.DefaultHeartbeatSeconds {
		t.Errorf("welcome inesperado: %+v", w)
	}
	waitFor(t, "cluster online", func() bool { return h.Online("cl-1") })

	// Llega el comando encolado antes de conectar.
	env := agent.read()
	if env.Kind != protocol.KindCommand || env.CommandID != "cmd-1" {
		t.Fatalf("esperaba command cmd-1, tengo %+v", env)
	}
	var cmd protocol.Command
	if err := json.Unmarshal(env.Payload, &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.Kind != protocol.CommandDeploy || cmd.Deploy == nil || cmd.Deploy.DeploymentID != "dep-1" || len(cmd.Deploy.Objects) != 1 {
		t.Fatalf("comando inesperado: %+v", cmd)
	}
	waitFor(t, "comando marcado sent", func() bool { st, _, _ := f.snapshot(); return st["cmd-1"] == store.CommandSent })

	// ack -> acked
	agent.send(protocol.KindAck, "cmd-1", protocol.Ack{CommandID: "cmd-1"})
	waitFor(t, "ack", func() bool { st, _, _ := f.snapshot(); return st["cmd-1"] == store.CommandAcked })

	// event applying -> deployment applying; event progressing -> progressing
	agent.send(protocol.KindEvent, "cmd-1", protocol.Event{CommandID: "cmd-1", Phase: protocol.PhaseApplying, Reason: "Applied", Message: "5 objetos aplicados"})
	waitFor(t, "deployment applying", func() bool { _, dp, _ := f.snapshot(); return dp["dep-1"] == store.DeploymentApplying })
	agent.send(protocol.KindEvent, "cmd-1", protocol.Event{Phase: protocol.PhaseProgressing, Message: "1/1 listos"}) // sin command_id en el payload: se toma del sobre
	waitFor(t, "deployment progressing", func() bool { _, dp, _ := f.snapshot(); return dp["dep-1"] == store.DeploymentProgressing })

	// heartbeat
	agent.send(protocol.KindHeartbeat, "", protocol.Heartbeat{Nodes: 3})
	waitFor(t, "heartbeat", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.heartbeats == 1 })

	// result succeeded -> deployment healthy, comando done
	agent.send(protocol.KindResult, "cmd-1", protocol.Result{CommandID: "cmd-1", Status: protocol.ResultSucceeded, Workload: &protocol.WorkloadStatus{Kind: "Deployment", Phase: protocol.PhaseHealthy, Desired: 1, Ready: 1}})
	waitFor(t, "deployment healthy", func() bool {
		st, dp, _ := f.snapshot()
		return dp["dep-1"] == store.DeploymentHealthy && st["cmd-1"] == store.CommandDone
	})

	// Un comando nuevo mientras está conectado llega con Notify.
	f.enqueueDeploy("cmd-2", "dep-2")
	h.Notify("cl-1")
	env = agent.read()
	if env.Kind != protocol.KindCommand || env.CommandID != "cmd-2" {
		t.Fatalf("esperaba cmd-2, tengo %+v", env)
	}
	// Timeout del agente -> failed con mensaje.
	agent.send(protocol.KindResult, "cmd-2", protocol.Result{CommandID: "cmd-2", Status: protocol.ResultTimeout, Message: "10m sin ready"})
	waitFor(t, "deployment failed", func() bool {
		st, dp, _ := f.snapshot()
		return dp["dep-2"] == store.DeploymentFailed && st["cmd-2"] == store.CommandFailed
	})

	// Cerrar -> offline.
	agent.conn.Close(websocket.StatusNormalClosure, "chau")
	waitFor(t, "offline", func() bool { _, _, online := f.snapshot(); return !online && !h.Online("cl-1") })
}

func TestResendsSentWithoutAckOnReconnect(t *testing.T) {
	f := newFake()
	f.enqueueDeploy("cmd-1", "dep-1")
	_, srv := newServer(t, f, Options{})

	a1, _, err := dialAgent(t, srv, f.token)
	if err != nil {
		t.Fatal(err)
	}
	a1.hello()
	env := a1.read()
	if env.CommandID != "cmd-1" {
		t.Fatalf("esperaba cmd-1, tengo %+v", env)
	}
	waitFor(t, "sent", func() bool { st, _, _ := f.snapshot(); return st["cmd-1"] == store.CommandSent })
	// El agente se cae sin hacer ack.
	a1.conn.Close(websocket.StatusGoingAway, "reinicio")
	waitFor(t, "offline", func() bool { _, _, online := f.snapshot(); return !online })

	a2, _, err := dialAgent(t, srv, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer a2.conn.Close(websocket.StatusNormalClosure, "")
	w := a2.hello()
	if w.PendingCommands != 1 {
		t.Errorf("welcome debería anunciar 1 pendiente, anuncia %d", w.PendingCommands)
	}
	env = a2.read()
	if env.Kind != protocol.KindCommand || env.CommandID != "cmd-1" {
		t.Fatalf("esperaba reenvío de cmd-1, tengo %+v", env)
	}
}

func TestHelloRequired(t *testing.T) {
	f := newFake()
	_, srv := newServer(t, f, Options{HelloTimeout: 200 * time.Millisecond})
	a, _, err := dialAgent(t, srv, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.conn.Close(websocket.StatusNormalClosure, "")
	// Mando un heartbeat en vez de hello.
	a.send(protocol.KindHeartbeat, "", protocol.Heartbeat{})
	env := a.read()
	if env.Kind != protocol.KindError {
		t.Fatalf("esperaba error de protocolo, tengo %+v", env)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var next protocol.Envelope
	if err := wsjson.Read(ctx, a.conn, &next); err == nil {
		t.Fatal("la conexión debería cerrarse tras el error")
	}
	if _, _, online := f.snapshot(); online {
		t.Error("nunca debería haber quedado online")
	}
}

func TestWatchdogMarksOffline(t *testing.T) {
	f := newFake()
	h, srv := newServer(t, f, Options{HeartbeatSeconds: 1, MissedHeartbeats: 1})
	a, _, err := dialAgent(t, srv, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.conn.Close(websocket.StatusNormalClosure, "")
	a.hello()
	waitFor(t, "online", func() bool { return h.Online("cl-1") })
	// Sin heartbeats por más de 1 segundo: offline.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && h.Online("cl-1") {
		time.Sleep(50 * time.Millisecond)
	}
	if h.Online("cl-1") {
		t.Fatal("el watchdog debería haber cerrado la conexión")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.disconnects) == 0 {
		t.Error("Disconnected no fue llamado")
	}
}

func TestTransitions(t *testing.T) {
	if DeploymentStatusForPhase(protocol.PhaseApplying) != store.DeploymentApplying || DeploymentStatusForPhase("raro") != "" {
		t.Error("fase -> estado incorrecto")
	}
	st, msg := DeploymentStatusForResult(protocol.Result{Status: protocol.ResultTimeout, Message: "x"})
	if st != store.DeploymentFailed || !strings.Contains(msg, "timeout") || !strings.Contains(msg, "x") {
		t.Errorf("timeout -> %s %q", st, msg)
	}
	st, _ = DeploymentStatusForResult(protocol.Result{Status: protocol.ResultSucceeded})
	if st != store.DeploymentHealthy {
		t.Errorf("succeeded -> %s", st)
	}
	if ScopeStatusForDeployment(store.DeploymentProgressing) != store.ScopeDeploying || ScopeStatusForDeployment(store.DeploymentHealthy) != store.ScopeHealthy {
		t.Error("estado de scope incorrecto")
	}
}

func TestDisconnectClosesLiveConnection(t *testing.T) {
	f := newFake()
	h, srv := newServer(t, f, Options{})
	if h.Disconnect("cl-1", "nada que cortar") {
		t.Error("sin conexión, Disconnect debería devolver false")
	}
	a, _, err := dialAgent(t, srv, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.conn.Close(websocket.StatusNormalClosure, "")
	a.hello()
	waitFor(t, "online", func() bool { return h.Online("cl-1") })

	// rotate-token o borrar el cluster: la sesión vieja se corta ya, con policy violation.
	if !h.Disconnect("cl-1", "el token del cluster fue rotado") {
		t.Fatal("Disconnect debería encontrar la conexión")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var env protocol.Envelope
	err = wsjson.Read(ctx, a.conn, &env)
	if err == nil {
		t.Fatalf("la conexión debería estar cerrada, leí %+v", env)
	}
	if st := websocket.CloseStatus(err); st != websocket.StatusPolicyViolation {
		t.Errorf("close status = %v (%v), quería PolicyViolation", st, err)
	}
	waitFor(t, "offline", func() bool { _, _, online := f.snapshot(); return !online && !h.Online("cl-1") })
}

func TestHelloInFlightReachesBackend(t *testing.T) {
	f := newFake()
	_, srv := newServer(t, f, Options{})
	a, _, err := dialAgent(t, srv, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.conn.Close(websocket.StatusNormalClosure, "")
	a.send(protocol.KindHello, "", protocol.Hello{Protocol: protocol.Version, AgentVersion: "test", InFlight: []string{"cmd-9", "cmd-10"}})
	if env := a.read(); env.Kind != protocol.KindWelcome {
		t.Fatalf("esperaba welcome, tengo %s", env.Kind)
	}
	// El backend recibe la lista tal cual: es lo que usa para marcar failed
	// los acked que el agente ya no tiene en curso.
	f.mu.Lock()
	got := f.lastHello.InFlight
	f.mu.Unlock()
	if len(got) != 2 || got[0] != "cmd-9" || got[1] != "cmd-10" {
		t.Errorf("InFlight en Connected = %v", got)
	}
}

func TestResultRetriesTransientErrors(t *testing.T) {
	f := newFake()
	f.enqueueDeploy("cmd-1", "dep-1")
	f.resultFailures = 1 // la primera llamada falla, la segunda acepta
	_, srv := newServer(t, f, Options{})
	a, _, err := dialAgent(t, srv, f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer a.conn.Close(websocket.StatusNormalClosure, "")
	a.hello()
	if env := a.read(); env.CommandID != "cmd-1" {
		t.Fatalf("esperaba cmd-1, tengo %+v", env)
	}
	a.send(protocol.KindAck, "cmd-1", protocol.Ack{CommandID: "cmd-1"})
	a.send(protocol.KindResult, "cmd-1", protocol.Result{CommandID: "cmd-1", Status: protocol.ResultSucceeded})
	waitFor(t, "result procesado tras el reintento", func() bool {
		st, dp, _ := f.snapshot()
		return st["cmd-1"] == store.CommandDone && dp["dep-1"] == store.DeploymentHealthy
	})
	f.mu.Lock()
	calls := f.resultCalls["cmd-1"]
	f.mu.Unlock()
	if calls != 2 {
		t.Errorf("CommandResult se llamó %d veces, quería 2 (fallo más reintento)", calls)
	}

	// Un comando ajeno o inexistente (ErrNotFound) es permanente: no se reintenta.
	a.send(protocol.KindResult, "cmd-ajeno", protocol.Result{CommandID: "cmd-ajeno", Status: protocol.ResultSucceeded})
	a.send(protocol.KindHeartbeat, "", protocol.Heartbeat{})
	waitFor(t, "heartbeat posterior", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.heartbeats == 1 })
	f.mu.Lock()
	calls = f.resultCalls["cmd-ajeno"]
	f.mu.Unlock()
	if calls != 1 {
		t.Errorf("un ErrNotFound no debe reintentarse: %d llamadas", calls)
	}
}
