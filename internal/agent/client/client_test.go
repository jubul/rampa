package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"rampa.dev/pkg/protocol"
)

const testToken = "rampa_cluster_test"

// fakeAPI es un servidor WebSocket que hace de plano de control.
type fakeAPI struct {
	t        *testing.T
	srv      *httptest.Server
	conns    atomic.Int32
	hellos   chan protocol.Hello
	messages chan protocol.Envelope // todo lo que manda el agente después del hello
	// onSession corre por cada conexión aceptada, después del welcome, con el
	// número de conexión (desde 1). Puede mandar comandos y cerrar.
	onSession func(n int, conn *websocket.Conn)
	badAuth   atomic.Int32
	connMu    sync.Mutex
	lastConn  *websocket.Conn
}

func newFakeAPI(t *testing.T, onSession func(n int, conn *websocket.Conn)) *fakeAPI {
	t.Helper()
	a := &fakeAPI{
		t:         t,
		hellos:    make(chan protocol.Hello, 16),
		messages:  make(chan protocol.Envelope, 256),
		onSession: onSession,
	}
	a.srv = httptest.NewServer(http.HandlerFunc(a.handle))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != ConnectPath {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		a.badAuth.Add(1)
		http.Error(w, `{"error":{"code":"unauthorized"}}`, http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(readLimit)
	n := int(a.conns.Add(1))

	ctx := r.Context()
	var env protocol.Envelope
	if err := wsjson.Read(ctx, conn, &env); err != nil || env.Kind != protocol.KindHello {
		a.t.Errorf("conexión %d: esperaba hello, err=%v kind=%q", n, err, env.Kind)
		return
	}
	var hello protocol.Hello
	if err := json.Unmarshal(env.Payload, &hello); err != nil {
		a.t.Errorf("hello inválido: %v", err)
		return
	}
	a.hellos <- hello
	a.connMu.Lock()
	a.lastConn = conn
	a.connMu.Unlock()

	welcome, _ := protocol.NewEnvelope(protocol.KindWelcome, "w1", "", protocol.Welcome{
		ClusterID: "c1", ClusterName: "eks-test", OrgSlug: "acme", HeartbeatSeconds: 1, ServerTime: time.Now().UTC().Format(time.RFC3339),
	})
	if err := wsjson.Write(ctx, conn, welcome); err != nil {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var msg protocol.Envelope
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				return
			}
			a.messages <- msg
		}
	}()
	if a.onSession != nil {
		a.onSession(n, conn)
	}
	<-done
}

func (a *fakeAPI) sendCommand(conn *websocket.Conn, cmd protocol.Command) {
	env, err := protocol.NewEnvelope(protocol.KindCommand, "m-"+cmd.ID, cmd.ID, cmd)
	if err != nil {
		a.t.Fatal(err)
	}
	if err := wsjson.Write(context.Background(), conn, env); err != nil {
		a.t.Logf("no pude mandar el comando: %v", err)
	}
}

// next devuelve el próximo mensaje del agente que no sea heartbeat.
func (a *fakeAPI) next(timeout time.Duration) (protocol.Envelope, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case m := <-a.messages:
			if m.Kind == protocol.KindHeartbeat {
				continue
			}
			return m, true
		case <-deadline:
			return protocol.Envelope{}, false
		}
	}
}

// fakeExecutor ejecuta comandos según una función inyectada.
type fakeExecutor struct {
	fn func(ctx context.Context, cmd protocol.Command, sink Sink) protocol.Result
}

func (f *fakeExecutor) Execute(ctx context.Context, cmd protocol.Command, sink Sink) protocol.Result {
	return f.fn(ctx, cmd, sink)
}

type fakeCluster struct{}

func (fakeCluster) KubernetesVersion(context.Context) (string, error) { return "v1.31.2", nil }
func (fakeCluster) NodeCount(context.Context) (int, error)            { return 3, nil }

func newTestClient(t *testing.T, api *fakeAPI, exec Executor) *Client {
	t.Helper()
	c, err := New(Config{
		APIURL:       api.srv.URL,
		Token:        testToken,
		AgentVersion: "test",
		Cluster:      fakeCluster{},
		Executor:     exec,
		Logger:       slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug})),
		MinBackoff:   10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		// Corto para que un test con comandos colgados no demore el cleanup.
		ShutdownGrace: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// waitFor espera a que cond sea true o venza el plazo.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitResult devuelve el result del comando o falla.
