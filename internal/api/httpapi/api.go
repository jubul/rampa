// Package httpapi implementa el contrato REST de docs/api.md: router chi,
// handlers, helpers JSON y el formato de error {"error":{"code","message","details"}}.
package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/config"
	"rampa.dev/internal/api/crypto"
	"rampa.dev/internal/api/deploy"
	"rampa.dev/internal/api/events"
	"rampa.dev/internal/api/hub"
	"rampa.dev/internal/api/infra"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/protocol"
)

// Deps es todo lo que la API necesita cableado.
type Deps struct {
	Store    *store.Store
	Auth     *auth.Authenticator
	Sessions *auth.Sessions
	OIDC     *auth.OIDC // nil si no está configurado
	Bus      *events.Bus
	Hub      *hub.Hub
	Pipeline *deploy.Pipeline
	// Infra es el runner de OpenTofu. nil deshabilita los endpoints /infra (501).
	Infra *infra.Service
	// Cipher es el cifrado del servidor (RAMPA_STATE_KEY) con el que se guardan
	// las credenciales de las cuentas con auth token. nil: esos providers
	// responden 501.
	Cipher  *crypto.Cipher
	Config  config.Config
	Log     *slog.Logger
	Version string
}

// API es el conjunto de handlers.
type API struct {
	Deps
	// bootstrapLimiter acota los canjes de POST /v1/agent/bootstrap por IP y por token.
	bootstrapLimiter *rateLimiter
	// sender reemplaza a Hub.Send cuando no es nil (para tests que simulan una
	// entrega fallida).
	sender atomic.Pointer[commandSender]
}

// commandSender manda un comando en vivo al agente de un cluster.
type commandSender func(clusterID string, cmd protocol.Command) error

// SetCommandSender reemplaza la entrega en vivo de comandos (Hub.Send). Solo
// para tests; nil restaura el Hub.
func (a *API) SetCommandSender(f func(clusterID string, cmd protocol.Command) error) {
	if f == nil {
		a.sender.Store(nil)
		return
	}
	cs := commandSender(f)
	a.sender.Store(&cs)
}

// sendCommand entrega cmd por la conexión viva del cluster.
func (a *API) sendCommand(clusterID string, cmd protocol.Command) error {
	if cs := a.sender.Load(); cs != nil {
		return (*cs)(clusterID, cmd)
	}
	return a.Hub.Send(clusterID, cmd)
}

// Límite de canjes de bootstrap: 10 intentos por minuto por IP y por token.
const (
	BootstrapRateLimit  = 10
	BootstrapRateWindow = time.Minute
)

// New arma la API.
func New(d Deps) *API {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Version == "" {
		d.Version = "dev"
	}
	return &API{Deps: d, bootstrapLimiter: newRateLimiter(BootstrapRateLimit, BootstrapRateWindow)}
}

// Handler devuelve el router con /v1, /healthz, /readyz y /version.
func (a *API) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(trustedProxies(a.Config.TrustedProxies))
	r.Use(requestLogger(a.Log))
	r.Use(recoverer(a.Log))
	// Idioma: Accept-Language para todos; RequireAuth lo pisa con la preferencia del usuario.
	r.Use(auth.LocaleFromHeader)
	if a.Config.DevLogin {
		r.Use(devCORS)
	}
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "ruta no encontrada")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "invalid", "método no permitido")
	})

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", a.readyz)
	r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"version": a.Version, "protocol": protocol.Version})
	})

	r.Route("/v1", a.routesV1)
	return r
}

