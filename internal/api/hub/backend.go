package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/events"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/protocol"
)

// StoreBackend es el Backend real: Postgres más el bus de eventos.
type StoreBackend struct {
	store *store.Store
	bus   *events.Bus
	log   *slog.Logger
}

// NewStoreBackend arma el backend.
func NewStoreBackend(st *store.Store, bus *events.Bus, log *slog.Logger) *StoreBackend {
	if log == nil {
		log = slog.Default()
	}
	return &StoreBackend{store: st, bus: bus, log: log}
}

func agentActor(a *Agent) store.Actor {
	return store.Actor{Type: "agent", ID: a.ClusterID, Name: a.ClusterName}
}

// Authenticate busca el cluster por el hash del token.
func (b *StoreBackend) Authenticate(ctx context.Context, token string) (*Agent, error) {
	cl, err := b.store.GetClusterByTokenHash(ctx, auth.HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	org, err := b.store.GetOrg(ctx, cl.OrgID)
	if err != nil {
		return nil, err
	}
	return &Agent{ClusterID: cl.ID, ClusterName: cl.Name, OrgID: cl.OrgID, OrgSlug: org.Slug}, nil
}

// Mensajes con los que se cierran los comandos cuyo result nunca va a llegar.
const (
	// OrphanMessage es para los acked que el agente no declaró en curso al reconectar.
	OrphanMessage = "el agente se reinició y el resultado se perdió"
	// StaleMessage es para los que el reaper encuentra vencidos.
	StaleMessage = "el agente no reportó el resultado dentro del tiempo esperado"
)

// Connected marca online, emite cluster.online y reconcilia los comandos
// acked del cluster con hello.InFlight: los que el agente ya no tiene en
// curso perdieron su result (se reinició) y se cierran como failed, con el
// mismo camino que un result del agente.
func (b *StoreBackend) Connected(ctx context.Context, a *Agent, hello protocol.Hello) error {
	if err := b.store.SetClusterOnline(ctx, a.OrgID, a.ClusterID, hello.AgentVersion, hello.KubernetesVersion); err != nil {
		return err
	}
	b.bus.Emit(ctx, a.OrgID, agentActor(a), "cluster.online", "cluster", a.ClusterID,
		fmt.Sprintf("el agente de %s se conectó", a.ClusterName),
		map[string]any{"agent_version": hello.AgentVersion, "kubernetes_version": hello.KubernetesVersion, "capabilities": hello.Capabilities, "in_flight": len(hello.InFlight)})
	return b.reconcileInFlight(ctx, a, hello.InFlight)
}

// reconcileInFlight falla los comandos acked del cluster que no están en inFlight.
func (b *StoreBackend) reconcileInFlight(ctx context.Context, a *Agent, inFlight []string) error {
	acked, err := b.store.AckedAgentCommands(ctx, a.OrgID, a.ClusterID)
	if err != nil {
		return err
	}
	if len(acked) == 0 {
		return nil
	}
	running := make(map[string]bool, len(inFlight))
	for _, id := range inFlight {
		running[id] = true
	}
	for i := range acked {
		cmd := &acked[i]
		if running[cmd.ID] {
			continue
		}
		b.log.Warn("comando acked huérfano tras la reconexión, lo marco failed", "cluster_id", a.ClusterID, "command_id", cmd.ID, "kind", cmd.Kind)
		res := protocol.Result{CommandID: cmd.ID, Status: protocol.ResultFailed, Message: OrphanMessage}
		if err := b.finishCommand(ctx, a, cmd, res); err != nil {
			return fmt.Errorf("cerrando el comando huérfano %s: %w", cmd.ID, err)
		}
	}
	return nil
}

// ReapStaleCommands cierra como failed los comandos sent o acked de cualquier
// cluster cuya antigüedad supera el timeout del payload más grace. Cubre lo
// que la reconciliación del hello no puede ver: un agente que sigue conectado
// pero nunca manda el result, o uno que no vuelve. Devuelve cuántos cerró.
func (b *StoreBackend) ReapStaleCommands(ctx context.Context, defaultTimeout, grace time.Duration) (int, error) {
	stale, err := b.store.StaleAgentCommands(ctx, int(defaultTimeout.Seconds()), grace, 100)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range stale {
		cmd := &stale[i]
		cl, err := b.store.GetCluster(ctx, cmd.OrgID, cmd.ClusterID)
		if err != nil {
			b.log.Error("reaper: no pude cargar el cluster del comando", "command_id", cmd.ID, "err", err)
			continue
		}
		a := &Agent{ClusterID: cl.ID, ClusterName: cl.Name, OrgID: cl.OrgID}
		b.log.Warn("comando vencido sin result, lo marco failed", "cluster_id", a.ClusterID, "command_id", cmd.ID, "kind", cmd.Kind, "status", cmd.Status)
		res := protocol.Result{CommandID: cmd.ID, Status: protocol.ResultTimeout, Message: StaleMessage}
		if err := b.finishCommand(ctx, a, cmd, res); err != nil {
			b.log.Error("reaper: no pude cerrar el comando", "command_id", cmd.ID, "err", err)
			continue
		}
		n++
	}
	return n, nil
}

// RunReaper corre ReapStaleCommands cada interval hasta que ctx termina.
func (b *StoreBackend) RunReaper(ctx context.Context, interval, defaultTimeout, grace time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, interval)
			n, err := b.ReapStaleCommands(rctx, defaultTimeout, grace)
			cancel()
			if err != nil {
				b.log.Error("reaper de comandos", "err", err)
			} else if n > 0 {
				b.log.Info("reaper de comandos", "closed", n)
			}
		}
	}
}

