// Package server cablea el plano de control: base, migraciones, bus de
// eventos, hub de agentes, API REST, SPA y rutas públicas.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/config"
	"rampa.dev/internal/api/deploy"
	"rampa.dev/internal/api/events"
	"rampa.dev/internal/api/httpapi"
	"rampa.dev/internal/api/hub"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/provider/aws"
)

// CloudFormationPath es la ruta pública de la plantilla de AWS.
const CloudFormationPath = "/public/cloudformation/rampa-connect.yaml"

// Server es el plano de control listo para servir.
type Server struct {
	cfg     config.Config
	log     *slog.Logger
	store   *store.Store
	hub     *hub.Hub
	backend *hub.StoreBackend
	bus     *events.Bus
	handler http.Handler
}

// Parámetros del reaper de comandos: cada cuánto corre y cuánto margen da
// por encima del timeout_seconds del payload antes de dar un comando por perdido.
const (
	ReaperInterval = time.Minute
	ReaperGrace    = 2 * time.Minute
)

// New abre la base, corre migraciones y arma todos los componentes.
func New(ctx context.Context, cfg config.Config, log *slog.Logger, version string) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx, st.Pool(), log); err != nil {
		st.Close()
		return nil, fmt.Errorf("migrando: %w", err)
	}
	// Ninguna conexión de agente sobrevive un reinicio de la API.
	if err := st.MarkAllClustersOffline(ctx); err != nil {
		st.Close()
		return nil, fmt.Errorf("marcando clusters offline: %w", err)
	}

	pipeline, err := deploy.New(cfg.DeployTimeout)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("cargando el chart: %w", err)
	}

	bus := events.New(st, log.With("component", "events"))
	secure := strings.HasPrefix(cfg.PublicURL, "https://")
	sessions := auth.NewSessions(cfg.SessionSecret, auth.DefaultSessionTTL, secure)
	authn := auth.NewAuthenticator(st, sessions, log.With("component", "auth"), httpapi.WriteError)
	var oidc *auth.OIDC
	if cfg.OIDCEnabled() {
		oidc = auth.NewOIDC(auth.OIDCConfig{
			Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
			RedirectURL: cfg.PublicURL + "/v1/auth/oidc/callback",
		}, sessions, secure)
		log.Info("login OIDC habilitado", "issuer", cfg.OIDCIssuer)
	}
	if cfg.DevLogin {
		log.Warn("RAMPA_DEV_LOGIN=true: POST /v1/auth/dev-login abre sesión sin contraseña. Solo para desarrollo.")
	}

	backend := hub.NewStoreBackend(st, bus, log.With("component", "hub"))
	h := hub.New(backend, log.With("component", "hub"), hub.Options{})

	api := httpapi.New(httpapi.Deps{
		Store: st, Auth: authn, Sessions: sessions, OIDC: oidc, Bus: bus, Hub: h, Pipeline: pipeline,
		Config: cfg, Log: log.With("component", "http"), Version: version,
	})

	apiHandler := api.Handler()
	var spa http.Handler
	if cfg.WebDir != "" {
		spa = spaHandler(cfg.WebDir, log)
		log.Info("sirviendo la SPA", "dir", cfg.WebDir)
	}
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == CloudFormationPath:
			serveCloudFormation(w, r)
		case path == "/v1" || strings.HasPrefix(path, "/v1/") || path == "/healthz" || path == "/readyz" || path == "/version":
			apiHandler.ServeHTTP(w, r)
		case spa != nil:
			spa.ServeHTTP(w, r)
		default:
			apiHandler.ServeHTTP(w, r)
		}
	})

	return &Server{cfg: cfg, log: log, store: st, hub: h, backend: backend, bus: bus, handler: root}, nil
}

// serveCloudFormation entrega la plantilla embebida en pkg/provider/aws.
func serveCloudFormation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(aws.CloudFormationTemplate)
}

// spaHandler sirve archivos estáticos con fallback a index.html para las
// rutas del router del frontend. Nunca hace fallback para /v1 ni /public.
func spaHandler(dir string, log *slog.Logger) http.HandlerFunc {
	fs := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/public/") {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "ruta no encontrada")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			httpapi.WriteError(w, http.StatusMethodNotAllowed, "invalid", "método no permitido")
			return
		}
		clean := filepath.Clean("/" + r.URL.Path)
		if clean == "/" || strings.HasSuffix(clean, "/index.html") {
			serveIndex(w, r, index, log)
			return
		}
		full := filepath.Join(dir, clean)
		if !strings.HasPrefix(full, filepath.Clean(dir)) {
			http.NotFound(w, r)
			return
		}
		if st, err := os.Stat(full); err == nil && !st.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r, index, log)
	}
}

// serveIndex entrega index.html sin la redirección ni el rechazo de ".." de
// http.ServeFile: la ruta del navegador es cosa del router del frontend.
func serveIndex(w http.ResponseWriter, r *http.Request, index string, log *slog.Logger) {
	f, err := os.Open(index)
	if err != nil {
		if log != nil {
			log.Error("no pude abrir index.html", "path", index, "err", err)
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "internal", "la SPA no está disponible")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal", "la SPA no está disponible")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", st.ModTime(), f)
}

// Handler devuelve el handler raíz (para tests).
func (s *Server) Handler() http.Handler { return s.handler }

// Store expone el store (para tests y el comando migrate).
func (s *Server) Store() *store.Store { return s.store }

// Hub expone el hub (para tests).
func (s *Server) Hub() *hub.Hub { return s.hub }

// Backend expone el backend del hub (para tests del reaper).
func (s *Server) Backend() *hub.StoreBackend { return s.backend }

// Run escucha en cfg.Listen hasta que ctx se cancela y apaga con gracia.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	// Reaper: cierra como failed los comandos sent o acked cuyo result nunca
	// llegó, para que ningún deployment quede en applying para siempre.
	go s.backend.RunReaper(ctx, ReaperInterval, s.cfg.DeployTimeout, ReaperGrace)

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("API escuchando", "addr", s.cfg.Listen, "public_url", s.cfg.PublicURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("sirviendo HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	s.log.Info("apagando la API")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s.hub.Shutdown(shutdownCtx)
	if err := srv.Shutdown(shutdownCtx); err != nil {
		s.log.Warn("shutdown HTTP con error", "err", err)
	}
	return nil
}

// Close libera la base.
func (s *Server) Close() {
	if s.store != nil {
		s.store.Close()
	}
}
