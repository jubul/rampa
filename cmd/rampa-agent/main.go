// rampa-agent corre en el cluster del cliente. Abre una conexión saliente al
// plano de control, recibe comandos (deploy, undeploy, status, logs,
// write_secret) y los ejecuta contra el API server con la identidad de su
// ServiceAccount.
//
// Configuración por variables de entorno (ver docs/api.md):
//
//	RAMPA_API_URL          requerido, https://api.rampa.dev
//	RAMPA_CLUSTER_TOKEN    token del cluster. Si está vacío se lee el Secret
//	                       <RAMPA_NAMESPACE>/rampa-agent-token y, si no existe,
//	                       se canjea RAMPA_BOOTSTRAP_TOKEN (ver internal/agent/bootstrap)
//	RAMPA_BOOTSTRAP_TOKEN  token de bootstrap de un solo uso (chart: cluster.bootstrapToken)
//	RAMPA_NAMESPACE        namespace del agente (default rampa-system; el chart lo inyecta)
//	RAMPA_KUBECONFIG       opcional, para correr fuera del cluster
//	RAMPA_LOG_LEVEL        debug | info | warn | error (default info)
//	RAMPA_HEALTH_ADDR      dirección de /healthz y /readyz (default :8081)
//	RAMPA_SHUTDOWN_GRACE   cuánto esperar a los comandos en curso al apagar (default 30s)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"rampa.dev/internal/agent/bootstrap"
	"rampa.dev/internal/agent/client"
	"rampa.dev/internal/agent/cloud"
	"rampa.dev/internal/agent/executor"
	"rampa.dev/internal/agent/health"
	"rampa.dev/internal/agent/kube"
)

// version la fija el build con -ldflags "-X main.version=...".
var version = "dev"

type config struct {
	APIURL         string
	Token          string
	BootstrapToken string
	Namespace      string
	Kubeconfig     string
	LogLevel       slog.Level
	HealthListen   string
	ShutdownGrace  time.Duration
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
	log.Info("rampa-agent arrancando", "version", version, "api", cfg.APIURL, "namespace", cfg.Namespace,
		"kubeconfig", cfg.Kubeconfig != "", "shutdown_grace", cfg.ShutdownGrace,
		"token_env", cfg.Token != "", "bootstrap_env", cfg.BootstrapToken != "")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	kc, err := kube.New(ctx, cfg.Kubeconfig, version, log.With("component", "kube"))
	if err != nil {
		log.Error("no pude conectar con Kubernetes", "err", err)
		return 1
	}

	// El servidor de salud arranca antes del bootstrap: /healthz tiene que
	// responder mientras esperamos a la API (hasta 10 minutos en una VM que
	// arranca antes que el plano de control), si no el startupProbe mata el pod.
	// /readyz sigue en 503 hasta que la sesión con la API esté establecida.
	var agentRef atomic.Pointer[client.Client]
	ready := func() bool {
		a := agentRef.Load()
		return a != nil && a.Connected()
	}
	hs := health.New(cfg.HealthListen, ready, log.With("component", "health"))
	healthErr := make(chan error, 1)
	go func() { healthErr <- hs.Run(ctx) }()

	tok, err := bootstrap.Resolve(ctx, bootstrap.Config{
		APIURL:         cfg.APIURL,
		ClusterToken:   cfg.Token,
		BootstrapToken: cfg.BootstrapToken,
		Namespace:      cfg.Namespace,
		AgentVersion:   version,
		Client:         kc.Typed,
		Logger:         log.With("component", "bootstrap"),
	})
	if err != nil {
		log.Error("no pude obtener el token del cluster", "err", err)
		stop()
		<-healthErr
		return 2
	}
	log.Info("token del cluster resuelto", "source", tok.Source, "persisted", tok.Persisted)

	exec := &executor.Executor{
		Kube:  kc,
		Cloud: cloud.NewAWSSecretsManager(log.With("component", "cloud")),
		Log:   log.With("component", "executor"),
	}
	agent, err := client.New(client.Config{
		APIURL:        cfg.APIURL,
		Token:         tok.Value,
		AgentVersion:  version,
		Cluster:       kc,
		Executor:      exec,
		Logger:        log.With("component", "client"),
		ShutdownGrace: cfg.ShutdownGrace,
	})
	if err != nil {
		log.Error("configuración del cliente inválida", "err", err)
		stop()
		<-healthErr
		return 2
	}
	agentRef.Store(agent)

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

// loadConfig lee la configuración desde variables de entorno. El token del
// cluster no es requerido acá: si falta, bootstrap.Resolve lo busca en el
// Secret del namespace o lo canjea con RAMPA_BOOTSTRAP_TOKEN, y si tampoco
// puede, falla con un mensaje que dice exactamente qué falta.
func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		APIURL:         strings.TrimSpace(getenv("RAMPA_API_URL")),
		Token:          strings.TrimSpace(getenv("RAMPA_CLUSTER_TOKEN")),
		BootstrapToken: strings.TrimSpace(getenv("RAMPA_BOOTSTRAP_TOKEN")),
		Namespace:      strings.TrimSpace(getenv("RAMPA_NAMESPACE")),
		Kubeconfig:     strings.TrimSpace(getenv("RAMPA_KUBECONFIG")),
		HealthListen:   strings.TrimSpace(getenv("RAMPA_HEALTH_ADDR")),
		LogLevel:       slog.LevelInfo,
		ShutdownGrace:  client.DefaultShutdownGrace,
	}
	var errs []error
	if cfg.APIURL == "" {
		errs = append(errs, errors.New("RAMPA_API_URL es requerida"))
	}
	if cfg.Namespace == "" {
		cfg.Namespace = bootstrap.DefaultNamespace
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
