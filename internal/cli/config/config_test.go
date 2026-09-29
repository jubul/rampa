package config

import (
	"os"
	"path/filepath"
	"testing"
)

func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfig, "")
	t.Setenv(EnvAPI, "")
	t.Setenv(EnvToken, "")
	t.Setenv(EnvOrg, "")
	return home
}

func TestSaveAndLoad(t *testing.T) {
	home := setHome(t)
	c := &Config{API: "https://api.rampa.dev/", Token: "rampa_abc", Org: "acme"}
	p, err := c.Save()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "rampa", "config.yaml")
	if p != want {
		t.Fatalf("ruta %s, esperaba %s", p, want)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("permisos %o, esperaba 0600", st.Mode().Perm())
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.API != "https://api.rampa.dev" || got.Token != "rampa_abc" || got.Org != "acme" {
		t.Errorf("config cargada: %+v", got)
	}
	if !got.HasSession() {
		t.Error("debería haber sesión")
	}
}

func TestLoadWithoutFile(t *testing.T) {
	setHome(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.HasSession() {
		t.Error("no debería haber sesión sin archivo ni entorno")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	setHome(t)
	if _, err := (&Config{API: "https://file", Token: "file-token", Org: "file-org"}).Save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvAPI, "https://env/")
	t.Setenv(EnvToken, "env-token")
	t.Setenv(EnvOrg, "env-org")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.API != "https://env" || c.Token != "env-token" || c.Org != "env-org" {
		t.Errorf("el entorno tiene que ganar: %+v", c)
	}
}

func TestEnvOnlySession(t *testing.T) {
	setHome(t)
	t.Setenv(EnvAPI, "https://env")
	t.Setenv(EnvToken, "tok")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.HasSession() || c.Org != "" {
		t.Errorf("sesión solo por entorno: %+v", c)
	}
}

func TestConfigPathEnv(t *testing.T) {
	setHome(t)
	alt := filepath.Join(t.TempDir(), "otro.yaml")
	t.Setenv(EnvConfig, alt)
	p, err := (&Config{API: "https://x", Token: "t"}).Save()
	if err != nil {
		t.Fatal(err)
	}
	if p != alt {
		t.Errorf("ruta %s, esperaba %s", p, alt)
	}
}

func TestInvalidFile(t *testing.T) {
	home := setHome(t)
	dir := filepath.Join(home, ".config", "rampa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("api: [esto no cierra"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("esperaba error con yaml inválido")
	}
}

func TestRedacted(t *testing.T) {
	if got := Redacted(""); got != "" {
		t.Errorf("vacío: %q", got)
	}
	if got := Redacted("corto"); got != "****" {
		t.Errorf("corto: %q", got)
	}
	if got := Redacted("rampa_1234567890abcdef"); got != "rampa_...cdef" {
		t.Errorf("largo: %q", got)
	}
}
