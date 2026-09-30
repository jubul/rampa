package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(map[string]string{
		"RAMPA_DATABASE_URL":   "postgres://u:p@localhost/db",
		"RAMPA_SESSION_SECRET": strings.Repeat("s", 32),
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Listen != ":8080" || c.PublicURL != "http://localhost:8080" || c.DeployTimeout != 10*time.Minute {
		t.Errorf("defaults inesperados: %+v", c)
	}
	if c.DevLogin || c.OIDCEnabled() {
		t.Errorf("dev login y oidc deberían estar apagados")
	}
	if c.ChartRepo != DefaultChartRepo || c.AgentImage != DefaultAgentImage {
		t.Errorf("chart/imagen por defecto incorrectos: %+v", c)
	}
}

func TestInfraDefaultsAndStateKey(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{
			"RAMPA_DATABASE_URL":   "postgres://u:p@localhost/db",
			"RAMPA_SESSION_SECRET": strings.Repeat("s", 32),
		}
	}
	c, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.TofuBin != "tofu" || c.InfraTimeout != 40*time.Minute || c.InfraConcurrency != 2 || c.InfraWorkdir == "" {
		t.Errorf("defaults de infra: %+v", c)
	}
	if c.InfraEnabled() {
		t.Error("sin RAMPA_STATE_KEY infra debería estar deshabilitada")
	}
	warned := false
	for _, w := range c.Warnings {
		if strings.Contains(w, "RAMPA_STATE_KEY") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("esperaba un aviso por la clave de estado vacía: %v", c.Warnings)
	}

	raw := strings.Repeat("ab", 32) // 32 bytes en hex
	for name, key := range map[string]string{
		"hex":        raw,
		"base64":     "q6urq6urq6urq6urq6urq6urq6urq6urq6urq6urq6s=",
		"base64 raw": "q6urq6urq6urq6urq6urq6urq6urq6urq6urq6urq6s",
	} {
		m := base()
		m["RAMPA_STATE_KEY"] = key
		m["RAMPA_TOFU_BIN"] = "/opt/tofu"
		m["RAMPA_INFRA_TIMEOUT"] = "1h"
		m["RAMPA_INFRA_CONCURRENCY"] = "4"
		m["RAMPA_INFRA_WORKDIR"] = "/var/tmp"
		c, err := Load(env(m))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !c.InfraEnabled() || len(c.StateKey) != 32 || c.TofuBin != "/opt/tofu" || c.InfraTimeout != time.Hour || c.InfraConcurrency != 4 || c.InfraWorkdir != "/var/tmp" {
			t.Errorf("%s: %+v", name, c)
		}
	}
	for name, key := range map[string]string{"corta": "abcd", "basura": "$$$", "31 bytes": strings.Repeat("ab", 31)} {
		m := base()
		m["RAMPA_STATE_KEY"] = key
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "RAMPA_STATE_KEY") {
			t.Errorf("clave %s debería fallar, tengo %v", name, err)
		}
	}
	for name, extra := range map[string]map[string]string{
		"timeout":     {"RAMPA_INFRA_TIMEOUT": "cuarenta"},
		"concurrency": {"RAMPA_INFRA_CONCURRENCY": "0"},
	} {
		m := base()
		for k, v := range extra {
			m[k] = v
		}
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s: esperaba error", name)
		}
	}
}

func TestLoadRequiresDatabase(t *testing.T) {
	_, err := Load(env(map[string]string{"RAMPA_SESSION_SECRET": strings.Repeat("s", 32)}))
	if err == nil || !strings.Contains(err.Error(), "RAMPA_DATABASE_URL") {
		t.Fatalf("esperaba error por RAMPA_DATABASE_URL, tengo %v", err)
	}
}

