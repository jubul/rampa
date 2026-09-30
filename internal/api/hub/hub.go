// Package hub mantiene las conexiones WebSocket de los agentes y habla el
// protocolo de pkg/protocol: hello/welcome, heartbeats, comandos con ack,
// eventos y results. La persistencia queda detrás de Backend para que el hub
// se pueda testear con un agente falso y sin base de datos.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/protocol"
)

// ErrUnauthorized lo devuelve Backend.Authenticate cuando el token no vale.
var ErrUnauthorized = errors.New("token de cluster inválido")

// ErrOffline lo devuelve Send cuando el cluster no tiene una conexión viva.
var ErrOffline = errors.New("el agente del cluster no está conectado")

// Agent identifica la conexión de un agente: su cluster y su organización.
// Todo lo que el agente reporta se aplica solo a este cluster.
type Agent struct {
	ClusterID   string
	ClusterName string
	OrgID       string
	OrgSlug     string
}

// Backend es lo que el hub necesita de la persistencia.
type Backend interface {
	// Authenticate resuelve el token de cluster (texto plano) a un Agent.
	Authenticate(ctx context.Context, token string) (*Agent, error)
	// Connected marca el cluster online con lo que dice el hello.
	Connected(ctx context.Context, a *Agent, hello protocol.Hello) error
	// Disconnected marca el cluster offline.
	Disconnected(ctx context.Context, a *Agent, reason string) error
	// Heartbeat registra un latido.
	Heartbeat(ctx context.Context, a *Agent, hb protocol.Heartbeat) error
	// PendingCommands devuelve los comandos queued y, si includeSent, los
	// sent que nunca recibieron ack. En orden de creación.
	PendingCommands(ctx context.Context, a *Agent, includeSent bool) ([]protocol.Command, error)
	// CommandSent marca un comando como enviado.
	CommandSent(ctx context.Context, a *Agent, commandID string) error
	// CommandAcked registra el ack.
	CommandAcked(ctx context.Context, a *Agent, commandID string) error
	// CommandEvent procesa un evento de progreso.
	CommandEvent(ctx context.Context, a *Agent, ev protocol.Event) error
	// CommandResult cierra un comando.
	CommandResult(ctx context.Context, a *Agent, res protocol.Result) error
}

// Options ajusta tiempos. Los ceros toman defaults.
type Options struct {
	HeartbeatSeconds int           // lo que se le pide al agente
	HelloTimeout     time.Duration // cuánto esperar el hello tras el upgrade
	WriteTimeout     time.Duration
	// MissedHeartbeats es cuántos heartbeats seguidos sin noticias marcan offline.
	MissedHeartbeats int
}

func (o Options) withDefaults() Options {
	if o.HeartbeatSeconds <= 0 {
		o.HeartbeatSeconds = protocol.DefaultHeartbeatSeconds
	}
	if o.HelloTimeout <= 0 {
		o.HelloTimeout = 15 * time.Second
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 10 * time.Second
	}
	if o.MissedHeartbeats <= 0 {
		o.MissedHeartbeats = 3
	}
	return o
}

// Hub es el registro de conexiones vivas.
type Hub struct {
	backend Backend
	log     *slog.Logger
	opts    Options

	mu    sync.Mutex
	conns map[string]*conn // clusterID -> conexión activa
	wg    sync.WaitGroup
}

// New crea el hub.
func New(b Backend, log *slog.Logger, opts Options) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{backend: b, log: log, opts: opts.withDefaults(), conns: map[string]*conn{}}
}

type conn struct {
	agent  *Agent
	ws     *websocket.Conn
	wake   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc

	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool
}

// Online dice si hay una conexión viva para el cluster.
func (h *Hub) Online(clusterID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.conns[clusterID]
	return ok
}