func (a *API) routesV1(r chi.Router) {
	r.Post("/auth/dev-login", a.devLogin)
	r.Post("/auth/logout", a.logout)
	r.Get("/auth/oidc/start", a.oidcStart)
	r.Get("/auth/oidc/callback", a.oidcCallback)

	if a.Hub != nil {
		r.Get("/agent/connect", a.Hub.ServeHTTP)
	}
	// Canje de un bootstrap token por el token del cluster: sin sesión, con límite por IP y token.
	r.Post("/agent/bootstrap", a.agentBootstrap)
	if a.Infra != nil {
		// Backend http de estado de OpenTofu: autentica con el token del run, no con sesión.
		r.Mount("/tfstate", a.Infra.StateHandler())
	}

	r.Group(func(r chi.Router) {
		r.Use(a.Auth.RequireAuth)
		r.Get("/me", a.me)
		r.Patch("/me", a.updateMe)
		r.Get("/orgs", a.listOrgs)
		r.Post("/orgs", a.createOrg)

		r.Route("/orgs/{org}", func(r chi.Router) {
			r.Use(a.Auth.RequireOrg)
			admin := a.Auth.RequireRole(auth.RoleAdmin)
			developer := a.Auth.RequireRole(auth.RoleDeveloper)
			owner := a.Auth.RequireRole(auth.RoleOwner)

			r.Get("/", a.getOrg)
			r.With(admin).Patch("/", a.updateOrg)
			r.With(owner).Delete("/", a.deleteOrg)

			r.Get("/members", a.listMembers)
			r.With(admin).Post("/members", a.setMember)
			r.With(admin).Delete("/members/{user_id}", a.removeMember)

			r.With(admin).Get("/tokens", a.listTokens)
			r.With(admin).Post("/tokens", a.createToken)
			r.With(admin).Delete("/tokens/{id}", a.deleteToken)

			r.Get("/providers", a.listProviders)

			r.Get("/cloud-accounts", a.listCloudAccounts)
			r.With(admin).Post("/cloud-accounts", a.createCloudAccount)
			r.Get("/cloud-accounts/{id}", a.getCloudAccount)
			r.With(admin).Patch("/cloud-accounts/{id}", a.updateCloudAccount)
			r.With(admin).Post("/cloud-accounts/{id}/verify", a.verifyCloudAccount)
			r.With(admin).Delete("/cloud-accounts/{id}", a.deleteCloudAccount)

			r.Get("/clusters", a.listClusters)
			r.With(admin).Post("/clusters", a.createCluster)
			r.Get("/clusters/{id}", a.getCluster)
			r.With(admin).Delete("/clusters/{id}", a.deleteCluster)
			r.With(admin).Post("/clusters/{id}/rotate-token", a.rotateClusterToken)
			r.With(admin).Post("/clusters/{id}/bootstrap-token", a.createBootstrapToken)
			r.With(admin).Get("/clusters/{id}/install-command", a.clusterInstallCommand)

			r.Get("/onboarding", a.onboarding)

			r.Get("/apps", a.listApps)
			r.With(developer).Post("/apps", a.importApp)
			r.With(developer).Post("/apps/preview", a.previewApp)
			r.With(developer).Post("/apps/from-spec", a.createAppFromSpec)
			r.Get("/apps/{app}", a.getApp)
			r.With(developer).Put("/apps/{app}/manifest", a.updateAppManifest)
			r.Get("/apps/{app}/spec", a.getAppSpec)
			r.With(developer).Put("/apps/{app}/spec", a.updateAppSpec)
			r.Get("/apps/{app}/scaffold.zip", a.scaffoldZip)
			r.With(developer).Delete("/apps/{app}", a.deleteApp)

			r.Get("/infra/modules", a.listInfraModules)
			r.With(admin).Post("/infra/clusters", a.createInfraCluster)
			r.Get("/infra/runs", a.listInfraRuns)
			r.Get("/infra/runs/{id}", a.getInfraRun)
			r.Get("/infra/runs/{id}/log", a.infraRunLog)
			r.With(admin).Post("/infra/runs/{id}/destroy", a.destroyInfraRun)
			r.With(admin).Post("/infra/runs/{id}/retry", a.retryInfraRun)

			r.Get("/apps/{app}/scopes", a.listScopes)
			r.With(developer).Put("/apps/{app}/scopes/{scope}", a.bindScope)
			r.With(developer).Delete("/apps/{app}/scopes/{scope}", a.deleteScope)
			r.With(developer).Put("/apps/{app}/scopes/{scope}/secrets", a.putScopeSecrets)
			r.Get("/apps/{app}/scopes/{scope}/secrets", a.getScopeSecrets)

			r.With(developer).Post("/apps/{app}/scopes/{scope}/deployments", a.createDeployment)
			r.Get("/apps/{app}/scopes/{scope}/deployments", a.listDeployments)
			r.Get("/deployments/{id}", a.getDeployment)
			r.With(admin).Post("/deployments/{id}/approve", a.approveDeployment)
			r.With(developer).Post("/deployments/{id}/cancel", a.cancelDeployment)

			r.Get("/events", a.listEvents)
			r.Get("/events/stream", a.streamEvents)
		})
	})
}

func (a *API) readyz(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "internal", "la base de datos no responde")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// requestLogger loguea cada request con slog. No loguea headers ni cuerpos.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
			)
		})
	}
}

// recoverer convierte un panic en un 500 con el formato de la API.
func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					log.Error("panic en handler", "method", r.Method, "path", r.URL.Path, "panic", rec)
					writeError(w, http.StatusInternalServerError, "internal", "error interno")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// devCORS permite que la web corriendo en el dev server de Vite (otro puerto
// de localhost) hable con la API. Solo se activa con RAMPA_DEV_LOGIN=true.
func devCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:")) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