func TestSessionSecret(t *testing.T) {
	base := map[string]string{"RAMPA_DATABASE_URL": "postgres://u:p@localhost/db"}

	if _, err := Load(env(base)); err == nil || !strings.Contains(err.Error(), "RAMPA_SESSION_SECRET") {
		t.Fatalf("sin secreto ni dev login debería fallar, tengo %v", err)
	}

	short := map[string]string{"RAMPA_DATABASE_URL": base["RAMPA_DATABASE_URL"], "RAMPA_SESSION_SECRET": "corto"}
	if _, err := Load(env(short)); err == nil || !strings.Contains(err.Error(), "al menos 32") {
		t.Fatalf("secreto corto debería fallar, tengo %v", err)
	}

	dev := map[string]string{"RAMPA_DATABASE_URL": base["RAMPA_DATABASE_URL"], "RAMPA_DEV_LOGIN": "true"}
	c, err := Load(env(dev))
	if err != nil {
		t.Fatalf("dev login sin secreto debería generar uno: %v", err)
	}
	if len(c.SessionSecret) < MinSessionSecretBytes || !c.DevLogin {
		t.Errorf("secreto efímero inválido: %d bytes, dev=%v", len(c.SessionSecret), c.DevLogin)
	}
	if len(c.Warnings) == 0 {
		t.Errorf("esperaba un aviso por el secreto efímero")
	}
}

func TestOIDCAllOrNothing(t *testing.T) {
	m := map[string]string{
		"RAMPA_DATABASE_URL":   "postgres://u:p@localhost/db",
		"RAMPA_SESSION_SECRET": strings.Repeat("s", 32),
		"RAMPA_OIDC_ISSUER":    "https://idp.example.com",
	}
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "OIDC") {
		t.Fatalf("OIDC parcial debería fallar, tengo %v", err)
	}
	m["RAMPA_OIDC_CLIENT_ID"] = "id"
	m["RAMPA_OIDC_CLIENT_SECRET"] = "secret"
	c, err := Load(env(m))
	if err != nil || !c.OIDCEnabled() {
		t.Fatalf("OIDC completo debería andar: %v", err)
	}
}

func TestInvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"timeout":   {"RAMPA_DEPLOY_TIMEOUT": "diez minutos"},
		"dev login": {"RAMPA_DEV_LOGIN": "quizas"},
		"public":    {"RAMPA_PUBLIC_URL": "localhost"},
		"database":  {"RAMPA_DATABASE_URL": "mysql://x"},
		"webdir":    {"RAMPA_WEB_DIR": "/no/existe/rampa"},
	}
	for name, extra := range cases {
		m := map[string]string{
			"RAMPA_DATABASE_URL":   "postgres://u:p@localhost/db",
			"RAMPA_SESSION_SECRET": strings.Repeat("s", 32),
		}
		for k, v := range extra {
			m[k] = v
		}
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s: esperaba error", name)
		}
	}
}

func TestTrustedProxies(t *testing.T) {
	base := map[string]string{
		"RAMPA_DATABASE_URL":   "postgres://u:p@localhost/db",
		"RAMPA_SESSION_SECRET": strings.Repeat("s", 32),
	}
	c, err := Load(env(base))
	if err != nil || len(c.TrustedProxies) != 0 {
		t.Fatalf("por defecto no hay proxies de confianza: %v %v", err, c.TrustedProxies)
	}
	base["RAMPA_TRUSTED_PROXIES"] = "10.0.0.0/8, 192.168.1.7 ,,fd00::/8"
	c, err = Load(env(base))
	if err != nil || len(c.TrustedProxies) != 3 {
		t.Fatalf("proxies: %v %v", err, c.TrustedProxies)
	}
	if c.TrustedProxies[1].String() != "192.168.1.7/32" {
		t.Errorf("una IP suelta vale como /32: %v", c.TrustedProxies[1])
	}
	base["RAMPA_TRUSTED_PROXIES"] = "10.0.0.0/8,nada"
	if _, err := Load(env(base)); err == nil || !strings.Contains(err.Error(), "RAMPA_TRUSTED_PROXIES") {
		t.Errorf("una entrada inválida debería fallar: %v", err)
	}
}
