// Package client implementa el lado agente del protocolo de pkg/protocol:
// conexión WebSocket saliente a la API, hello/welcome, heartbeat, recepción de
// comandos con ack inmediato, ejecución concurrente (un comando a la vez por
// namespace), reconexión con backoff exponencial para siempre y apagado con
// grace que entrega los results de los comandos en curso antes de cerrar.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"

	"rampa.dev/pkg/protocol"
)

const (
	// ConnectPath es la ruta del endpoint de agentes en la API.
	ConnectPath = "/v1/agent/connect"

	defaultMinBackoff  = time.Second
	defaultMaxBackoff  = 60 * time.Second
	defaultDialTimeout = 20 * time.Second
	welcomeTimeout     = 30 * time.Second
	writeTimeout       = 15 * time.Second
	pingTimeout        = 10 * time.Second
	// readLimit es el tamaño máximo de un mensaje entrante. Un deploy trae
	// todos los objetos renderizados, así que es generoso.
	readLimit = 64 << 20
	// maxOutbox es cuántos results guardamos si no hay conexión para entregarlos.
	maxOutbox = 1000
	// DefaultShutdownGrace es cuánto se espera a los comandos en curso al apagar
	// si la configuración no dice otra cosa (RAMPA_SHUTDOWN_GRACE).
	DefaultShutdownGrace = 30 * time.Second
	// Cota del registro de comandos terminados para responder repetidos sin
	// volver a ejecutarlos: cantidad y antigüedad máxima.
	maxDoneCommands = 1000
	doneCommandTTL  = time.Hour
	// shutdownExtra es el margen después del grace para que los ejecutores
	// cancelados terminen y sus results forzados salgan por la conexión.
	shutdownExtra = 5 * time.Second
	// ShutdownMessage es el mensaje del result failed que reciben los comandos
	// que seguían en curso cuando venció el grace del apagado.
	ShutdownMessage = "el agente se apagó durante el comando"
)

// DefaultCapabilities son los comandos que el agente sabe ejecutar. Van en el
// hello para que la API sepa qué puede mandar (write_secret requiere un agente
// que lo anuncie).
var DefaultCapabilities = []string{
	string(protocol.CommandDeploy),
	string(protocol.CommandUndeploy),
	string(protocol.CommandStatus),
	string(protocol.CommandLogs),
	string(protocol.CommandWriteSecret),
}

// ErrNotConnected se devuelve al intentar mandar sin conexión activa.
var ErrNotConnected = errors.New("sin conexión con la API")

// Sink es por donde un Executor manda eventos y logs mientras trabaja.
type Sink interface {
	// Event manda un hito. Si no hay conexión se descarta sin error: es best effort.
	Event(ev protocol.Event) error
	// Log manda líneas de log. Sin conexión devuelve error para cortar el streaming.
	Log(chunk protocol.LogChunk) error
}

// Executor ejecuta comandos. Devuelve exactamente un Result por comando.
type Executor interface {
	Execute(ctx context.Context, cmd protocol.Command, sink Sink) protocol.Result
}

// Cluster es lo que el cliente pregunta al cluster para hello y heartbeat.
type Cluster interface {
	KubernetesVersion(ctx context.Context) (string, error)
	NodeCount(ctx context.Context) (int, error)
}

// Config del cliente.
type Config struct {
	APIURL       string // https://api.rampa.dev (se acepta http/https/ws/wss)
	Token        string // token del cluster; va en Authorization: Bearer
	AgentVersion string
	Capabilities []string
	Cluster      Cluster
	Executor     Executor
	Logger       *slog.Logger
	HTTPClient   *http.Client
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	DialTimeout  time.Duration
	// ShutdownGrace es cuánto espera Run a los comandos en curso cuando su ctx
	// se cancela, antes de darlos por fallidos y cerrar. Default 30s.
	ShutdownGrace time.Duration
}

