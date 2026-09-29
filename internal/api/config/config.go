// Package config carga la configuración del plano de control desde variables
// de entorno RAMPA_*, según la tabla de docs/api.md.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Defaults documentados en docs/api.md.
const (
	DefaultListen        = ":8080"
	DefaultPublicURL     = "http://localhost:8080"
	DefaultAgentImage    = "ghcr.io/CHANGEME/rampa-agent:latest"
	DefaultChartRepo     = "oci://ghcr.io/CHANGEME/charts/rampa-agent"
	DefaultDeployTimeout = 10 * time.Minute
	// MinSessionSecretBytes es el largo mínimo del secreto de sesión.
	MinSessionSecretBytes = 32
)

// Config es la configuración del servidor.
type Config struct {
	Listen        string
	DatabaseURL   string
	PublicURL     string
	SessionSecret []byte
	DevLogin      bool

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string

	WebDir        string
	AgentImage    string
	ChartRepo     string
	DeployTimeout time.Duration

	// Warnings son avisos no fatales que se loguean al arrancar.
	Warnings []string
}

// OIDCEnabled dice si las tres variables de OIDC están configuradas.
func (c Config) OIDCEnabled() bool {
	return c.OIDCIssuer != "" && c.OIDCClientID != "" && c.OIDCClientSecret != ""
}

// FromEnv carga la configuración desde el entorno del proceso.
func FromEnv() (Config, error) {
	return Load(os.Getenv)
}

// Load carga la configuración usando getenv, que permite testear sin tocar el
// entorno real. Aplica defaults y valida.
func Load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		v := strings.TrimSpace(getenv(key))
		if v == "" {
			return def
		}
		return v
	}

	c := Config{
		Listen:           get("RAMPA_LISTEN", DefaultListen),
		DatabaseURL:      get("RAMPA_DATABASE_URL", ""),
		PublicURL:        strings.TrimRight(get("RAMPA_PUBLIC_URL", DefaultPublicURL), "/"),
		OIDCIssuer:       get("RAMPA_OIDC_ISSUER", ""),
		OIDCClientID:     get("RAMPA_OIDC_CLIENT_ID", ""),
		OIDCClientSecret: get("RAMPA_OIDC_CLIENT_SECRET", ""),
		WebDir:           get("RAMPA_WEB_DIR", ""),
		AgentImage:       get("RAMPA_AGENT_IMAGE", DefaultAgentImage),
		ChartRepo:        get("RAMPA_CHART_REPO", DefaultChartRepo),
	}

	var errs []error

	switch strings.ToLower(get("RAMPA_DEV_LOGIN", "false")) {
	case "true", "1", "yes":
		c.DevLogin = true
	case "false", "0", "no":
		c.DevLogin = false
	default:
		errs = append(errs, fmt.Errorf("RAMPA_DEV_LOGIN debe ser true o false, es %q", getenv("RAMPA_DEV_LOGIN")))
	}

	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("RAMPA_DATABASE_URL es requerida (postgres://user:pass@host/db)"))
	} else if !strings.HasPrefix(c.DatabaseURL, "postgres://") && !strings.HasPrefix(c.DatabaseURL, "postgresql://") {
		errs = append(errs, errors.New("RAMPA_DATABASE_URL debe empezar con postgres://"))
	}

	if u, err := url.Parse(c.PublicURL); err != nil || u.Scheme == "" || u.Host == "" {
		errs = append(errs, fmt.Errorf("RAMPA_PUBLIC_URL %q no es una URL absoluta", c.PublicURL))
	}

	secret := get("RAMPA_SESSION_SECRET", "")
	switch {
	case secret == "" && c.DevLogin:
		// En desarrollo se genera uno efímero: las sesiones no sobreviven un reinicio.
		b := make([]byte, MinSessionSecretBytes)
		if _, err := rand.Read(b); err != nil {
			errs = append(errs, fmt.Errorf("no pude generar un secreto de sesión: %w", err))
		}
		c.SessionSecret = []byte(hex.EncodeToString(b))
		c.Warnings = append(c.Warnings, "RAMPA_SESSION_SECRET vacío: uso un secreto efímero porque RAMPA_DEV_LOGIN=true; las sesiones no sobreviven reinicios")
	case secret == "":
		errs = append(errs, errors.New("RAMPA_SESSION_SECRET es requerida (32 o más bytes) salvo con RAMPA_DEV_LOGIN=true"))
	case len(secret) < MinSessionSecretBytes:
		errs = append(errs, fmt.Errorf("RAMPA_SESSION_SECRET tiene %d bytes; se necesitan al menos %d", len(secret), MinSessionSecretBytes))
	default:
		c.SessionSecret = []byte(secret)
	}

	oidcSet := 0
	for _, v := range []string{c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret} {
		if v != "" {
			oidcSet++
		}
	}
	if oidcSet != 0 && oidcSet != 3 {
		errs = append(errs, errors.New("OIDC: hay que setear RAMPA_OIDC_ISSUER, RAMPA_OIDC_CLIENT_ID y RAMPA_OIDC_CLIENT_SECRET juntas, o ninguna"))
	}

	timeoutRaw := get("RAMPA_DEPLOY_TIMEOUT", DefaultDeployTimeout.String())
	timeout, err := time.ParseDuration(timeoutRaw)
	if err != nil || timeout <= 0 {
		errs = append(errs, fmt.Errorf("RAMPA_DEPLOY_TIMEOUT %q inválido: se espera una duración como 10m", timeoutRaw))
	} else {
		c.DeployTimeout = timeout
	}

	if c.WebDir != "" {
		if st, err := os.Stat(c.WebDir); err != nil || !st.IsDir() {
			errs = append(errs, fmt.Errorf("RAMPA_WEB_DIR %q no es un directorio", c.WebDir))
		}
	}

	if !c.DevLogin && !c.OIDCEnabled() {
		c.Warnings = append(c.Warnings, "ni RAMPA_DEV_LOGIN ni OIDC están configurados: solo se puede entrar con tokens de API")
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return c, nil
}
