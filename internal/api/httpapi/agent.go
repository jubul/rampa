package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/store"
)

// bootstrapResponse es la respuesta de POST /v1/agent/bootstrap.
type bootstrapResponse struct {
	ClusterToken string `json:"cluster_token"`
	ClusterID    string `json:"cluster_id"`
	APIURL       string `json:"api_url"`
}

// agentBootstrap canjea un bootstrap token de un solo uso por el token del
// cluster (ADR 0004). Sin sesión: la credencial es el token. Valida hash, no
// usado y no vencido en un solo UPDATE (dos canjes concurrentes no ganan los
// dos), marca used_at y genera un token de cluster NUEVO: si ya había uno, el
// anterior deja de valer y la conexión viva del agente viejo se corta. Los
// fallos se loguean con la IP y el motivo, nunca con el token. 10 intentos por
// minuto por IP y por token; el exceso responde 429.
func (a *API) agentBootstrap(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !a.bootstrapLimiter.Allow("ip:" + ip) {
		a.Log.Warn("bootstrap: demasiados intentos desde la IP", "ip", ip)
		a.fail(w, r, tooManyRequests("demasiados intentos de bootstrap desde esta dirección; esperá un minuto"), "")
		return
	}
	var body struct {
		BootstrapToken string `json:"bootstrap_token"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	token := strings.TrimSpace(body.BootstrapToken)
	if !auth.IsBootstrapToken(token) {
		a.Log.Warn("bootstrap rechazado: el token no tiene formato de bootstrap", "ip", ip)
		writeError(w, http.StatusUnauthorized, "unauthorized", "bootstrap_token inválido: se espera rampa_bootstrap_<...>")
		return
	}
	hash := auth.HashToken(token)
	if !a.bootstrapLimiter.Allow("token:" + hash) {
		a.Log.Warn("bootstrap: demasiados intentos con el mismo token", "ip", ip, "token_hash_prefix", hash[:8])
		a.fail(w, r, tooManyRequests("demasiados intentos con este bootstrap token; esperá un minuto"), "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var cl *store.Cluster
	var plain string
	err := a.Store.WithTx(ctx, func(tx *store.Store) error {
		bt, err := tx.ConsumeBootstrapToken(ctx, hash)
		if err != nil {
			return err
		}
		c, err := tx.GetCluster(ctx, bt.OrgID, bt.ClusterID)
		if err != nil {
			return err
		}
		p, h, err := auth.NewClusterToken()
		if err != nil {
			return err
		}
		if c, err = tx.RotateClusterToken(ctx, c.OrgID, c.ID, h); err != nil {
			return err
		}
		cl, plain = c, p
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.Log.Warn("bootstrap rechazado", "ip", ip, "reason", a.bootstrapFailureReason(ctx, hash))
			writeError(w, http.StatusUnauthorized, "unauthorized", "bootstrap token inválido, ya usado o vencido; generá uno nuevo desde Rampa")
			return
		}
		a.fail(w, r, err, "bootstrap")
		return
	}
	// La conexión viva (si la hay) se autenticó con el token anterior, que ya no vale.
	if a.Hub != nil {
		a.Hub.Disconnect(cl.ID, "el token del cluster fue canjeado por bootstrap")
	}
	a.Log.Info("bootstrap canjeado", "ip", ip, "cluster_id", cl.ID, "org_id", cl.OrgID)
	a.Bus.Emit(ctx, cl.OrgID, store.Actor{Type: "agent", ID: cl.ID, Name: cl.Name}, "cluster.token_rotated", "cluster", cl.ID,
		"token del cluster "+cl.Name+" emitido por bootstrap; el anterior ya no vale", map[string]any{"via": "bootstrap", "kind": cl.Kind})
	writeJSON(w, http.StatusOK, bootstrapResponse{ClusterToken: plain, ClusterID: cl.ID, APIURL: a.Config.PublicURL})
}

// bootstrapFailureReason distingue en el log por qué falló un canje. Nunca
// devuelve el token.
func (a *API) bootstrapFailureReason(ctx context.Context, hash string) string {
	bt, err := a.Store.GetBootstrapTokenByHash(ctx, hash)
	switch {
	case err != nil:
		return "desconocido"
	case bt.UsedAt != nil:
		return "ya usado"
	case !time.Now().Before(bt.ExpiresAt):
		return "vencido"
	default:
		return "no canjeable"
	}
}

// clientIP es el host de r.RemoteAddr sin puerto. El middleware trustedProxies
// ya lo reemplazó por la IP real del cliente cuando la petición llegó por un
// proxy de confianza; los headers de reenvío nunca se leen acá.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