// Client mantiene la conexión con la API y despacha comandos.
type Client struct {
	cfg     Config
	connURL string
	log     *slog.Logger

	connected atomic.Bool
	welcome   atomic.Pointer[protocol.Welcome]

	writeMu sync.Mutex
	conn    *websocket.Conn

	outboxMu sync.Mutex
	outbox   []protocol.Envelope

	nsMu    sync.Mutex
	nsLocks map[string]*sync.Mutex

	inflightMu sync.Mutex
	inflight   map[string]struct{}
	// done recuerda los comandos terminados con su result, acotado en cantidad
	// y antigüedad, para responder un comando repetido sin ejecutarlo de nuevo.
	done      map[string]doneCommand
	doneOrder []string
	wg        sync.WaitGroup

	// execCtx es el contexto de los ejecutores. No es el de la señal: se cancela
	// solo cuando venció el grace del apagado. draining indica que ya se pidió
	// el apagado y no se aceptan comandos nuevos.
	execCtx    context.Context
	cancelExec context.CancelFunc
	draining   atomic.Bool

	// Hooks para tests.
	onConnect func()
}

// doneCommand es un comando terminado y el result que se le entregó.
type doneCommand struct {
	result protocol.Envelope
	at     time.Time
}

// New valida la configuración y arma el cliente. No conecta.
func New(cfg Config) (*Client, error) {
	if cfg.APIURL == "" {
		return nil, errors.New("falta la URL de la API (RAMPA_API_URL)")
	}
	if cfg.Token == "" {
		return nil, errors.New("falta el token del cluster (RAMPA_CLUSTER_TOKEN)")
	}
	if cfg.Executor == nil {
		return nil, errors.New("falta el ejecutor de comandos")
	}
	if cfg.Cluster == nil {
		return nil, errors.New("falta el acceso al cluster")
	}
	connURL, err := connectURL(cfg.APIURL)
	if err != nil {
		return nil, err
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = defaultMinBackoff
	}
	if cfg.MaxBackoff < cfg.MinBackoff {
		cfg.MaxBackoff = defaultMaxBackoff
		if cfg.MaxBackoff < cfg.MinBackoff {
			cfg.MaxBackoff = cfg.MinBackoff
		}
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = defaultDialTimeout
	}
	if len(cfg.Capabilities) == 0 {
		cfg.Capabilities = DefaultCapabilities
	}
	if cfg.AgentVersion == "" {
		cfg.AgentVersion = "dev"
	}
	if cfg.ShutdownGrace <= 0 {
		cfg.ShutdownGrace = DefaultShutdownGrace
	}
	execCtx, cancelExec := context.WithCancel(context.Background())
	return &Client{
		cfg:        cfg,
		connURL:    connURL,
		log:        cfg.Logger,
		nsLocks:    map[string]*sync.Mutex{},
		inflight:   map[string]struct{}{},
		done:       map[string]doneCommand{},
		execCtx:    execCtx,
		cancelExec: cancelExec,
	}, nil
}