// Disconnected marca offline y emite cluster.offline.
func (b *StoreBackend) Disconnected(ctx context.Context, a *Agent, reason string) error {
	if err := b.store.SetClusterOffline(ctx, a.OrgID, a.ClusterID); err != nil {
		return err
	}
	b.bus.Emit(ctx, a.OrgID, agentActor(a), "cluster.offline", "cluster", a.ClusterID,
		fmt.Sprintf("el agente de %s se desconectó: %s", a.ClusterName, reason), nil)
	return nil
}

// Heartbeat actualiza last_seen_at.
func (b *StoreBackend) Heartbeat(ctx context.Context, a *Agent, hb protocol.Heartbeat) error {
	return b.store.TouchCluster(ctx, a.OrgID, a.ClusterID, hb.AgentVersion, hb.KubernetesVersion)
}

// PendingCommands lee la cola durable y la convierte a protocol.Command.
func (b *StoreBackend) PendingCommands(ctx context.Context, a *Agent, includeSent bool) ([]protocol.Command, error) {
	rows, err := b.store.PendingAgentCommands(ctx, a.OrgID, a.ClusterID, includeSent)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.Command, 0, len(rows))
	for _, row := range rows {
		var cmd protocol.Command
		if err := json.Unmarshal(row.Payload, &cmd); err != nil {
			b.log.Error("payload de agent_command corrupto, lo marco failed", "command_id", row.ID, "err", err)
			res, _ := json.Marshal(map[string]string{"status": "failed", "message": "payload corrupto: " + err.Error()})
			_ = b.store.FinishAgentCommand(ctx, a.OrgID, a.ClusterID, row.ID, store.CommandFailed, res)
			continue
		}
		cmd.ID = row.ID
		out = append(out, cmd)
	}
	return out, nil
}

// CommandSent marca sent.
func (b *StoreBackend) CommandSent(ctx context.Context, a *Agent, commandID string) error {
	return b.store.MarkAgentCommandSent(ctx, a.OrgID, a.ClusterID, commandID)
}

// CommandAcked marca acked y, si es un deploy, registra started_at.
func (b *StoreBackend) CommandAcked(ctx context.Context, a *Agent, commandID string) error {
	if err := b.store.MarkAgentCommandAcked(ctx, a.OrgID, a.ClusterID, commandID); err != nil {
		return err
	}
	cmd, err := b.store.GetAgentCommand(ctx, a.OrgID, a.ClusterID, commandID)
	if err != nil {
		return err
	}
	if cmd.DeploymentID != "" {
		now := time.Now().UTC()
		if _, err := b.store.UpdateDeployment(ctx, a.OrgID, cmd.DeploymentID, store.DeploymentUpdate{StartedAt: &now}); err != nil {
			return err
		}
	}
	return nil
}

