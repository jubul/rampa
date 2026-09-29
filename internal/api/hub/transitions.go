package hub

import (
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/protocol"
)

// DeploymentStatusForPhase mapea la fase que reporta el agente al estado del
// deployment. Devuelve "" si la fase no cambia el estado.
func DeploymentStatusForPhase(phase protocol.Phase) string {
	switch phase {
	case protocol.PhaseApplying:
		return store.DeploymentApplying
	case protocol.PhaseProgressing:
		return store.DeploymentProgressing
	case protocol.PhaseHealthy:
		return store.DeploymentHealthy
	case protocol.PhaseFailed:
		return store.DeploymentFailed
	}
	return ""
}

// DeploymentStatusForResult mapea el desenlace de un comando deploy al estado
// final del deployment y devuelve el mensaje a guardar.
func DeploymentStatusForResult(res protocol.Result) (status, message string) {
	switch res.Status {
	case protocol.ResultSucceeded:
		return store.DeploymentHealthy, res.Message
	case protocol.ResultTimeout:
		msg := "timeout esperando que el workload esté sano"
		if res.Message != "" {
			msg += ": " + res.Message
		}
		return store.DeploymentFailed, msg
	case protocol.ResultRejected:
		msg := "el agente rechazó el comando"
		if res.Message != "" {
			msg += ": " + res.Message
		}
		return store.DeploymentFailed, msg
	default:
		return store.DeploymentFailed, res.Message
	}
}

// CommandStatusForResult mapea el desenlace al estado de agent_commands.
func CommandStatusForResult(res protocol.Result) string {
	if res.Status == protocol.ResultSucceeded {
		return store.CommandDone
	}
	return store.CommandFailed
}

// ScopeStatusForDeployment mapea el estado del deployment al del scope.
func ScopeStatusForDeployment(status string) string {
	switch status {
	case store.DeploymentHealthy:
		return store.ScopeHealthy
	case store.DeploymentFailed:
		return store.ScopeFailed
	case store.DeploymentQueued, store.DeploymentApplying, store.DeploymentProgressing:
		return store.ScopeDeploying
	}
	return ""
}