// connectURL convierte la URL de la API en la URL del WebSocket.
func connectURL(api string) (string, error) {
	u, err := url.Parse(strings.TrimRight(api, "/"))
	if err != nil {
		return "", fmt.Errorf("RAMPA_API_URL inválida: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("RAMPA_API_URL inválida: esquema %q (esperaba http o https)", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("RAMPA_API_URL inválida: sin host")
	}
	u.Path = strings.TrimRight(u.Path, "/") + ConnectPath
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// Connected dice si hay una sesión aceptada por la API (después de welcome).
func (c *Client) Connected() bool { return c.connected.Load() }

// Welcome devuelve el último welcome recibido, o nil si nunca hubo.
func (c *Client) Welcome() *protocol.Welcome { return c.welcome.Load() }

// Run conecta y se queda reconectando hasta que ctx termine. Cancelar ctx pide
// el apagado: el agente deja de aceptar comandos nuevos, mantiene la sesión
// abierta (reconectando si hace falta) para entregar los results de los
// comandos en curso y espera hasta ShutdownGrace. Lo que siga en curso al
// vencer el grace recibe un result failed con ShutdownMessage. Recién después
// se cierra la conexión y Run devuelve.
func (c *Client) Run(ctx context.Context) error {
	// La vida de las sesiones no es la del ctx de la señal: dura hasta que el
	// apagado termine de entregar los results.
	lifeCtx, endLife := context.WithCancel(context.Background())
	defer endLife()
	go func() {
		defer endLife()
		select {
		case <-ctx.Done():
		case <-lifeCtx.Done():
			return
		}
		c.shutdown()
	}()

	attempt := 0
	for {
		start := time.Now()
		err := c.session(lifeCtx)
		c.connected.Store(false)
		if lifeCtx.Err() != nil {
			c.log.Info("cerrando conexión con la API")
			return nil
		}
		// Una sesión que duró un rato resetea el backoff.
		if time.Since(start) > c.cfg.MaxBackoff {
			attempt = 0
		}
		wait := c.backoff(attempt)
		attempt++
		c.log.Warn("conexión con la API caída, reintentando", "err", err, "en", wait.Round(time.Millisecond), "intento", attempt)
		select {
		case <-lifeCtx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// shutdown deja de aceptar comandos, espera a los en curso hasta el grace y da
// por fallidos los que sigan. Al volver, todos los results están entregados o
// encolados sin conexión posible.
func (c *Client) shutdown() {
	c.draining.Store(true)
	grace := c.cfg.ShutdownGrace
	c.log.Info("apagado pedido: no acepto comandos nuevos, espero los en curso", "grace", grace, "en_curso", len(c.InFlight()))
	if c.WaitInflight(grace) {
		c.log.Info("comandos en curso terminados y results entregados")
		return
	}
	pending := c.InFlight()
	c.log.Warn("venció el grace del apagado; doy por fallidos los comandos en curso", "command_ids", pending)
	for _, id := range pending {
		c.finish(protocol.Result{CommandID: id, Status: protocol.ResultFailed, Message: ShutdownMessage})
	}
	c.cancelExec()
	// Un margen corto para que las goroutines terminen y, si hace falta, se
	// entreguen los results recién encolados.
	c.WaitInflight(shutdownExtra)
}

// WaitInflight espera a que terminen los comandos en curso y a que sus results
// estén entregados (cola de salida vacía), o a que venza el plazo. Devuelve
// true si todo quedó entregado.
func (c *Client) WaitInflight(timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-deadline.C:
		return false
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for c.outboxLen() > 0 {
		select {
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
	return true
}

// InFlight devuelve, ordenados, los ids de los comandos que se están ejecutando.
func (c *Client) InFlight() []string {
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	ids := make([]string, 0, len(c.inflight))
	for id := range c.inflight {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// backoff exponencial con jitter, acotado entre MinBackoff y MaxBackoff.
func (c *Client) backoff(attempt int) time.Duration {
	if attempt > 30 {
		attempt = 30
	}
	d := c.cfg.MinBackoff << uint(attempt)
	if d <= 0 || d > c.cfg.MaxBackoff {
		d = c.cfg.MaxBackoff
	}
	// Jitter: entre la mitad y el total del intervalo calculado.
	half := d / 2
	if half > 0 {
		d = half + time.Duration(rand.Int64N(int64(half)+1))
	}
	if d < c.cfg.MinBackoff {
		d = c.cfg.MinBackoff
	}
	if d > c.cfg.MaxBackoff {
		d = c.cfg.MaxBackoff
	}
	return d
}

// session abre una conexión, hace el handshake y atiende mensajes hasta que se corte.
func (c *Client) session(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout)
	defer cancel()
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.cfg.Token)
	headers.Set("User-Agent", "rampa-agent/"+c.cfg.AgentVersion)
	conn, resp, err := websocket.Dial(dialCtx, c.connURL, &websocket.DialOptions{
		HTTPHeader: headers,
		HTTPClient: c.cfg.HTTPClient,
	})
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return fmt.Errorf("la API rechazó el token del cluster (HTTP %d); revisá RAMPA_CLUSTER_TOKEN", resp.StatusCode)
			default:
				return fmt.Errorf("la API respondió HTTP %d al conectar", resp.StatusCode)
			}
		}
		return fmt.Errorf("no pude conectar a %s: %w", c.connURL, err)
	}
	conn.SetReadLimit(readLimit)
	defer conn.CloseNow()

	sessCtx, cancelSess := context.WithCancel(ctx)
	defer cancelSess()

	welcome, err := c.handshake(sessCtx, conn)
	if err != nil {
		return err
	}
	c.welcome.Store(welcome)
	c.setConn(conn)
	c.connected.Store(true)
	defer func() {
		c.connected.Store(false)
		c.setConn(nil)
	}()
	c.log.Info("conectado a la API",
		"cluster", welcome.ClusterName, "cluster_id", welcome.ClusterID, "org", welcome.OrgSlug,
		"heartbeat_s", welcome.HeartbeatSeconds, "pending_commands", welcome.PendingCommands)
	if c.onConnect != nil {
		c.onConnect()
	}
	c.flushOutbox(sessCtx)

	interval := time.Duration(welcome.HeartbeatSeconds) * time.Second
	if interval <= 0 {
		interval = protocol.DefaultHeartbeatSeconds * time.Second
	}
	hbDone := make(chan error, 1)
	go func() { hbDone <- c.heartbeatLoop(sessCtx, conn, interval) }()

	readErr := make(chan error, 1)
	go func() { readErr <- c.readLoop(sessCtx, conn) }()

	select {
	case err := <-readErr:
		cancelSess()
		return err
	case err := <-hbDone:
		cancelSess()
		conn.CloseNow()
		<-readErr
		return err
	}
}

// handshake manda hello y espera welcome.
func (c *Client) handshake(ctx context.Context, conn *websocket.Conn) (*protocol.Welcome, error) {
	verCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	k8sVersion, err := c.cfg.Cluster.KubernetesVersion(verCtx)
	cancel()
	if err != nil {
		c.log.Warn("no pude obtener la versión de Kubernetes para el hello", "err", err)
	}
	hello := protocol.Hello{
		Protocol:          protocol.Version,
		AgentVersion:      c.cfg.AgentVersion,
		KubernetesVersion: k8sVersion,
		Capabilities:      c.cfg.Capabilities,
		InFlight:          c.InFlight(),
	}
	env, err := protocol.NewEnvelope(protocol.KindHello, uuid.NewString(), "", hello)
	if err != nil {
		return nil, fmt.Errorf("no pude armar el hello: %w", err)
	}
	if err := writeEnvelope(ctx, conn, env); err != nil {
		return nil, fmt.Errorf("no pude mandar el hello: %w", err)
	}

	readCtx, cancelRead := context.WithTimeout(ctx, welcomeTimeout)
	defer cancelRead()
	var reply protocol.Envelope
	if err := wsjson.Read(readCtx, conn, &reply); err != nil {
		return nil, fmt.Errorf("esperando welcome: %w", err)
	}
	switch reply.Kind {
	case protocol.KindWelcome:
		var w protocol.Welcome
		if err := json.Unmarshal(reply.Payload, &w); err != nil {
			return nil, fmt.Errorf("welcome inválido: %w", err)
		}
		return &w, nil
	case protocol.KindError:
		var e protocol.Error
		_ = json.Unmarshal(reply.Payload, &e)
		return nil, fmt.Errorf("la API rechazó la conexión: %s (%s)", e.Message, e.Code)
	default:
		return nil, fmt.Errorf("esperaba welcome y llegó %q", reply.Kind)
	}
}

// heartbeatLoop manda heartbeat cada interval y hace ping para detectar conexiones muertas.
func (c *Client) heartbeatLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		infoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		nodes, err := c.cfg.Cluster.NodeCount(infoCtx)
		if err != nil {
			c.log.Debug("no pude contar los nodos para el heartbeat", "err", err)
		}
		k8sVersion, _ := c.cfg.Cluster.KubernetesVersion(infoCtx)
		cancel()
		hb := protocol.Heartbeat{Nodes: nodes, KubernetesVersion: k8sVersion, AgentVersion: c.cfg.AgentVersion}
		env, err := protocol.NewEnvelope(protocol.KindHeartbeat, uuid.NewString(), "", hb)
		if err != nil {
			return err
		}
		if err := c.write(ctx, env); err != nil {
			return fmt.Errorf("heartbeat: %w", err)
		}
		pingCtx, cancelPing := context.WithTimeout(ctx, pingTimeout)
		err = conn.Ping(pingCtx)
		cancelPing()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("la API no respondió al ping: %w", err)
		}
	}
}

// readLoop procesa mensajes hasta que la conexión falle.
func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		var env protocol.Envelope
		if err := wsjson.Read(ctx, conn, &env); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if status := websocket.CloseStatus(err); status != -1 {
				return fmt.Errorf("la API cerró la conexión (%d)", status)
			}
			return fmt.Errorf("leyendo de la API: %w", err)
		}
		switch env.Kind {
		case protocol.KindCommand:
			c.handleCommand(ctx, env)
		case protocol.KindError:
			var e protocol.Error
			_ = json.Unmarshal(env.Payload, &e)
			c.log.Warn("error de protocolo desde la API", "code", e.Code, "message", e.Message, "command_id", env.CommandID)
		case protocol.KindWelcome:
			c.log.Debug("welcome repetido, ignorado")
		default:
			c.log.Warn("mensaje inesperado de la API, ignorado", "kind", env.Kind, "id", env.ID)
		}
	}
}