// CommandEvent actualiza el deployment según la fase y emite un evento de la org.
func (b *StoreBackend) CommandEvent(ctx context.Context, a *Agent, ev protocol.Event) error {
	cmd, err := b.store.GetAgentCommand(ctx, a.OrgID, a.ClusterID, ev.CommandID)
	if err != nil {
		return fmt.Errorf("comando %s: %w", ev.CommandID, err)
	}
	payload := map[string]any{"phase": ev.Phase, "reason": ev.Reason, "object": ev.Object, "command_id": ev.CommandID}

	switch cmd.Kind {
	case string(protocol.CommandDeploy):
		if cmd.DeploymentID == "" {
			return nil
		}
		dep, err := b.store.GetDeployment(ctx, a.OrgID, cmd.DeploymentID)
		if err != nil {
			return err
		}
		if dep.Terminal() {
			return nil
		}
		status := DeploymentStatusForPhase(ev.Phase)
		kind := "deployment.progress"
		if status != "" && status != dep.Status {
			now := time.Now().UTC()
			upd := store.DeploymentUpdate{Status: status, StartedAt: &now}
			if ev.Message != "" {
				msg := ev.Message
				upd.Message = &msg
			}
			if status == store.DeploymentHealthy || status == store.DeploymentFailed {
				upd.FinishedAt = &now
			}
			err := b.store.WithTx(ctx, func(tx *store.Store) error {
				if _, err := tx.UpdateDeployment(ctx, a.OrgID, dep.ID, upd); err != nil {
					return err
				}
				return b.applyScopeForDeployment(ctx, tx, a.OrgID, dep, status)
			})
			if err != nil {
				return err
			}
			kind = "deployment." + status
		}
		payload["app"] = dep.AppName
		payload["scope"] = dep.ScopeName
		b.bus.Emit(ctx, a.OrgID, agentActor(a), kind, "deployment", dep.ID, ev.Message, payload)
	case string(protocol.CommandUndeploy):
		if cmd.ScopeID != "" {
			if sc, err := b.store.GetScopeByID(ctx, a.OrgID, cmd.ScopeID); err == nil {
				payload["scope"] = sc.Name
				payload["app"] = b.appName(ctx, a.OrgID, sc.ApplicationID)
			}
			b.bus.Emit(ctx, a.OrgID, agentActor(a), "scope.progress", "scope", cmd.ScopeID, ev.Message, payload)
		}
	default:
		b.bus.Emit(ctx, a.OrgID, agentActor(a), "cluster.command_progress", "cluster", a.ClusterID, ev.Message, payload)
	}
	return nil
}

// CommandResult cierra el comando y actualiza deployment y scope.
func (b *StoreBackend) CommandResult(ctx context.Context, a *Agent, res protocol.Result) error {
	cmd, err := b.store.GetAgentCommand(ctx, a.OrgID, a.ClusterID, res.CommandID)
	if err != nil {
		return fmt.Errorf("comando %s: %w", res.CommandID, err)
	}
	if cmd.Status == store.CommandDone || cmd.Status == store.CommandFailed {
		// Result repetido tras una reconexión: ya está procesado.
		return nil
	}
	return b.finishCommand(ctx, a, cmd, res)
}

// applyScopeForDeployment lleva el scope al estado que corresponde al del
// deployment. Un scope en deleting no se toca: el undeploy encolado manda y
// el estado del deploy queda solo en el deployment.
func (b *StoreBackend) applyScopeForDeployment(ctx context.Context, tx *store.Store, orgID string, dep *store.Deployment, status string) error {
	sc := ScopeStatusForDeployment(status)
	if sc == "" {
		return nil
	}
	err := tx.SetScopeDeployStatus(ctx, orgID, dep.ScopeID, sc, dep.ID)
	if errors.Is(err, store.ErrConflict) {
		b.log.Info("el scope está en deleting; no le aplico el estado del deploy", "scope_id", dep.ScopeID, "deployment_id", dep.ID, "status", status)
		return nil
	}
	return err
}

