package main

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"rampa.dev/internal/agent/client"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfig(t *testing.T) {
	cfg, err := loadConfig(env(map[string]string{
		"RAMPA_API_URL": " https://api.rampa.dev ", "RAMPA_CLUSTER_TOKEN": "rampa_cluster_x", "RAMPA_LOG_LEVEL": "DEBUG",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIURL != "https://api.rampa.dev" || cfg.Token != "rampa_cluster_x" || cfg.LogLevel != slog.LevelDebug || cfg.HealthListen != ":8081" || cfg.Kubeconfig != "" {
		t.Errorf("config: %+v", cfg)
	}
	if cfg.ShutdownGrace != client.DefaultShutdownGrace {
		t.Errorf("shutdown grace por defecto: %s", cfg.ShutdownGrace)
	}

	cfg, err = loadConfig(env(map[string]string{
		"RAMPA_API_URL": "http://localhost:8080", "RAMPA_CLUSTER_TOKEN": "t", "RAMPA_SHUTDOWN_GRACE": " 2m ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ShutdownGrace != 2*time.Minute {
		t.Errorf("shutdown grace: %s", cfg.ShutdownGrace)
	}
	for _, bad := range []string{"treinta", "0", "-5s"} {
		if _, err := loadConfig(env(map[string]string{"RAMPA_API_URL": "http://x", "RAMPA_CLUSTER_TOKEN": "t", "RAMPA_SHUTDOWN_GRACE": bad})); err == nil || !strings.Contains(err.Error(), "RAMPA_SHUTDOWN_GRACE") {
			t.Errorf("RAMPA_SHUTDOWN_GRACE=%q: esperaba error, fue %v", bad, err)
		}
	}

	cfg, err = loadConfig(env(map[string]string{
		"RAMPA_API_URL": "http://localhost:8080", "RAMPA_CLUSTER_TOKEN": "t", "RAMPA_KUBECONFIG": "/tmp/kc", "RAMPA_HEALTH_ADDR": ":9999", "RAMPA_LOG_LEVEL": "warn",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Kubeconfig != "/tmp/kc" || cfg.HealthListen != ":9999" || cfg.LogLevel != slog.LevelWarn {
		t.Errorf("config: %+v", cfg)
	}

	_, err = loadConfig(env(map[string]string{"RAMPA_LOG_LEVEL": "loud"}))
	if err == nil {
		t.Fatal("esperaba errores")
	}
	for _, want := range []string{"RAMPA_API_URL", "RAMPA_CLUSTER_TOKEN", "RAMPA_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("el error debería mencionar %s: %v", want, err)
		}
	}
}