// handleCommand hace ack, valida y lanza la ejecución en su goroutine.
// ctx es el de la sesión: solo se usa para el ack. La ejecución vive más que la sesión.
func (c *Client) handleCommand(ctx context.Context, env protocol.Envelope) {
	var cmd protocol.Command
	decodeErr := json.Unmarshal(env.Payload, &cmd)
	if cmd.ID == "" {
		cmd.ID = env.CommandID
	}
	if cmd.ID == "" {
		cmd.ID = env.ID
	}
	if cmd.ID == "" {
		c.log.Error("comando sin id, no puedo responder", "err", decodeErr)
		return
	}
	log := c.log.With("command_id", cmd.ID, "kind", cmd.Kind)

	// Un comando que ya terminó (la API lo reenvió porque perdió el ack o el
	// result) recibe otra vez su result, sin volver a ejecutarse.
	if prev, ok := c.finished(cmd.ID); ok {
		log.Info("comando repetido ya terminado; reenvío el result sin ejecutarlo")
		c.writeOrEnqueue(prev)
		return
	}
	// Apagándonos no aceptamos comandos nuevos: sin ack, la API lo reenvía al
	// próximo agente que conecte.
	if c.draining.Load() {
		log.Info("comando ignorado: el agente se está apagando")
		return
	}

	ack, err := protocol.NewEnvelope(protocol.KindAck, uuid.NewString(), cmd.ID, protocol.Ack{CommandID: cmd.ID})
	if err == nil {
		err = c.write(ctx, ack)
	}
	if err != nil {
		log.Warn("no pude mandar el ack", "err", err)
	}

	if decodeErr != nil {
		c.finish(protocol.Result{CommandID: cmd.ID, Status: protocol.ResultRejected, Message: "payload del comando inválido: " + decodeErr.Error()})
		return
	}
	ns, serialize, ok := commandNamespace(cmd)
	if !ok {
		c.finish(protocol.Result{CommandID: cmd.ID, Status: protocol.ResultRejected, Message: fmt.Sprintf("comando %q no soportado o payload incompleto", cmd.Kind)})
		return
	}
	if !c.markInflight(cmd.ID) {
		log.Info("comando repetido mientras sigue en curso; ack reenviado")
		return
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.unmarkInflight(cmd.ID)
		if serialize {
			lock := c.namespaceLock(ns)
			lock.Lock()
			defer lock.Unlock()
		}
		log.Info("ejecutando comando", "namespace", ns)
		// El contexto de ejecución es el del agente, no el de la sesión ni el de
		// la señal: si la conexión se cae a mitad de un deploy, el deploy sigue y
		// el result se entrega al reconectar; si llega SIGTERM, se espera hasta
		// el grace antes de cancelarlo.
		res := c.cfg.Executor.Execute(c.execCtx, cmd, &sink{c: c, commandID: cmd.ID})
		if res.CommandID == "" {
			res.CommandID = cmd.ID
		}
		if res.Status == "" {
			res.Status = protocol.ResultFailed
			res.Message = "el ejecutor no informó un estado"
		}
		log.Info("comando terminado", "status", res.Status, "message", res.Message)
		c.finish(res)
	}()
}

