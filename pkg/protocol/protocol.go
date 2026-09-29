// Package protocol define los mensajes entre el plano de control (API) y el
// agente que corre en el cluster del cliente. JSON sobre WebSocket.
//
// El agente abre la conexión (saliente) y manda Hello. La API responde Welcome.
// Después la API manda Command y el agente responde Ack al recibirlo, cero o más
// Event mientras trabaja, y exactamente un Result al terminar. El agente manda
// Heartbeat cada HeartbeatSeconds. Cualquiera puede mandar Error.
//
// Todo lo que el agente aplica es lo que la API le manda ya renderizado
// (objetos de Kubernetes en JSON). El agente no conoce el chart ni el rampa.yaml.
package protocol

import (
	"encoding/json"
	"time"
)

// Version del protocolo. Se sube cuando hay un cambio incompatible.
const Version = 1

// DefaultHeartbeatSeconds es el intervalo de heartbeat que la API pide por defecto.
const DefaultHeartbeatSeconds = 30

// Kind es el tipo de mensaje del sobre.
type Kind string

const (
	KindHello     Kind = "hello"     // agente -> API, primer mensaje al conectar
	KindWelcome   Kind = "welcome"   // API -> agente, aceptado
	KindHeartbeat Kind = "heartbeat" // agente -> API, periódico
	KindCommand   Kind = "command"   // API -> agente
	KindAck       Kind = "ack"       // agente -> API, comando recibido
	KindEvent     Kind = "event"     // agente -> API, progreso de un comando
	KindLog       Kind = "log"       // agente -> API, líneas de log de un comando logs
	KindResult    Kind = "result"    // agente -> API, comando terminado
	KindError     Kind = "error"     // cualquiera, error de protocolo
)

// Envelope es el sobre de todo mensaje. Payload se decodifica según Kind.
type Envelope struct {
	Kind      Kind            `json:"kind"`
	ID        string          `json:"id,omitempty"`         // id del mensaje (uuid)
	CommandID string          `json:"command_id,omitempty"` // para ack, event, log, result
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// NewEnvelope serializa un payload en un sobre.
func NewEnvelope(kind Kind, id, commandID string, payload any) (Envelope, error) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		raw = b
	}
	return Envelope{Kind: kind, ID: id, CommandID: commandID, Payload: raw}, nil
}

// Hello lo manda el agente al conectar. La autenticación va en el header
// Authorization: Bearer <token del cluster>, no en el mensaje.
type Hello struct {
	Protocol          int      `json:"protocol"`
	AgentVersion      string   `json:"agent_version"`
	KubernetesVersion string   `json:"kubernetes_version"`
	Capabilities      []string `json:"capabilities"` // "deploy", "undeploy", "status", "logs"
	// InFlight son los ids de comandos que el agente todavía está ejecutando al
	// reconectar. La API marca como failed cualquier comando acked de este cluster
	// que no esté en la lista: su result se perdió (el agente se reinició).
	InFlight []string `json:"in_flight,omitempty"`
}

// Welcome lo manda la API si acepta al agente.
type Welcome struct {
	ClusterID        string `json:"cluster_id"`
	ClusterName      string `json:"cluster_name"`
	OrgSlug          string `json:"org_slug"`
	HeartbeatSeconds int    `json:"heartbeat_seconds"`
	PendingCommands  int    `json:"pending_commands"`
	ServerTime       string `json:"server_time"` // RFC3339
}

// Heartbeat es el latido periódico del agente.
type Heartbeat struct {
	Nodes             int    `json:"nodes"`
	KubernetesVersion string `json:"kubernetes_version,omitempty"`
	AgentVersion      string `json:"agent_version,omitempty"`
}

// CommandKind es el tipo de comando.
type CommandKind string

const (
	CommandDeploy   CommandKind = "deploy"
	CommandUndeploy CommandKind = "undeploy"
	CommandStatus   CommandKind = "status"
	CommandLogs     CommandKind = "logs"
)

// Command es una orden de la API. Exactamente uno de los punteros está seteado según Kind.
type Command struct {
	ID       string           `json:"id"`
	Kind     CommandKind      `json:"kind"`
	Deploy   *DeployCommand   `json:"deploy,omitempty"`
	Undeploy *UndeployCommand `json:"undeploy,omitempty"`
	Status   *StatusCommand   `json:"status,omitempty"`
	Logs     *LogsCommand     `json:"logs,omitempty"`
}

