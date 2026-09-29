// rampa-api es el plano de control de Rampa. Subcomandos: serve (default) y migrate.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/spf13/cobra"

	"rampa.dev/internal/api/config"
	"rampa.dev/internal/api/server"
	"rampa.dev/internal/api/store"
)

// version se setea con -ldflags "-X main.version=...".
var version = "dev"

const (
	devPGPort   = 54329
	devPGDBName = "rampa"
	// devSessionSecret es fijo a propósito: en --dev las sesiones sobreviven reinicios.
	devSessionSecret = "rampa-dev-session-secret-no-usar-en-produccion-0123456789"
	// devStateKey es la clave AES-256 (32 bytes en hex) del estado de OpenTofu en
	// --dev, fija para que el estado sobreviva reinicios. Solo para desarrollo.
	devStateKey = "72616d70612d6465762d73746174652d6b65792d6e6f2d7573652d70726f6421"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type flags struct {
	dev      bool
	logLevel string
}

func newRootCmd() *cobra.Command {
	f := &flags{}
	root := &cobra.Command{
		Use:           "rampa-api",
		Short:         "Plano de control de Rampa",
		Long:          "rampa-api es el plano de control de Rampa: API REST, hub de agentes y persistencia en Postgres. Sin subcomando corre serve.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), f)
		},
	}
	root.PersistentFlags().BoolVar(&f.dev, "dev", false, "modo desarrollo: Postgres embebido en ~/.local/share/rampa/pg (puerto 54329; binarios cacheados en ~/.embedded-postgres-go), RAMPA_DEV_LOGIN=true, un secreto de sesión fijo y una clave de estado de OpenTofu fija")
	root.PersistentFlags().StringVar(&f.logLevel, "log-level", "info", "nivel de log: debug, info, warn, error")

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Corre migraciones y sirve la API (default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), f)
		},
	}
	migrate := &cobra.Command{
		Use:   "migrate",
		Short: "Aplica las migraciones pendientes y termina",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrate(cmd.Context(), f)
		},
	}
	root.AddCommand(serve, migrate)
	return root
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
	slog.SetDefault(log)
	return log
}

// signalContext cancela con SIGINT o SIGTERM.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// setupDev arranca el Postgres embebido y setea el entorno de desarrollo.
// Devuelve la función para apagarlo.
func setupDev(log *slog.Logger) (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("no pude resolver el home: %w", err)
	}
	base := filepath.Join(home, ".local", "share", "rampa", "pg")
	for _, sub := range []string{"data", "runtime"} {
		if err := os.MkdirAll(filepath.Join(base, sub), 0o755); err != nil {
			return nil, fmt.Errorf("creando %s: %w", filepath.Join(base, sub), err)
		}
	}
	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(devPGPort).
		Database(devPGDBName).
		Username("rampa").
		Password("rampa").
		DataPath(filepath.Join(base, "data")).
		RuntimePath(filepath.Join(base, "runtime")).
		StartTimeout(60 * time.Second).
		Logger(devPGLogWriter{log: log})
	pg := embeddedpostgres.NewDatabase(cfg)
	log.Info("arrancando Postgres embebido", "dir", base, "port", devPGPort)
	if err := pg.Start(); err != nil {
		return nil, fmt.Errorf("arrancando el Postgres embebido (la primera vez descarga los binarios): %w", err)
	}
	setDefault := func(key, value string) {
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	setDefault("RAMPA_DATABASE_URL", fmt.Sprintf("postgres://rampa:rampa@127.0.0.1:%d/%s?sslmode=disable", devPGPort, devPGDBName))
	_ = os.Setenv("RAMPA_DEV_LOGIN", "true")
	setDefault("RAMPA_SESSION_SECRET", devSessionSecret)
	setDefault("RAMPA_STATE_KEY", devStateKey)
	return func() {
		if err := pg.Stop(); err != nil {
			log.Warn("apagando el Postgres embebido", "err", err)
		}
	}, nil
}

// devPGLogWriter manda la salida de Postgres al logger en nivel debug.
type devPGLogWriter struct{ log *slog.Logger }

func (w devPGLogWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			w.log.Debug("postgres", "line", line)
		}
	}
	return len(p), nil
}

func runServe(parent context.Context, f *flags) error {
	log := newLogger(f.logLevel)
	ctx, stop := signalContext(parent)
	defer stop()

	if f.dev {
		shutdown, err := setupDev(log)
		if err != nil {
			return err
		}
		defer shutdown()
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("configuración inválida:\n%w", err)
	}
	srv, err := server.New(ctx, cfg, log, version)
	if err != nil {
		return err
	}
	defer srv.Close()
	if f.dev {
		log.Info("modo dev listo", "api", cfg.PublicURL, "login", "POST /v1/auth/dev-login {\"email\": \"vos@ejemplo.com\"}")
	}
	return srv.Run(ctx)
}

func runMigrate(parent context.Context, f *flags) error {
	log := newLogger(f.logLevel)
	ctx, stop := signalContext(parent)
	defer stop()

	if f.dev {
		shutdown, err := setupDev(log)
		if err != nil {
			return err
		}
		defer shutdown()
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("configuración inválida:\n%w", err)
	}
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool(), log); err != nil {
		if errors.Is(err, context.Canceled) {
			return errors.New("migración interrumpida")
		}
		return err
	}
	log.Info("migraciones al día", "versiones", store.MigrationVersions())
	return nil
}