// commandNamespace devuelve el namespace del comando y si hay que serializarlo
// con los demás comandos de ese namespace. deploy y undeploy se serializan;
// status y logs son de solo lectura y corren en paralelo. ok es false si el
// comando no se entiende o le falta el payload.
func commandNamespace(cmd protocol.Command) (ns string, serialize, ok bool) {
	switch cmd.Kind {
	case protocol.CommandDeploy:
		if cmd.Deploy != nil && cmd.Deploy.Namespace != "" {
			return cmd.Deploy.Namespace, true, true
		}
	case protocol.CommandUndeploy:
		if cmd.Undeploy != nil && cmd.Undeploy.Namespace != "" {
			return cmd.Undeploy.Namespace, true, true
		}
	case protocol.CommandStatus:
		if cmd.Status != nil && cmd.Status.Namespace != "" {
			return cmd.Status.Namespace, false, true
		}
	case protocol.CommandLogs:
		if cmd.Logs != nil && cmd.Logs.Namespace != "" {
			return cmd.Logs.Namespace, false, true
		}
	case protocol.CommandWriteSecret:
		// Se serializa con los deploys del namespace para no pisar el Secret
		// <app>-params entre dos escrituras concurrentes. Con target cloud el
		// namespace puede venir vacío: se usa <app>-<scope> como clave.
		if ws := cmd.WriteSecret; ws != nil && ws.App != "" {
			ns := ws.Namespace
			if ns == "" {
				ns = ws.App + "-" + ws.Scope
			}
			return ns, true, true
		}
	}
	return "", false, false
}

