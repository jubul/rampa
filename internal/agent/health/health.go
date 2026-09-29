// Package health expone /healthz y /readyz para las probes del chart del agente.
package health

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// DefaultAddr es donde escucha el servidor si no se configura otra cosa.
const DefaultAddr = ":8081"

// Server es el servidor HTTP de salud.
type Server struct {
	srv   *http.Server
	ready func() bool
	log   *slog.Logger
}

// New arma el servidor. ready dice si el agente está conectado a la API.
func New(addr string, ready func() bool, log *slog.Logger) *Server {
	if addr == "" {
		addr = DefaultAddr
	}
	if ready == nil {
		ready = func() bool { return false }
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Server{ready: ready, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

// Handler devuelve el handler HTTP (para tests o para montarlo en otro mux).
func (s *Server) Handler() http.Handler { return s.srv.Handler }

// Run escucha hasta que ctx termine y después apaga el servidor con gracia.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("servidor de salud escuchando", "addr", s.srv.Addr)
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	}
}

// healthz: el proceso vive.
func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// readyz: hay sesión con la API.
func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if s.ready() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("conectado a la API\n"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("sin conexión con la API\n"))
}