// Notify avisa que hay comandos nuevos para el cluster. Si no está conectado,
// no pasa nada: los va a recibir al reconectar.
func (h *Hub) Notify(clusterID string) {
	h.mu.Lock()
	c := h.conns[clusterID]
	h.mu.Unlock()
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Send manda un comando por la conexión viva del cluster, sin pasar por la
// cola durable. Es para comandos que solo tienen sentido en vivo y cuyo payload
// no debe persistirse completo (write_secret: los valores viajan una sola vez).
// El llamador registra la fila de agent_commands por su cuenta; el ack y el
// result vuelven por el camino normal. Devuelve ErrOffline si no hay conexión.
func (h *Hub) Send(clusterID string, cmd protocol.Command) error {
	h.mu.Lock()
	c := h.conns[clusterID]
	h.mu.Unlock()
	if c == nil {
		return ErrOffline
	}
	if err := h.send(c, protocol.KindCommand, cmd.ID, cmd); err != nil {
		return fmt.Errorf("mandando el comando %s: %w", cmd.ID, err)
	}
	sentCtx, cancel := opCtx(c.ctx)
	defer cancel()
	if err := h.backend.CommandSent(sentCtx, c.agent, cmd.ID); err != nil {
		h.log.Error("marcando comando como enviado", "cluster_id", clusterID, "command_id", cmd.ID, "err", err)
	}
	h.log.Info("comando enviado en vivo", "cluster_id", clusterID, "command_id", cmd.ID, "kind", cmd.Kind)
	return nil
}

// Disconnect corta la conexión viva del cluster, si hay, con
// StatusPolicyViolation y el motivo. Lo usan rotate-token y borrar cluster:
// la sesión se autenticó con un token que ya no vale y no puede seguir
// operando hasta que el agente decida reconectar.
func (h *Hub) Disconnect(clusterID, reason string) bool {
	h.mu.Lock()
	c := h.conns[clusterID]
	h.mu.Unlock()
	if c == nil {
		return false
	}
	h.log.Info("cortando la conexión del agente", "cluster_id", clusterID, "reason", reason)
	c.close(websocket.StatusPolicyViolation, reason)
	return true
}

// Shutdown cierra todas las conexiones y espera a que terminen.
func (h *Hub) Shutdown(ctx context.Context) {
	h.mu.Lock()
	conns := make([]*conn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		c.close(websocket.StatusGoingAway, "la API se está apagando")
	}
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// ServeHTTP atiende GET /v1/agent/connect.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, ok := auth.BearerToken(r.Header.Get("Authorization"))
	if !ok || !auth.IsClusterToken(token) {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "hace falta Authorization: Bearer <token del cluster>")
		return
	}
	authCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	agent, err := h.backend.Authenticate(authCtx, token)
	cancel()
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "token de cluster inválido")
			return
		}
		h.log.Error("autenticando agente", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "internal", "error interno")
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		h.log.Warn("upgrade a websocket falló", "cluster_id", agent.ClusterID, "err", err)
		return
	}
	ws.SetReadLimit(64 << 20) // los results traen poco, pero los deploys que mandamos nosotros no cuentan acá

	h.wg.Add(1)
	defer h.wg.Done()
	h.serve(agent, ws)
}