func (c *Client) namespaceLock(ns string) *sync.Mutex {
	c.nsMu.Lock()
	defer c.nsMu.Unlock()
	l, ok := c.nsLocks[ns]
	if !ok {
		l = &sync.Mutex{}
		c.nsLocks[ns] = l
	}
	return l
}

func (c *Client) markInflight(id string) bool {
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	if _, exists := c.inflight[id]; exists {
		return false
	}
	c.inflight[id] = struct{}{}
	return true
}

func (c *Client) unmarkInflight(id string) {
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	delete(c.inflight, id)
}

// markDone registra el result de un comando terminado. Devuelve false si ya
// había uno: el segundo result (por ejemplo el del ejecutor después de que el
// apagado lo dio por fallido) se descarta. Poda por cantidad y antigüedad.
func (c *Client) markDone(id string, env protocol.Envelope) bool {
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	if _, exists := c.done[id]; exists {
		return false
	}
	now := time.Now()
	c.done[id] = doneCommand{result: env, at: now}
	c.doneOrder = append(c.doneOrder, id)
	for len(c.doneOrder) > 0 {
		oldest := c.doneOrder[0]
		entry := c.done[oldest]
		if len(c.doneOrder) <= maxDoneCommands && now.Sub(entry.at) < doneCommandTTL {
			break
		}
		delete(c.done, oldest)
		c.doneOrder = c.doneOrder[1:]
	}
	return true
}

// finished devuelve el result de un comando terminado, si lo recordamos.
func (c *Client) finished(id string) (protocol.Envelope, bool) {
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	entry, ok := c.done[id]
	if !ok || time.Since(entry.at) >= doneCommandTTL {
		return protocol.Envelope{}, false
	}
	return entry.result, true
}

