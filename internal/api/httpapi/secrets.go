package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/hub"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/protocol"
)

// Targets del comando write_secret (pkg/protocol).
const (
	secretTargetKubernetes = "kubernetes"
	secretTargetCloud      = "cloud"
)

// redactedWriteSecret es lo que se persiste en agent_commands para un
// write_secret: el comando sin values, con las claves en su lugar.
type redactedWriteSecret struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	WriteSecret struct {
		App       string   `json:"app"`
		Scope     string   `json:"scope"`
		Namespace string   `json:"namespace"`
		Target    string   `json:"target"`
		Keys      []string `json:"keys"`
	} `json:"write_secret"`
}

// putScopeSecrets recibe secretos de aplicación para un scope y se los manda al
// agente del cluster en un write_secret, solo por la conexión viva. La API no
// persiste los valores: en agent_commands queda el comando con las claves, en
// scope_secret_keys las claves para el GET, y el evento lleva solo las claves.
func (a *API) putScopeSecrets(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	scopeName := chi.URLParam(r, "scope")
	var body struct {
		Values map[string]string `json:"values"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	if len(body.Values) == 0 {
		a.fail(w, r, invalid("values es requerido: {\"API_KEY\": \"...\"}"), "")
		return
	}
	keys := make([]string, 0, len(body.Values))
	for k := range body.Values {
		if err := manifest.ValidateParameterName(k); err != nil {
			a.fail(w, r, invalid("values."+k+" "+err.Error()), "")
			return
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if err := auth.CheckRole(r.Context(), auth.RequiredRoleForScope(scopeName)); err != nil {
		a.fail(w, r, err, "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	appRow, m, err := a.loadApp(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	if _, _, err := resolveScopeSpec(m, scopeName); err != nil {
		a.fail(w, r, err, "")
		return
	}
	row, err := a.Store.GetScope(ctx, oc.Org.ID, appRow.ID, scopeName)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.fail(w, r, err, "scope")
		return
	}
	if row == nil || row.ClusterID == "" {
		a.fail(w, r, invalid(fmt.Sprintf("el scope %s no está atado a ningún cluster; hacé PUT /apps/%s/scopes/%s primero", scopeName, appRow.Name, scopeName)), "")
		return
	}
	cl, err := a.Store.GetCluster(ctx, oc.Org.ID, row.ClusterID)
	if err != nil {
		a.fail(w, r, err, "cluster")
		return
	}
	if a.Hub == nil || !a.Hub.Online(cl.ID) {
		a.fail(w, r, conflict(fmt.Sprintf("el agente del cluster %s está offline; los secretos se entregan solo por la conexión viva, reintentá cuando esté online", cl.Name)), "")
		return
	}
	target := secretTargetCloud
	if cl.SecretStore == store.SecretStoreKubernetes {
		target = secretTargetKubernetes
	}
	cmd := protocol.Command{
		ID:   uuid.NewString(),
		Kind: protocol.CommandWriteSecret,
		WriteSecret: &protocol.WriteSecretCommand{
			App: appRow.Name, Scope: scopeName, Namespace: m.Namespace(scopeName), Target: target, Values: body.Values,
		},
	}
	var redacted redactedWriteSecret
	redacted.ID, redacted.Kind = cmd.ID, string(cmd.Kind)
	redacted.WriteSecret.App, redacted.WriteSecret.Scope = appRow.Name, scopeName
	redacted.WriteSecret.Namespace, redacted.WriteSecret.Target, redacted.WriteSecret.Keys = cmd.WriteSecret.Namespace, target, keys
	payload, err := json.Marshal(redacted)
	if err != nil {
		a.fail(w, r, err, "comando")
		return
	}
	if _, err := a.Store.CreateAgentCommand(ctx, &store.AgentCommand{
		ID: cmd.ID, OrgID: oc.Org.ID, ClusterID: cl.ID, Kind: string(protocol.CommandWriteSecret), Payload: payload, ScopeID: row.ID,
	}); err != nil {
		a.fail(w, r, err, "secretos")
		return
	}
	// Entrega en vivo. Si la conexión se cayó entre el chequeo y el envío, el
	// comando se cierra como failed y la respuesta es 409: nada quedó a medias
	// y las claves no se registran, porque nunca se escribieron.
	if err := a.sendCommand(cl.ID, cmd); err != nil {
		res, _ := json.Marshal(map[string]string{"status": "failed", "message": "el agente se desconectó antes de recibir el comando"})
		if ferr := a.Store.FinishAgentCommand(ctx, oc.Org.ID, cl.ID, cmd.ID, store.CommandFailed, res); ferr != nil {
			a.Log.Error("cerrando el write_secret no entregado", "command_id", cmd.ID, "err", ferr)
		}
		if errors.Is(err, hub.ErrOffline) {
			a.fail(w, r, conflict(fmt.Sprintf("el agente del cluster %s se desconectó; los secretos se entregan solo por la conexión viva", cl.Name)), "")
			return
		}
		a.fail(w, r, err, "secretos")
		return
	}
	// Recién con el comando entregado las claves pasan a ser visibles en el GET.
	if err := a.Store.UpsertScopeSecretKeys(ctx, oc.Org.ID, row.ID, keys); err != nil {
		a.Log.Error("registrando las claves del scope tras entregar el write_secret", "scope_id", row.ID, "command_id", cmd.ID, "err", err)
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "scope.secrets_written", "scope", row.ID,
		fmt.Sprintf("%d secreto(s) de %s/%s enviados al agente de %s (%s)", len(keys), appRow.Name, scopeName, cl.Name, target),
		map[string]any{"app": appRow.Name, "scope": scopeName, "keys": keys, "target": target, "cluster_id": cl.ID, "command_id": cmd.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"keys": keys})
}

// getScopeSecrets devuelve las claves conocidas de un scope (sin valores).
func (a *API) getScopeSecrets(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	scopeName := chi.URLParam(r, "scope")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	appRow, m, err := a.loadApp(ctx, oc.Org.ID, chi.URLParam(r, "app"))
	if err != nil {
		a.fail(w, r, err, "application")
		return
	}
	if _, _, err := resolveScopeSpec(m, scopeName); err != nil {
		a.fail(w, r, err, "")
		return
	}
	row, err := a.Store.GetScope(ctx, oc.Org.ID, appRow.ID, scopeName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"keys": []string{}})
			return
		}
		a.fail(w, r, err, "scope")
		return
	}
	keys, err := a.Store.ListScopeSecretKeys(ctx, oc.Org.ID, row.ID)
	if err != nil {
		a.fail(w, r, err, "secretos")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}