// finishCommand es el único camino para cerrar un comando: lo usan el result
// del agente, la reconciliación del hello y el reaper. Cierra el comando y
// actualiza deployment y scope en UNA transacción, así un fallo a mitad de
// camino no deja el comando done con el deployment colgado en applying. Los
// eventos se emiten después del commit. Si otro camino lo cerró antes
// (FinishAgentCommand no afecta filas), no hace nada.
func (b *StoreBackend) finishCommand(ctx context.Context, a *Agent, cmd *store.AgentCommand, res protocol.Result) error {
	var emits []func()
	emit := func(kind, subjectType, subjectID, message string, payload any) {
		emits = append(emits, func() { b.bus.Emit(ctx, a.OrgID, agentActor(a), kind, subjectType, subjectID, message, payload) })
	}
	resJSON, _ := json.Marshal(res)
	err := b.store.WithTx(ctx, func(tx *store.Store) error {
		if err := tx.FinishAgentCommand(ctx, a.OrgID, a.ClusterID, cmd.ID, CommandStatusForResult(res), resJSON); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// Ya lo cerró otro camino (result y reaper a la vez): nada que hacer.
				return nil
			}
			return err
		}
		switch cmd.Kind {
		case string(protocol.CommandDeploy):
			if cmd.DeploymentID == "" {
				return nil
			}
			dep, err := tx.GetDeployment(ctx, a.OrgID, cmd.DeploymentID)
			if err != nil {
				return err
			}
			if dep.Status == store.DeploymentCancelled {
				return nil
			}
			status, message := DeploymentStatusForResult(res)
			now := time.Now().UTC()
			upd := store.DeploymentUpdate{Status: status, StartedAt: &now, FinishedAt: &now}
			if message != "" {
				upd.Message = &message
			}
			if res.Workload != nil {
				upd.Workload, _ = json.Marshal(res.Workload)
			}
			if _, err := tx.UpdateDeployment(ctx, a.OrgID, dep.ID, upd); err != nil {
				return err
			}
			if err := b.applyScopeForDeployment(ctx, tx, a.OrgID, dep, status); err != nil {
				return err
			}
			payload := map[string]any{"app": dep.AppName, "scope": dep.ScopeName, "image": dep.Image, "result": res.Status, "command_id": cmd.ID}
			if res.Workload != nil {
				payload["workload"] = res.Workload
			}
			text := message
			if text == "" {
				text = fmt.Sprintf("%s/%s quedó %s", dep.AppName, dep.ScopeName, status)
			}
			emit("deployment."+status, "deployment", dep.ID, text, payload)
		case string(protocol.CommandUndeploy):
			if cmd.ScopeID == "" {
				return nil
			}
			sc, err := tx.GetScopeByID(ctx, a.OrgID, cmd.ScopeID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return nil
				}
				return err
			}
			appName := b.appNameTx(ctx, tx, a.OrgID, sc.ApplicationID)
			if res.Status == protocol.ResultSucceeded {
				if sc.Ephemeral {
					if err := tx.DeleteScope(ctx, a.OrgID, sc.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
						return err
					}
				} else if err := tx.ClearScopeDeployment(ctx, a.OrgID, sc.ID); err != nil {
					return err
				}
				emit("scope.deleted", "scope", sc.ID, fmt.Sprintf("scope %s/%s borrado del cluster", appName, sc.Name), map[string]any{"app": appName, "scope": sc.Name, "ephemeral": sc.Ephemeral})
			} else {
				if err := tx.SetScopeStatus(ctx, a.OrgID, sc.ID, store.ScopeFailed, ""); err != nil {
					return err
				}
				emit("scope.failed", "scope", sc.ID, "undeploy falló: "+res.Message, map[string]any{"app": appName, "scope": sc.Name, "command_id": cmd.ID})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, e := range emits {
		e()
	}
	return nil
}

// appName devuelve el nombre de la application o "" si no se pudo cargar. Los
// eventos de scope llevan app y scope en el payload para que la web sepa qué
// pantalla refrescar.
func (b *StoreBackend) appName(ctx context.Context, orgID, appID string) string {
	return b.appNameTx(ctx, b.store, orgID, appID)
}

func (b *StoreBackend) appNameTx(ctx context.Context, st *store.Store, orgID, appID string) string {
	if appID == "" {
		return ""
	}
	app, err := st.GetApplication(ctx, orgID, appID)
	if err != nil {
		return ""
	}
	return app.Name
}
