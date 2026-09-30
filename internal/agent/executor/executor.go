// Package executor traduce los comandos del protocolo a operaciones de kube y
// arma el Result. Es el pegamento entre client (transporte) y kube (cluster).
package executor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"rampa.dev/internal/agent/client"
	"rampa.dev/internal/agent/kube"
	"rampa.dev/pkg/protocol"
)

// Kube es lo que el ejecutor necesita del cluster. *kube.Client lo implementa.
type Kube interface {
	Deploy(ctx context.Context, cmd *protocol.DeployCommand, report kube.Reporter) (*protocol.WorkloadStatus, error)
	Undeploy(ctx context.Context, cmd *protocol.UndeployCommand, report kube.Reporter) error
	Status(ctx context.Context, cmd *protocol.StatusCommand) (*protocol.WorkloadStatus, error)
	Logs(ctx context.Context, cmd *protocol.LogsCommand, sink kube.LogSink) error
	// WriteSecret escribe el Secret <app>-params (target kubernetes) y devuelve las claves escritas.
	WriteSecret(ctx context.Context, cmd *protocol.WriteSecretCommand) ([]string, error)
}

// CloudSecrets escribe secretos en el gestor de secretos de la nube (target
// cloud) con la identidad de workload del agente. *cloud.AWSSecretsManager lo
// implementa.
type CloudSecrets interface {
	WriteSecret(ctx context.Context, cmd *protocol.WriteSecretCommand) ([]string, error)
}

// Executor implementa client.Executor sobre un cluster.
type Executor struct {
	Kube Kube
	// Cloud es opcional: sin él, write_secret con target cloud falla con un mensaje claro.
	Cloud CloudSecrets
	Log   *slog.Logger
	// DefaultDeployTimeout se usa si el comando no trae timeout_seconds. Default 10m.
	DefaultDeployTimeout time.Duration
}

// Execute corre el comando y devuelve exactamente un Result.
func (e *Executor) Execute(ctx context.Context, cmd protocol.Command, sink client.Sink) protocol.Result {
	log := e.logger().With("command_id", cmd.ID, "kind", cmd.Kind)
	report := func(phase protocol.Phase, reason, object, message string) {
		if err := sink.Event(protocol.Event{Time: time.Now().UTC(), Phase: phase, Reason: reason, Object: object, Message: message}); err != nil {
			log.Debug("no pude mandar el evento", "reason", reason, "err", err)
		}
	}
	res := protocol.Result{CommandID: cmd.ID}
	switch cmd.Kind {
	case protocol.CommandDeploy:
		if cmd.Deploy == nil {
			return rejected(res, "el comando deploy no trae payload")
		}
		if cmd.Deploy.TimeoutSeconds <= 0 && e.DefaultDeployTimeout > 0 {
			cmd.Deploy.TimeoutSeconds = int(e.DefaultDeployTimeout / time.Second)
		}
		status, err := e.Kube.Deploy(ctx, cmd.Deploy, report)
		res.Workload = status
		switch {
		case err == nil:
			res.Status = protocol.ResultSucceeded
			res.Message = fmt.Sprintf("%s/%s sano", status.Kind, cmd.Deploy.App)
		case errors.Is(err, kube.ErrTimeout):
			res.Status = protocol.ResultTimeout
			res.Message = fmt.Sprintf("el workload no estuvo sano en %ds: %v", cmd.Deploy.TimeoutSeconds, err)
		default:
			res.Status = protocol.ResultFailed
			res.Message = err.Error()
			if status != nil && status.Phase == "" {
				status.Phase = protocol.PhaseFailed
			}
		}
	case protocol.CommandUndeploy:
		if cmd.Undeploy == nil {
			return rejected(res, "el comando undeploy no trae payload")
		}
		if err := e.Kube.Undeploy(ctx, cmd.Undeploy, report); err != nil {
			if errors.Is(err, kube.ErrTimeout) {
				res.Status = protocol.ResultTimeout
			} else {
				res.Status = protocol.ResultFailed
			}
			res.Message = err.Error()
			break
		}
		res.Status = protocol.ResultSucceeded
		res.Message = fmt.Sprintf("namespace %s borrado", cmd.Undeploy.Namespace)
	case protocol.CommandStatus:
		if cmd.Status == nil {
			return rejected(res, "el comando status no trae payload")
		}
		status, err := e.Kube.Status(ctx, cmd.Status)
		if err != nil {
			res.Status = protocol.ResultFailed
			res.Message = err.Error()
			break
		}
		res.Status = protocol.ResultSucceeded
		res.Workload = status
		res.Message = string(status.Phase)
	case protocol.CommandLogs:
		if cmd.Logs == nil {
			return rejected(res, "el comando logs no trae payload")
		}
		err := e.Kube.Logs(ctx, cmd.Logs, func(chunk protocol.LogChunk) error {
			chunk.CommandID = cmd.ID
			return sink.Log(chunk)
		})
		if err != nil {
			res.Status = protocol.ResultFailed
			res.Message = err.Error()
			break
		}
		res.Status = protocol.ResultSucceeded
		res.Message = "logs enviados"
	case protocol.CommandWriteSecret:
		return e.writeSecret(ctx, cmd, res, report)
	default:
		return rejected(res, fmt.Sprintf("comando %q no soportado por este agente", cmd.Kind))
	}
	if ctx.Err() != nil && res.Status == protocol.ResultFailed {
		res.Message = "el agente se está apagando: " + res.Message
	}
	return res
}

