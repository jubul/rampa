// rampa-agent corre en el cluster del cliente. Abre una conexión saliente al
// plano de control, recibe comandos (deploy, undeploy, status, logs) y los
// ejecuta contra el API server con la identidad de su ServiceAccount.
//
// Configuración por variables de entorno (ver docs/api.md):
//
//	RAMPA_API_URL        requerido, https://api.rampa.dev
//	RAMPA_CLUSTER_TOKEN  requerido, token del cluster
//	RAMPA_KUBECONFIG     opcional, para correr fuera del cluster
//	RAMPA_LOG_LEVEL      debug | info | warn | error (default info)
//	RAMPA_HEALTH_ADDR    dirección de /healthz y /readyz (default :8081)
//	RAMPA_SHUTDOWN_GRACE cuánto esperar a los comandos en curso al apagar (default 30s)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"rampa.dev/internal/agent/client"
	"rampa.dev/internal/agent/executor"
	"rampa.dev/internal/agent/health"
	"rampa.dev/internal/agent/kube"
)

// version la fija el build con -ldflags "-X main.version=...".
var version = "dev"

type config struct {
	APIURL        string
	Token         string
	Kubeconfig    string
	LogLevel      slog.Level
	HealthListen  string
	ShutdownGrace time.Duration
}

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error de configuración:", err)
		return 2
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	log.Info("rampa-agent arrancando", "version", version, "api", cfg.APIURL, "kubeconfig", cfg.Kubeconfig != "", "shutdown_grace", cfg.ShutdownGrace)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	kc, err := kube.New(ctx, cfg.Kubeconfig, version, log.With("component", "kube"))
	if err != nil {
		log.Error("no pude conectar con Kubernetes", "err", err)
		return 1
	}

	exec := &executor.Executor{Kube: kc, Log: log.With("component", "executor")}
	agent, err := client.New(client.Config{
		APIURL:        cfg.APIURL,
		Token:         cfg.Token,
		AgentVersion:  version,
		Cluster:       kc,
		Executor:      exec,
		Logger:        log.With("component", "client"),
		ShutdownGrace: cfg.ShutdownGrace,
	})
	if err != nil {
		log.Error("configuración del cliente inválida", "err", err)
		return 2
	}

	hs := health.New(cfg.HealthListen, agent.Connected, log.With("component", "health"))
	healthErr := make(chan error, 1)
	go func() { healthErr <- hs.Run(ctx) }()

	// Al cancelar ctx (SIGTERM), agent.Run deja de aceptar comandos, espera a
	// los en curso hasta ShutdownGrace entregando sus results, da por fallidos
	// los que sigan y recién entonces cierra la conexión y devuelve.
	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	code := 0
	select {
	case err := <-healthErr:
		if err != nil {
			log.Error("el servidor de salud falló", "err", err)
			code = 1
		}
		stop()
		<-runErr
	case err := <-runErr:
		if err != nil {
			log.Error("el cliente terminó con error", "err", err)
			code = 1
		}
		stop()
		<-healthErr
	}

	if left := agent.InFlight(); len(left) > 0 {
		log.Warn("quedaron comandos sin terminar al apagar; recibieron result failed", "command_ids", left)
	}
	log.Info("rampa-agent apagado")
	return code
}

// loadConfig lee la configuración desde variables de entorno.
func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		APIURL:        strings.TrimSpace(getenv("RAMPA_API_URL")),
		Token:         strings.TrimSpace(getenv("RAMPA_CLUSTER_TOKEN")),
		Kubeconfig:    strings.TrimSpace(getenv("RAMPA_KUBECONFIG")),
		HealthListen:  strings.TrimSpace(getenv("RAMPA_HEALTH_ADDR")),
		LogLevel:      slog.LevelInfo,
		ShutdownGrace: client.DefaultShutdownGrace,
	}
	var errs []error
	if cfg.APIURL == "" {
		errs = append(errs, errors.New("RAMPA_API_URL es requerida"))
	}
	if cfg.Token == "" {
		errs = append(errs, errors.New("RAMPA_CLUSTER_TOKEN es requerido"))
	}
	if cfg.HealthListen == "" {
		cfg.HealthListen = health.DefaultAddr
	}
	if raw := strings.TrimSpace(getenv("RAMPA_SHUTDOWN_GRACE")); raw != "" {
		d, err := time.ParseDuration(raw)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("RAMPA_SHUTDOWN_GRACE %q no es una duración válida (ejemplo: 30s, 2m)", raw))
		case d <= 0:
			errs = append(errs, fmt.Errorf("RAMPA_SHUTDOWN_GRACE debe ser mayor que cero, fue %q", raw))
		default:
			cfg.ShutdownGrace = d
		}
	}
	if lvl := strings.ToLower(strings.TrimSpace(getenv("RAMPA_LOG_LEVEL"))); lvl != "" {
		switch lvl {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn", "warning":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			errs = append(errs, fmt.Errorf("RAMPA_LOG_LEVEL %q no es válido (debug, info, warn, error)", lvl))
		}
	}
	return cfg, errors.Join(errs...)
}