// finish manda el result; si no hay conexión lo guarda para el reconectar.
// Un comando solo termina una vez: si ya tenía result, este se descarta.
func (c *Client) finish(res protocol.Result) {
	env, err := protocol.NewEnvelope(protocol.KindResult, uuid.NewString(), res.CommandID, res)
	if err != nil {
		c.log.Error("no pude serializar el result", "command_id", res.CommandID, "err", err)
		return
	}
	if !c.markDone(res.CommandID, env) {
		c.log.Info("result descartado: el comando ya tenía uno entregado", "command_id", res.CommandID, "status", res.Status)
		return
	}
	c.writeOrEnqueue(env)
}

// writeOrEnqueue manda el sobre por la conexión actual o, si no hay, lo encola
// para el próximo flushOutbox. La decisión y el encolado ocurren bajo writeMu,
// el mismo mutex que setConn: así un result no puede quedar encolado después
// de que la sesión nueva ya vació la cola.
func (c *Client) writeOrEnqueue(env protocol.Envelope) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		c.log.Warn("result sin entregar, queda pendiente para la próxima conexión", "command_id", env.CommandID)
		c.enqueue(env)
		return
	}
	if err := writeEnvelope(context.Background(), c.conn, env); err != nil {
		c.log.Warn("result sin entregar, queda pendiente para la próxima conexión", "command_id", env.CommandID, "err", err)
		c.enqueue(env)
	}
}

func (c *Client) enqueue(env protocol.Envelope) {
	c.outboxMu.Lock()
	defer c.outboxMu.Unlock()
	if len(c.outbox) >= maxOutbox {
		dropped := c.outbox[0]
		c.outbox = c.outbox[1:]
		c.log.Error("cola de results llena, descarto el más viejo", "command_id", dropped.CommandID)
	}
	c.outbox = append(c.outbox, env)
}

func (c *Client) outboxLen() int {
	c.outboxMu.Lock()
	defer c.outboxMu.Unlock()
	return len(c.outbox)
}

func (c *Client) flushOutbox(ctx context.Context) {
	c.outboxMu.Lock()
	pending := c.outbox
	c.outbox = nil
	c.outboxMu.Unlock()
	for i, env := range pending {
		if err := c.write(ctx, env); err != nil {
			c.log.Warn("no pude entregar results pendientes, vuelven a la cola", "restantes", len(pending)-i, "err", err)
			c.outboxMu.Lock()
			c.outbox = append(pending[i:], c.outbox...)
			c.outboxMu.Unlock()
			return
		}
	}
	if len(pending) > 0 {
		c.log.Info("results pendientes entregados", "cantidad", len(pending))
	}
}

func (c *Client) setConn(conn *websocket.Conn) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.conn = conn
}

// write serializa la escritura sobre la conexión actual.
func (c *Client) write(ctx context.Context, env protocol.Envelope) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return ErrNotConnected
	}
	return writeEnvelope(ctx, c.conn, env)
}

func writeEnvelope(ctx context.Context, conn *websocket.Conn, env protocol.Envelope) error {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(wctx, conn, env)
}

// sink implementa Sink para un comando.
type sink struct {
	c         *Client
	commandID string
}

func (s *sink) Event(ev protocol.Event) error {
	ev.CommandID = s.commandID
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	env, err := protocol.NewEnvelope(protocol.KindEvent, uuid.NewString(), s.commandID, ev)
	if err != nil {
		return err
	}
	if err := s.c.write(context.Background(), env); err != nil {
		s.c.log.Debug("evento descartado por falta de conexión", "command_id", s.commandID, "reason", ev.Reason)
	}
	return nil
}

func (s *sink) Log(chunk protocol.LogChunk) error {
	chunk.CommandID = s.commandID
	env, err := protocol.NewEnvelope(protocol.KindLog, uuid.NewString(), s.commandID, chunk)
	if err != nil {
		return err
	}
	return s.c.write(context.Background(), env)
}