func (h *Hub) serve(agent *Agent, ws *websocket.Conn) {
	// El contexto de la conexión es independiente del request: cerramos
	// nosotros cuando corresponde.
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{agent: agent, ws: ws, wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	log := h.log.With("cluster_id", agent.ClusterID, "cluster", agent.ClusterName, "org", agent.OrgSlug)

	// 1. hello
	helloCtx, helloCancel := context.WithTimeout(ctx, h.opts.HelloTimeout)
	var env protocol.Envelope
	err := wsjson.Read(helloCtx, ws, &env)
	helloCancel()
	if err != nil {
		log.Warn("el agente no mandó hello", "err", err)
		c.close(websocket.StatusPolicyViolation, "se esperaba hello")
		return
	}
	if env.Kind != protocol.KindHello {
		h.sendError(c, "bad_payload", "el primer mensaje tiene que ser hello")
		c.close(websocket.StatusPolicyViolation, "se esperaba hello")
		return
	}
	var hello protocol.Hello
	if len(env.Payload) > 0 {
		if err := json.Unmarshal(env.Payload, &hello); err != nil {
			h.sendError(c, "bad_payload", "hello inválido")
			c.close(websocket.StatusPolicyViolation, "hello inválido")
			return
		}
	}
	if hello.Protocol != protocol.Version {
		h.sendError(c, "unsupported", fmt.Sprintf("protocolo %d no soportado; la API habla %d", hello.Protocol, protocol.Version))
		c.close(websocket.StatusPolicyViolation, "protocolo incompatible")
		return
	}

	// 2. registrar (desplazando una conexión anterior del mismo cluster). El
	// cierre de la vieja puede esperar hasta 1 s al agente: va fuera del lock.
	h.mu.Lock()
	old := h.conns[agent.ClusterID]
	h.conns[agent.ClusterID] = c
	h.mu.Unlock()
	if old != nil {
		old.close(websocket.StatusPolicyViolation, "otra conexión del mismo cluster")
	}
	defer h.unregister(c, log)

	connCtx, connCancel := opCtx(ctx)
	err = h.backend.Connected(connCtx, agent, hello)
	connCancel()
	if err != nil {
		log.Error("marcando online", "err", err)
		c.close(websocket.StatusInternalError, "error interno")
		return
	}
	log.Info("agente conectado", "agent_version", hello.AgentVersion, "kubernetes", hello.KubernetesVersion)

	// 3. welcome con la cantidad de pendientes
	pendCtx, pendCancel := opCtx(ctx)
	pending, err := h.backend.PendingCommands(pendCtx, agent, true)
	pendCancel()
	if err != nil {
		log.Error("listando comandos pendientes", "err", err)
		c.close(websocket.StatusInternalError, "error interno")
		return
	}
	welcome := protocol.Welcome{
		ClusterID:        agent.ClusterID,
		ClusterName:      agent.ClusterName,
		OrgSlug:          agent.OrgSlug,
		HeartbeatSeconds: h.opts.HeartbeatSeconds,
		PendingCommands:  len(pending),
		ServerTime:       time.Now().UTC().Format(time.RFC3339),
	}
	if err := h.send(c, protocol.KindWelcome, "", welcome); err != nil {
		log.Warn("no pude mandar welcome", "err", err)
		return
	}

	// 4. reenviar queued y sent sin ack
	if err := h.dispatch(c, pending, log); err != nil {
		return
	}

	// 5. loop principal
	msgs := make(chan protocol.Envelope)
	readErr := make(chan error, 1)
	go func() {
		for {
			var env protocol.Envelope
			if err := wsjson.Read(ctx, ws, &env); err != nil {
				readErr <- err
				return
			}
			select {
			case msgs <- env:
			case <-ctx.Done():
				return
			}
		}
	}()

	deadline := time.Duration(h.opts.HeartbeatSeconds*h.opts.MissedHeartbeats) * time.Second
	watchdog := time.NewTimer(deadline)
	defer watchdog.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-readErr:
			status := websocket.CloseStatus(err)
			if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
				log.Info("agente desconectado")
			} else {
				log.Warn("conexión del agente cerrada", "err", err)
			}
			return
		case <-watchdog.C:
			log.Warn("sin heartbeats, marco offline", "esperados", h.opts.MissedHeartbeats)
			// El agente no responde: no tiene sentido esperar su close frame.
			c.closeNow()
			return
		case <-c.wake:
			qCtx, qCancel := opCtx(ctx)
			queued, err := h.backend.PendingCommands(qCtx, agent, false)
			qCancel()
			if err != nil {
				log.Error("listando comandos queued", "err", err)
				continue
			}
			if err := h.dispatch(c, queued, log); err != nil {
				return
			}
		case env := <-msgs:
			if !watchdog.Stop() {
				select {
				case <-watchdog.C:
				default:
				}
			}
			watchdog.Reset(deadline)
			h.handle(c, env, log)
		}
	}
}

func (h *Hub) unregister(c *conn, log *slog.Logger) {
	h.mu.Lock()
	current := h.conns[c.agent.ClusterID] == c
	if current {
		delete(h.conns, c.agent.ClusterID)
	}
	h.mu.Unlock()
	// Primero el estado (offline ya), después el handshake de cierre, que
	// puede esperar al agente.
	if current {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.backend.Disconnected(ctx, c.agent, "conexión cerrada"); err != nil {
			log.Error("marcando offline", "err", err)
		}
	}
	c.close(websocket.StatusNormalClosure, "")
}

// opCtx acota una operación del backend a un timeout, atada al contexto de la conexión.
func opCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 10*time.Second)
}

func (h *Hub) dispatch(c *conn, cmds []protocol.Command, log *slog.Logger) error {
	for _, cmd := range cmds {
		if err := h.send(c, protocol.KindCommand, cmd.ID, cmd); err != nil {
			log.Warn("no pude mandar comando", "command_id", cmd.ID, "err", err)
			return err
		}
		sentCtx, sentCancel := opCtx(c.ctx)
		err := h.backend.CommandSent(sentCtx, c.agent, cmd.ID)
		sentCancel()
		if err != nil {
			log.Error("marcando comando como enviado", "command_id", cmd.ID, "err", err)
		}
		log.Info("comando enviado", "command_id", cmd.ID, "kind", cmd.Kind)
	}
	return nil
}