// DeployCommand: aplicar objetos en un namespace y esperar a que el workload esté sano.
//
// El agente:
//  1. Crea el namespace si no existe, con Labels.
//  2. Agrega Labels a cada objeto y los aplica con server-side apply (field manager "rampa", force).
//  3. Borra del namespace los objetos con label rampa.dev/app=<App> cuya label
//     rampa.dev/deployment sea distinta de DeploymentID (prune), en los kinds que gestiona.
//  4. Observa el workload (Deployment, Rollout o CronJob llamado App) hasta healthy, failed o TimeoutSeconds.
//     Manda Event con cada cambio relevante y un Result final.
type DeployCommand struct {
	DeploymentID   string            `json:"deployment_id"`
	App            string            `json:"app"`
	Scope          string            `json:"scope"`
	Namespace      string            `json:"namespace"`
	Labels         map[string]string `json:"labels"`  // incluye rampa.dev/app, rampa.dev/scope, rampa.dev/deployment
	Objects        []json.RawMessage `json:"objects"` // objetos de Kubernetes en JSON, ya renderizados
	TimeoutSeconds int               `json:"timeout_seconds"`
}

// UndeployCommand: borrar el namespace del scope. Como scope = namespace, esto lo borra todo.
type UndeployCommand struct {
	App       string `json:"app"`
	Scope     string `json:"scope"`
	Namespace string `json:"namespace"`
	Wait      bool   `json:"wait"` // esperar a que el namespace desaparezca
}

// StatusCommand: reportar el estado actual del workload de un scope.
type StatusCommand struct {
	App       string `json:"app"`
	Namespace string `json:"namespace"`
}

// LogsCommand: mandar las últimas líneas de log de los pods del workload, opcionalmente seguir.
type LogsCommand struct {
	App             string `json:"app"`
	Namespace       string `json:"namespace"`
	Container       string `json:"container,omitempty"` // default "app"
	TailLines       int    `json:"tail_lines"`
	Follow          bool   `json:"follow"`
	DurationSeconds int    `json:"duration_seconds"` // cuánto seguir como máximo
}

// Ack confirma que el agente recibió el comando y empezó a trabajar.
type Ack struct {
	CommandID string `json:"command_id"`
}

// Phase es la fase de un deployment tal como la ve el agente.
type Phase string

const (
	PhaseApplying    Phase = "applying"
	PhaseProgressing Phase = "progressing"
	PhaseHealthy     Phase = "healthy"
	PhaseFailed      Phase = "failed"
)

// Event es un hito durante la ejecución de un comando.
type Event struct {
	CommandID string    `json:"command_id"`
	Time      time.Time `json:"time"`
	Phase     Phase     `json:"phase,omitempty"`
	Reason    string    `json:"reason,omitempty"` // Applied, Pruned, Progressing, Unhealthy, ImagePullBackOff...
	Object    string    `json:"object,omitempty"` // kind/nombre
	Message   string    `json:"message"`
}

// LogChunk son líneas de log de un pod.
type LogChunk struct {
	CommandID string   `json:"command_id"`
	Pod       string   `json:"pod"`
	Container string   `json:"container"`
	Lines     []string `json:"lines"`
	EOF       bool     `json:"eof"`
}

// ResultStatus es el desenlace de un comando.
type ResultStatus string

const (
	ResultSucceeded ResultStatus = "succeeded"
	ResultFailed    ResultStatus = "failed"
	ResultTimeout   ResultStatus = "timeout"
	ResultRejected  ResultStatus = "rejected" // el agente no puede ejecutarlo (capability, payload inválido)
)

// Result cierra un comando.
type Result struct {
	CommandID string          `json:"command_id"`
	Status    ResultStatus    `json:"status"`
	Message   string          `json:"message,omitempty"`
	Workload  *WorkloadStatus `json:"workload,omitempty"` // para deploy y status
}

// WorkloadStatus es el estado observado del workload de un scope.
type WorkloadStatus struct {
	Kind      string `json:"kind"` // Deployment, Rollout, CronJob
	Phase     Phase  `json:"phase"`
	Desired   int32  `json:"desired"`
	Ready     int32  `json:"ready"`
	Updated   int32  `json:"updated"`
	Available int32  `json:"available"`
	Image     string `json:"image,omitempty"`
	Message   string `json:"message,omitempty"`
}

// Error es un error de protocolo.
type Error struct {
	Code    string `json:"code"` // unauthorized, bad_payload, unsupported, internal
	Message string `json:"message"`
}
