// Package config maneja la sesión local del CLI: a qué API hablar, con qué
// token y sobre qué organización. Vive en ~/.config/rampa/config.yaml con
// permisos 0600. Las variables RAMPA_API, RAMPA_TOKEN y RAMPA_ORG pisan lo que
// diga el archivo, así el mismo binario sirve en CI sin tocar el disco.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Variables de entorno que pisan al archivo.
const (
	EnvAPI    = "RAMPA_API"
	EnvToken  = "RAMPA_TOKEN"
	EnvOrg    = "RAMPA_ORG"
	EnvConfig = "RAMPA_CONFIG" // ruta alternativa al archivo, útil para tests y CI
)

// Config es la sesión del CLI.
type Config struct {
	API   string `yaml:"api"`
	Token string `yaml:"token,omitempty"`
	Org   string `yaml:"org,omitempty"`
}

// ErrNoSession se devuelve cuando no hay API o token configurados.
var ErrNoSession = errors.New("no hay sesión con la API de Rampa. Corré `rampa login --api URL --token TOKEN` o exportá RAMPA_API y RAMPA_TOKEN")

// Path devuelve la ruta del archivo de configuración: $RAMPA_CONFIG si está
// seteada, si no ~/.config/rampa/config.yaml.
func Path() (string, error) {
	if p := os.Getenv(EnvConfig); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no pude resolver el directorio home: %w", err)
	}
	return filepath.Join(home, ".config", "rampa", "config.yaml"), nil
}

// Load lee el archivo (si existe) y aplica las variables de entorno encima.
// Si el archivo no existe devuelve una config vacía sin error: la ausencia de
// sesión la decide el que llama con HasSession.
func Load() (*Config, error) {
	c := &Config{}
	p, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("%s: no es un config.yaml válido: %w", p, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// sin archivo, seguimos solo con el entorno
	default:
		return nil, fmt.Errorf("leyendo %s: %w", p, err)
	}
	c.applyEnv()
	c.API = strings.TrimRight(c.API, "/")
	return c, nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv(EnvAPI); v != "" {
		c.API = v
	}
	if v := os.Getenv(EnvToken); v != "" {
		c.Token = v
	}
	if v := os.Getenv(EnvOrg); v != "" {
		c.Org = v
	}
}

// HasSession dice si hay lo mínimo para hablar con la API.
func (c *Config) HasSession() bool {
	return c != nil && c.API != "" && c.Token != ""
}

// Save escribe el archivo con permisos 0600 (y el directorio con 0700). Se
// escribe a un temporal y se renombra para no dejar un archivo a medias.
func (c *Config) Save() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", fmt.Errorf("creando %s: %w", filepath.Dir(p), err)
	}
	out := Config{API: strings.TrimRight(c.API, "/"), Token: c.Token, Org: c.Org}
	b, err := yaml.Marshal(out)
	if err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", fmt.Errorf("escribiendo %s: %w", tmp, err)
	}
	// Por si el archivo ya existía con otros permisos.
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("guardando %s: %w", p, err)
	}
	return p, nil
}

// Redacted devuelve el token enmascarado para mostrar en pantalla o logs.
func Redacted(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 10 {
		return "****"
	}
	return token[:6] + "..." + token[len(token)-4:]
}