// writeSecret ejecuta write_secret según el target. El result y el evento
// llevan solo las claves: los valores no salen del comando.
func (e *Executor) writeSecret(ctx context.Context, cmd protocol.Command, res protocol.Result, report kube.Reporter) protocol.Result {
	ws := cmd.WriteSecret
	if ws == nil {
		return rejected(res, "el comando write_secret no trae payload")
	}
	if len(ws.Values) == 0 {
		return rejected(res, "el comando write_secret no trae valores")
	}
	var (
		keys        []string
		err         error
		destination string
	)
	switch ws.Target {
	case kube.TargetKubernetes:
		destination = fmt.Sprintf("Secret %s/%s", ws.Namespace, kube.ParamsSecretName(ws.App))
		keys, err = e.Kube.WriteSecret(ctx, ws)
	case kube.TargetCloud:
		destination = fmt.Sprintf("el gestor de secretos de la nube (rampa/%s/%s/*)", ws.App, ws.Scope)
		if e.Cloud == nil {
			err = errors.New("este agente no tiene configurado un gestor de secretos de nube; usá secret_store kubernetes en el cluster o instalá el agente con identidad de nube")
			break
		}
		keys, err = e.Cloud.WriteSecret(ctx, ws)
	default:
		return rejected(res, fmt.Sprintf("target %q de write_secret no soportado (kubernetes, cloud)", ws.Target))
	}
	if err != nil {
		res.Status = protocol.ResultFailed
		res.Message = err.Error()
		if ws.Target == kube.TargetCloud && len(keys) > 0 {
			res.Message += "; claves escritas antes del fallo: " + strings.Join(keys, ", ")
		}
		if ctx.Err() != nil {
			res.Message = "el agente se está apagando: " + res.Message
		}
		return res
	}
	list := strings.Join(keys, ", ")
	report(protocol.PhaseHealthy, "SecretWritten", kube.ParamsSecretName(ws.App), fmt.Sprintf("claves escritas en %s: %s", destination, list))
	res.Status = protocol.ResultSucceeded
	res.Message = fmt.Sprintf("%d claves escritas en %s: %s", len(keys), destination, list)
	return res
}

func rejected(res protocol.Result, msg string) protocol.Result {
	res.Status = protocol.ResultRejected
	res.Message = msg
	return res
}

func (e *Executor) logger() *slog.Logger {
	if e.Log == nil {
		return slog.Default()
	}
	return e.Log
}