func (a *fakeAPI) waitResult(t *testing.T, commandID string, timeout time.Duration) protocol.Result {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m := <-a.messages:
			if m.Kind == protocol.KindResult && m.CommandID == commandID {
				var r protocol.Result
				if err := json.Unmarshal(m.Payload, &r); err != nil {
					t.Fatal(err)
				}
				return r
			}
		case <-deadline:
			t.Fatalf("no llegó el result de %s", commandID)
		}
	}
}

// startClient corre Run en segundo plano y espera a que termine al limpiar el
// test, para que ningún log llegue después de que el test cerró.
func startClient(t *testing.T, c *Client) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run no terminó al cancelar")
		}
	})
	return cancel
}

func deployCommand(id string) protocol.Command {
	return protocol.Command{
		ID:   id,
		Kind: protocol.CommandDeploy,
		Deploy: &protocol.DeployCommand{
			DeploymentID: "dep-" + id, App: "checkout-api", Scope: "dev", Namespace: "checkout-api-dev",
			Labels:  map[string]string{"rampa.dev/app": "checkout-api"},
			Objects: []json.RawMessage{[]byte(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"checkout-api"}}`)},
		},
	}
}

func TestHelloWelcomeAndDeployFlow(t *testing.T) {
	var api *fakeAPI
	api = newFakeAPI(t, func(n int, conn *websocket.Conn) {
		api.sendCommand(conn, deployCommand("cmd-1"))
	})
	exec := &fakeExecutor{fn: func(ctx context.Context, cmd protocol.Command, sink Sink) protocol.Result {
		if cmd.Kind != protocol.CommandDeploy || cmd.Deploy == nil || cmd.Deploy.App != "checkout-api" {
			t.Errorf("comando inesperado: %+v", cmd)
		}
		_ = sink.Event(protocol.Event{Phase: protocol.PhaseApplying, Reason: "Applied", Object: "Service/checkout-api", Message: "aplicado"})
		_ = sink.Event(protocol.Event{Phase: protocol.PhaseProgressing, Reason: "Progressing", Message: "1/1"})
		return protocol.Result{Status: protocol.ResultSucceeded, Message: "listo", Workload: &protocol.WorkloadStatus{Kind: "Deployment", Phase: protocol.PhaseHealthy, Ready: 1, Desired: 1}}
	}}
	c := newTestClient(t, api, exec)
	cancel := startClient(t, c)

	select {
	case hello := <-api.hellos:
		if hello.Protocol != protocol.Version || hello.AgentVersion != "test" || hello.KubernetesVersion != "v1.31.2" {
			t.Errorf("hello: %+v", hello)
		}
		if strings.Join(hello.Capabilities, ",") != "deploy,undeploy,status,logs" {
			t.Errorf("capabilities: %v", hello.Capabilities)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no llegó el hello")
	}

	var kinds []protocol.Kind
	var result protocol.Result
	for len(kinds) < 4 {
		m, ok := api.next(5 * time.Second)
		if !ok {
			t.Fatalf("faltaron mensajes; llegaron %v", kinds)
		}
		if m.CommandID != "cmd-1" {
			t.Errorf("mensaje %s con command_id %q", m.Kind, m.CommandID)
		}
		kinds = append(kinds, m.Kind)
		switch m.Kind {
		case protocol.KindAck:
			var ack protocol.Ack
			if err := json.Unmarshal(m.Payload, &ack); err != nil || ack.CommandID != "cmd-1" {
				t.Errorf("ack: %s err=%v", m.Payload, err)
			}
		case protocol.KindEvent:
			var ev protocol.Event
			if err := json.Unmarshal(m.Payload, &ev); err != nil || ev.CommandID != "cmd-1" || ev.Time.IsZero() {
				t.Errorf("event: %s err=%v", m.Payload, err)
			}
		case protocol.KindResult:
			if err := json.Unmarshal(m.Payload, &result); err != nil {
				t.Errorf("result: %v", err)
			}
		}
	}
	want := []protocol.Kind{protocol.KindAck, protocol.KindEvent, protocol.KindEvent, protocol.KindResult}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("orden de mensajes %v, esperaba %v", kinds, want)
		}
	}
	if result.Status != protocol.ResultSucceeded || result.Workload == nil || result.Workload.Ready != 1 {
		t.Errorf("result: %+v", result)
	}
	if !c.Connected() {
		t.Error("Connected() debería ser true con la sesión abierta")
	}
	if w := c.Welcome(); w == nil || w.ClusterName != "eks-test" {
		t.Errorf("welcome guardado: %+v", w)
	}

	// Un heartbeat cada segundo según el welcome.
	deadline := time.After(4 * time.Second)
	gotHeartbeat := false
	for !gotHeartbeat {
		select {
		case m := <-api.messages:
			if m.Kind == protocol.KindHeartbeat {
				var hb protocol.Heartbeat
				if err := json.Unmarshal(m.Payload, &hb); err != nil || hb.Nodes != 3 {
					t.Errorf("heartbeat: %s", m.Payload)
				}
				gotHeartbeat = true
			}
		case <-deadline:
			t.Fatal("no llegó ningún heartbeat")
		}
	}

	cancel()
	deadline = time.After(5 * time.Second)
	for c.Connected() {
		select {
		case <-deadline:
			t.Fatal("Connected() debería ser false después de cerrar")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestReconnectsWhenServerCloses(t *testing.T) {
	api := newFakeAPI(t, func(n int, conn *websocket.Conn) {
		if n == 1 {
			// La API se cae después del welcome.
			_ = conn.Close(websocket.StatusGoingAway, "reinicio")
		}
	})
	c := newTestClient(t, api, &fakeExecutor{fn: func(context.Context, protocol.Command, Sink) protocol.Result {
		return protocol.Result{Status: protocol.ResultSucceeded}
	}})
	connects := make(chan struct{}, 8)
	c.onConnect = func() { connects <- struct{}{} }
	startClient(t, c)

	for i := 0; i < 2; i++ {
		select {
		case <-connects:
		case <-time.After(5 * time.Second):
			t.Fatalf("esperaba la conexión %d", i+1)
		}
	}
	if got := api.conns.Load(); got < 2 {
		t.Fatalf("esperaba al menos 2 conexiones, hubo %d", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !c.Connected() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !c.Connected() {
		t.Error("debería estar conectado después de reconectar")
	}
}

func TestKeepsRetryingOnUnauthorized(t *testing.T) {
	api := newFakeAPI(t, nil)
	c, err := New(Config{
		APIURL: api.srv.URL, Token: "token-malo", Cluster: fakeCluster{},
		Executor:   &fakeExecutor{fn: func(context.Context, protocol.Command, Sink) protocol.Result { return protocol.Result{} }},
		Logger:     slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		MinBackoff: 5 * time.Millisecond, MaxBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if api.badAuth.Load() < 3 {
		t.Errorf("esperaba varios reintentos con 401, hubo %d", api.badAuth.Load())
	}
	if c.Connected() {
		t.Error("nunca debería figurar conectado")
	}
}

func TestRejectsUnknownOrIncompleteCommands(t *testing.T) {
	var api *fakeAPI
	api = newFakeAPI(t, func(n int, conn *websocket.Conn) {
		api.sendCommand(conn, protocol.Command{ID: "prov-1", Kind: "provision"})
		api.sendCommand(conn, protocol.Command{ID: "dep-sin-ns", Kind: protocol.CommandDeploy, Deploy: &protocol.DeployCommand{App: "x"}})
	})
	executed := atomic.Int32{}
	c := newTestClient(t, api, &fakeExecutor{fn: func(context.Context, protocol.Command, Sink) protocol.Result {
		executed.Add(1)
		return protocol.Result{Status: protocol.ResultSucceeded}
	}})
	startClient(t, c)

	results := map[string]protocol.Result{}
	acks := 0
	for len(results) < 2 {
		m, ok := api.next(5 * time.Second)
		if !ok {
			t.Fatalf("faltaron mensajes: results=%v acks=%d", results, acks)
		}
		switch m.Kind {
		case protocol.KindAck:
			acks++
		case protocol.KindResult:
			var r protocol.Result
			if err := json.Unmarshal(m.Payload, &r); err != nil {
				t.Fatal(err)
			}
			results[m.CommandID] = r
		}
	}
	if acks != 2 {
		t.Errorf("esperaba 2 acks, hubo %d", acks)
	}
	for id, r := range results {
		if r.Status != protocol.ResultRejected || r.CommandID != id {
			t.Errorf("%s: %+v", id, r)
		}
	}
	if !strings.Contains(results["prov-1"].Message, "provision") {
		t.Errorf("mensaje de rechazo: %q", results["prov-1"].Message)
	}
	if executed.Load() != 0 {
		t.Error("el ejecutor no debería haber corrido")
	}
}

func TestSerializesPerNamespaceAndParallelAcross(t *testing.T) {
	var api *fakeAPI
	api = newFakeAPI(t, func(n int, conn *websocket.Conn) {
		a := deployCommand("a1")
		b := deployCommand("a2") // mismo namespace que a1
		other := deployCommand("b1")
		other.Deploy.Namespace = "otra-app-dev"
		api.sendCommand(conn, a)
		api.sendCommand(conn, b)
		api.sendCommand(conn, other)
	})
	var mu sync.Mutex
	running := map[string]int{}
	maxRunning := map[string]int{}
	c := newTestClient(t, api, &fakeExecutor{fn: func(ctx context.Context, cmd protocol.Command, sink Sink) protocol.Result {
		ns := cmd.Deploy.Namespace
		mu.Lock()
		running[ns]++
		if running[ns] > maxRunning[ns] {
			maxRunning[ns] = running[ns]
		}
		total := 0
		for _, v := range running {
			total += v
		}
		if total > maxRunning["total"] {
			maxRunning["total"] = total
		}
		mu.Unlock()
		time.Sleep(60 * time.Millisecond)
		mu.Lock()
		running[ns]--
		mu.Unlock()
		return protocol.Result{Status: protocol.ResultSucceeded}
	}})
	startClient(t, c)

	results := 0
	for results < 3 {
		m, ok := api.next(5 * time.Second)
		if !ok {
			t.Fatalf("faltaron results, llegaron %d", results)
		}
		if m.Kind == protocol.KindResult {
			results++
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if maxRunning["checkout-api-dev"] != 1 {
		t.Errorf("dos deploys del mismo namespace corrieron a la vez: %v", maxRunning)
	}
	if maxRunning["total"] < 2 {
		t.Errorf("namespaces distintos deberían correr en paralelo: %v", maxRunning)
	}
}

func TestResultDeliveredAfterReconnect(t *testing.T) {
	release := make(chan struct{})
	var api *fakeAPI
	api = newFakeAPI(t, func(n int, conn *websocket.Conn) {
		if n == 1 {
			api.sendCommand(conn, deployCommand("lento"))
			// Esperamos el ack y cortamos la conexión con el deploy a mitad de camino.
			time.Sleep(50 * time.Millisecond)
			_ = conn.Close(websocket.StatusGoingAway, "deploy de la API")
		}
	})
	c := newTestClient(t, api, &fakeExecutor{fn: func(ctx context.Context, cmd protocol.Command, sink Sink) protocol.Result {
		<-release
		if ctx.Err() != nil {
			t.Error("el contexto de ejecución no debería cancelarse al caer la sesión")
		}
		return protocol.Result{Status: protocol.ResultSucceeded, Message: "sobreviví a la reconexión"}
	}})
	connects := make(chan struct{}, 8)
	c.onConnect = func() { connects <- struct{}{} }
	startClient(t, c)

	for i := 0; i < 2; i++ {
		select {
		case <-connects:
		case <-time.After(5 * time.Second):
			t.Fatalf("esperaba la conexión %d", i+1)
		}
	}
	close(release)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-api.messages:
			if m.Kind == protocol.KindResult && m.CommandID == "lento" {
				var r protocol.Result
				_ = json.Unmarshal(m.Payload, &r)
				if r.Status != protocol.ResultSucceeded {
					t.Errorf("result: %+v", r)
				}
				return
			}
		case <-deadline:
			t.Fatal("el result no llegó por la segunda conexión")
		}
	}
}

func TestShutdownDeliversInflightResultBeforeClosing(t *testing.T) {
	release := make(chan struct{})
	var api *fakeAPI
	api = newFakeAPI(t, func(n int, conn *websocket.Conn) {
		if n == 1 {
			api.sendCommand(conn, deployCommand("en-curso"))
		}
	})
	var execCtxErr atomic.Pointer[error]
	c := newTestClient(t, api, &fakeExecutor{fn: func(ctx context.Context, cmd protocol.Command, sink Sink) protocol.Result {
		<-release
		err := ctx.Err()
		execCtxErr.Store(&err)
		return protocol.Result{Status: protocol.ResultSucceeded, Message: "terminé durante el apagado"}
	}})
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { _ = c.Run(ctx); close(runDone) }()

	// Ack del comando: ya está en curso.
	m, ok := api.next(5 * time.Second)
	if !ok || m.Kind != protocol.KindAck || m.CommandID != "en-curso" {
		t.Fatalf("esperaba el ack, llegó %+v", m)
	}

	// Llega la señal. La sesión tiene que seguir abierta mientras el comando corre.
	cancel()
	time.Sleep(150 * time.Millisecond)
	if !c.Connected() {
		t.Fatal("la sesión no debe cerrarse mientras haya comandos en curso")
	}
	select {
	case <-runDone:
		t.Fatal("Run no debe devolver con comandos en curso")
	default:
	}
	if got := c.InFlight(); len(got) != 1 || got[0] != "en-curso" {
		t.Errorf("InFlight: %v", got)
	}

	// Un comando nuevo durante el apagado no se acepta: sin ack y sin ejecutar.
	api.sendCommand(mustConn(t, api), deployCommand("tarde"))
	time.Sleep(100 * time.Millisecond)
	select {
	case m := <-api.messages:
		if m.Kind != protocol.KindHeartbeat {
			t.Fatalf("no debería llegar nada por un comando durante el apagado, llegó %s %s", m.Kind, m.CommandID)
		}
	default:
	}

	close(release)
	r := api.waitResult(t, "en-curso", 5*time.Second)
	if r.Status != protocol.ResultSucceeded || r.Message != "terminé durante el apagado" {
		t.Errorf("result: %+v", r)
	}
	if err := execCtxErr.Load(); err == nil || *err != nil {
		t.Errorf("el contexto del ejecutor no debe cancelarse con la señal dentro del grace: %v", err)
	}
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Run no terminó después de entregar el result")
	}
	if c.Connected() {
		t.Error("la sesión debería estar cerrada al terminar Run")
	}
	if api.conns.Load() != 1 {
		t.Errorf("no debería haber reconectado, hubo %d conexiones", api.conns.Load())
	}
}

// mustConn devuelve la conexión del lado servidor de la última sesión aceptada.
func mustConn(t *testing.T, api *fakeAPI) *websocket.Conn {
	t.Helper()
	api.connMu.Lock()
	defer api.connMu.Unlock()
	if api.lastConn == nil {
		t.Fatal("no hay conexión aceptada")
	}
	return api.lastConn
}

func TestShutdownFailsInflightAfterGrace(t *testing.T) {
	var api *fakeAPI
	api = newFakeAPI(t, func(n int, conn *websocket.Conn) {
		if n == 1 {
			api.sendCommand(conn, deployCommand("colgado"))
		}
	})
	execDone := make(chan struct{})
	c := newTestClient(t, api, &fakeExecutor{fn: func(ctx context.Context, cmd protocol.Command, sink Sink) protocol.Result {
		defer close(execDone)
		// Solo termina cuando el agente cancela el contexto al vencer el grace.
		<-ctx.Done()
		return protocol.Result{Status: protocol.ResultSucceeded, Message: "tarde"}
	}})
	c.cfg.ShutdownGrace = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { _ = c.Run(ctx); close(runDone) }()

	if m, ok := api.next(5 * time.Second); !ok || m.Kind != protocol.KindAck {
		t.Fatalf("esperaba el ack, llegó %+v", m)
	}
	start := time.Now()
	cancel()

	r := api.waitResult(t, "colgado", 5*time.Second)
	if r.Status != protocol.ResultFailed || r.Message != ShutdownMessage {
		t.Errorf("result al vencer el grace: %+v", r)
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Error("el result forzado llegó antes de que venciera el grace")
	}
	select {
	case <-execDone:
	case <-time.After(5 * time.Second):
		t.Fatal("el ejecutor debería haberse cancelado al vencer el grace")
	}
	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Run no terminó")
	}
	// El result tardío del ejecutor se descarta: un comando termina una sola vez.
	for {
		select {
		case m := <-api.messages:
			if m.Kind == protocol.KindResult {
				t.Fatalf("no debería haber un segundo result: %s", m.Payload)
			}
			continue
		default:
		}
		break
	}
}

func TestHelloReportsInflightCommandsOnReconnect(t *testing.T) {
	release := make(chan struct{})
	var api *fakeAPI
	api = newFakeAPI(t, func(n int, conn *websocket.Conn) {
		if n == 1 {
			api.sendCommand(conn, deployCommand("largo"))
			time.Sleep(50 * time.Millisecond)
			_ = conn.Close(websocket.StatusGoingAway, "reinicio de la API")
		}
	})
	c := newTestClient(t, api, &fakeExecutor{fn: func(ctx context.Context, cmd protocol.Command, sink Sink) protocol.Result {
		<-release
		return protocol.Result{Status: protocol.ResultSucceeded}
	}})
	startClient(t, c)

	first := <-api.hellos
	if len(first.InFlight) != 0 {
		t.Errorf("el primer hello no debería traer comandos en curso: %v", first.InFlight)
	}
	select {
	case second := <-api.hellos:
		if len(second.InFlight) != 1 || second.InFlight[0] != "largo" {
			t.Errorf("el hello de la reconexión debería traer in_flight=[largo]: %v", second.InFlight)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no hubo reconexión")
	}
	close(release)
	api.waitResult(t, "largo", 5*time.Second)
}

func TestRepeatedFinishedCommandResendsResultWithoutExecuting(t *testing.T) {
	var api *fakeAPI
	api = newFakeAPI(t, func(n int, conn *websocket.Conn) {
		api.sendCommand(conn, deployCommand("dup"))
	})
	var executed atomic.Int32
	c := newTestClient(t, api, &fakeExecutor{fn: func(ctx context.Context, cmd protocol.Command, sink Sink) protocol.Result {
		executed.Add(1)
		return protocol.Result{Status: protocol.ResultSucceeded, Message: "primera y única ejecución"}
	}})
	startClient(t, c)

	first := api.waitResult(t, "dup", 5*time.Second)
	// La API perdió el ack o el result y reenvía el mismo comando.
	api.sendCommand(mustConn(t, api), deployCommand("dup"))
	second := api.waitResult(t, "dup", 5*time.Second)
	if second.Status != first.Status || second.Message != first.Message {
		t.Errorf("el result reenviado debería ser el mismo: %+v vs %+v", first, second)
	}
	if executed.Load() != 1 {
		t.Errorf("el comando se ejecutó %d veces", executed.Load())
	}
}

func TestDoneCommandsAreBounded(t *testing.T) {
	c := &Client{done: map[string]doneCommand{}, log: slog.New(slog.NewTextHandler(testWriter{t}, nil))}
	for i := 0; i < maxDoneCommands+10; i++ {
		env := protocol.Envelope{Kind: protocol.KindResult, CommandID: fmt.Sprint("cmd-", i)}
		if !c.markDone(env.CommandID, env) {
			t.Fatalf("cmd-%d ya figuraba terminado", i)
		}
	}
	if len(c.done) != maxDoneCommands || len(c.doneOrder) != maxDoneCommands {
		t.Errorf("registro de terminados: %d entradas, esperaba %d", len(c.done), maxDoneCommands)
	}
	if _, ok := c.finished("cmd-0"); ok {
		t.Error("el más viejo debería haberse podado")
	}
	if _, ok := c.finished(fmt.Sprint("cmd-", maxDoneCommands+9)); !ok {
		t.Error("el más nuevo debería seguir")
	}
	if c.markDone("cmd-500", protocol.Envelope{}) {
		t.Error("un id ya terminado no debe registrarse dos veces")
	}
	// Las entradas vencidas por TTL se podan y dejan de responder.
	c.done["cmd-600"] = doneCommand{result: protocol.Envelope{}, at: time.Now().Add(-2 * doneCommandTTL)}
	if _, ok := c.finished("cmd-600"); ok {
		t.Error("una entrada vencida no debe usarse")
	}
}

func TestWriteOrEnqueueDecidesUnderWriteMutex(t *testing.T) {
	c := &Client{log: slog.New(slog.NewTextHandler(testWriter{t}, nil))}
	env := protocol.Envelope{Kind: protocol.KindResult, CommandID: "r1"}
	// Mientras setConn (que toma writeMu) está en curso, finish no puede decidir.
	c.writeMu.Lock()
	enqueued := make(chan struct{})
	go func() {
		c.writeOrEnqueue(env)
		close(enqueued)
	}()
	select {
	case <-enqueued:
		t.Fatal("writeOrEnqueue no debería decidir sin writeMu")
	case <-time.After(50 * time.Millisecond):
	}
	c.writeMu.Unlock()
	select {
	case <-enqueued:
	case <-time.After(time.Second):
		t.Fatal("writeOrEnqueue quedó trabado")
	}
	if c.outboxLen() != 1 || c.outbox[0].CommandID != "r1" {
		t.Errorf("outbox: %+v", c.outbox)
	}
}

func TestBackoffStaysWithinBounds(t *testing.T) {
	c := &Client{cfg: Config{MinBackoff: time.Second, MaxBackoff: 60 * time.Second}}
	prevMax := time.Duration(0)
	for attempt := 0; attempt < 40; attempt++ {
		for i := 0; i < 50; i++ {
			d := c.backoff(attempt)
			if d < time.Second || d > 60*time.Second {
				t.Fatalf("intento %d: %s fuera de [1s, 60s]", attempt, d)
			}
		}
		if attempt == 6 {
			prevMax = c.backoff(attempt)
		}
	}
	// Con 6 duplicaciones el intervalo base es 64s, o sea ya está en el techo.
	if prevMax < 30*time.Second {
		t.Errorf("después de 6 intentos el backoff debería estar cerca del techo, fue %s", prevMax)
	}
	if d := c.backoff(0); d != time.Second {
		t.Errorf("primer reintento debería ser 1s, fue %s", d)
	}
}

func TestConnectURL(t *testing.T) {
	cases := map[string]string{
		"https://api.rampa.dev":        "wss://api.rampa.dev/v1/agent/connect",
		"http://localhost:8080/":       "ws://localhost:8080/v1/agent/connect",
		"wss://api.rampa.dev/base":     "wss://api.rampa.dev/base/v1/agent/connect",
		"http://localhost:8080?x=1#f":  "ws://localhost:8080/v1/agent/connect",
		"https://api.rampa.dev/v1/../": "wss://api.rampa.dev/v1/../v1/agent/connect",
	}
	for in, want := range cases {
		got, err := connectURL(in)
		if err != nil || got != want {
			t.Errorf("%s: got %q err=%v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://x", "localhost:8080", "https://"} {
		if _, err := connectURL(bad); err == nil {
			t.Errorf("%q debería ser inválida", bad)
		}
	}
}

func TestNewValidatesConfig(t *testing.T) {
	exec := &fakeExecutor{}
	if _, err := New(Config{Token: "t", Executor: exec, Cluster: fakeCluster{}}); err == nil {
		t.Error("sin APIURL debería fallar")
	}
	if _, err := New(Config{APIURL: "http://x", Executor: exec, Cluster: fakeCluster{}}); err == nil {
		t.Error("sin token debería fallar")
	}
	if _, err := New(Config{APIURL: "http://x", Token: "t", Cluster: fakeCluster{}}); err == nil {
		t.Error("sin executor debería fallar")
	}
	c, err := New(Config{APIURL: "http://x", Token: "t", Executor: exec, Cluster: fakeCluster{}})
	if err != nil {
		t.Fatal(err)
	}
	if c.cfg.MinBackoff != time.Second || c.cfg.MaxBackoff != 60*time.Second || len(c.cfg.Capabilities) != 4 {
		t.Errorf("defaults: %+v", c.cfg)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