func (h *Hub) handle(c *conn, env protocol.Envelope, log *slog.Logger) {
	ctx, cancel := opCtx(c.ctx)
	defer cancel()
	commandID := func(fromPayload string) string {
		if env.CommandID != "" {
			return env.CommandID
		}
		return fromPayload
	}
	switch env.Kind {
	case protocol.KindHeartbeat:
		var hb protocol.Heartbeat
		_ = json.Unmarshal(env.Payload, &hb)
		if err := h.backend.Heartbeat(ctx, c.agent, hb); err != nil {
			log.Error("registrando heartbeat", "err", err)
		}
	case protocol.KindAck:
		var ack protocol.Ack
		_ = json.Unmarshal(env.Payload, &ack)
		id := commandID(ack.CommandID)
		if id == "" {
			h.sendError(c, "bad_payload", "ack sin command_id")
			return
		}
		h.withRetry(c, "ack", id, log, func(ctx context.Context) error {
			return h.backend.CommandAcked(ctx, c.agent, id)
		})
	case protocol.KindEvent:
		var ev protocol.Event
		if err := json.Unmarshal(env.Payload, &ev); err != nil {
			h.sendError(c, "bad_payload", "event inválido")
			return
		}
		ev.CommandID = commandID(ev.CommandID)
		if ev.CommandID == "" {
			h.sendError(c, "bad_payload", "event sin command_id")
			return
		}
		if ev.Time.IsZero() {
			ev.Time = time.Now().UTC()
		}
		if err := h.backend.CommandEvent(ctx, c.agent, ev); err != nil {
			log.Warn("event rechazado", "command_id", ev.CommandID, "err", err)
		}
	case protocol.KindResult:
		var res protocol.Result
		if err := json.Unmarshal(env.Payload, &res); err != nil {
			h.sendError(c, "bad_payload", "result inválido")
			return
		}
		res.CommandID = commandID(res.CommandID)
		if res.CommandID == "" {
			h.sendError(c, "bad_payload", "result sin command_id")
			return
		}
		h.withRetry(c, "result", res.CommandID, log, func(ctx context.Context) error {
			return h.backend.CommandResult(ctx, c.agent, res)
		})
	case protocol.KindLog:
		// Fase B: logs en vivo. Por ahora solo se aceptan.
		log.Debug("log chunk recibido", "command_id", env.CommandID)
	case protocol.KindError:
		var pe protocol.Error
		_ = json.Unmarshal(env.Payload, &pe)
		log.Warn("error de protocolo del agente", "code", pe.Code, "message", pe.Message)
	default:
		h.sendError(c, "bad_payload", fmt.Sprintf("kind %q desconocido", env.Kind))
	}
}

// retryBackoff son las esperas entre intentos de ack y result contra el
// backend. Acotado: el loop de la conexión se bloquea mientras reintenta.
var retryBackoff = []time.Duration{250 * time.Millisecond, time.Second}

// withRetry corre op contra el backend y reintenta con backoff si falla por
// algo transitorio (la base no responde). Un ErrNotFound es permanente: el
// comando no es de este cluster o ya se cerró, y se descarta con log. El
// agente no reenvía results, así que perder uno acá deja el deployment
// colgado hasta que lo recoja el reaper.
func (h *Hub) withRetry(c *conn, what, commandID string, log *slog.Logger, op func(ctx context.Context) error) {
	var err error
	for attempt := 0; ; attempt++ {
		ctx, cancel := opCtx(c.ctx)
		err = op(ctx)
		cancel()
		if err == nil {
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			log.Warn(what+" rechazado", "command_id", commandID, "err", err)
			return
		}
		if attempt >= len(retryBackoff) {
			break
		}
		log.Warn(what+" falló, reintento", "command_id", commandID, "attempt", attempt+1, "err", err)
		select {
		case <-time.After(retryBackoff[attempt]):
		case <-c.ctx.Done():
			log.Error(what+" descartado: la conexión se cerró durante el reintento", "command_id", commandID, "err", err)
			return
		}
	}
	log.Error(what+" descartado tras reintentos", "command_id", commandID, "attempts", len(retryBackoff)+1, "err", err)
}

func (h *Hub) send(c *conn, kind protocol.Kind, commandID string, payload any) error {
	env, err := protocol.NewEnvelope(kind, uuid.NewString(), commandID, payload)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(c.ctx, h.opts.WriteTimeout)
	defer cancel()
	return wsjson.Write(ctx, c.ws, env)
}

func (h *Hub) sendError(c *conn, code, message string) {
	if err := h.send(c, protocol.KindError, "", protocol.Error{Code: code, Message: message}); err != nil {
		h.log.Debug("no pude mandar error de protocolo", "err", err)
	}
}

func (c *conn) close(status websocket.StatusCode, reason string) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	// Close hace el handshake de cierre y espera al par; si el agente no
	// responde, cortamos la conexión sin esperar más.
	done := make(chan struct{})
	go func() {
		_ = c.ws.Close(status, reason)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = c.ws.CloseNow()
	}
	c.cancel()
}

// closeNow corta la conexión sin handshake.
func (c *conn) closeNow() {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	_ = c.ws.CloseNow()
	c.cancel()
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message}})
}
