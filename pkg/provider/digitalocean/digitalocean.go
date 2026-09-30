// Package digitalocean implementa provider.Provider para DigitalOcean.
//
// Conexión de cuenta: DigitalOcean no ofrece federación de identidad, así que
// el cliente crea un token de API con el alcance mínimo y lo pega en la web. La
// API lo guarda cifrado (cloud_accounts.credentials_ciphertext) y lo carga en
// CloudAccount.Credentials solo para llamar a la nube (ADR 0004, decisión 4).
//
// Cluster: DOKS o un Droplet con k3s. Registry: DOCR. Secretos: DigitalOcean no
// tiene gestor de secretos, así que el cluster usa secret_store "kubernetes" y
// el agente mantiene el Secret <app>-params con lo que le manda la API.
package digitalocean

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"rampa.dev/pkg/provider"
)

const (
	// DefaultBaseURL es la API pública de DigitalOcean. Los tests la reemplazan.
	DefaultBaseURL = "https://api.digitalocean.com"
	// SecretStoreName es el valor especial de clusters.secret_store: sin gestor
	// de secretos en la nube, el agente escribe Secrets de Kubernetes.
	SecretStoreName = "kubernetes"
	// RegistryHost es el host de DigitalOcean Container Registry.
	RegistryHost = "registry.digitalocean.com"
	// TokensURL es donde el cliente crea el token de API.
	TokensURL = "https://cloud.digitalocean.com/account/api/tokens/new"
	// DefaultModuleSource es la base de los módulos OpenTofu de services.
	DefaultModuleSource = "git::https://github.com/CHANGEME/rampa.git"

	// CredentialAPIToken es la clave de CloudAccount.Credentials con el token.
	CredentialAPIToken = "api_token"
	// SettingRegistryName es la clave de CloudAccount.Settings con el nombre del DOCR.
	SettingRegistryName = "registry_name"
	// EnvToken es la variable que leen el provider de OpenTofu y doctl.
	EnvToken = "DIGITALOCEAN_TOKEN"

	verifyTimeout = 15 * time.Second
)

// registryNamePattern: minúsculas, números y guiones, hasta 63 caracteres.
var registryNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Config configura el provider.
type Config struct {
	// BaseURL es la base de la API de DigitalOcean. Vacío usa DefaultBaseURL.
	BaseURL string
	// HTTPClient es el cliente HTTP. nil usa uno con timeout.
	HTTPClient *http.Client
	// ModuleSource es la base de los módulos OpenTofu (git::https://...).
	ModuleSource string
}

// Provider es la implementación de DigitalOcean.
type Provider struct {
	cfg Config
}

var (
	_ provider.Provider            = (*Provider)(nil)
	_ provider.CredentialsProvider = (*Provider)(nil)
)

// New crea el provider con una configuración explícita.
func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: verifyTimeout}
	}
	if cfg.ModuleSource == "" {
		cfg.ModuleSource = DefaultModuleSource
	}
	return &Provider{cfg: cfg}
}

func init() {
	provider.Register(New(Config{}))
}

// Kind implementa provider.Provider.
func (p *Provider) Kind() provider.Kind { return provider.DigitalOcean }

// DisplayName implementa provider.Provider.
func (p *Provider) DisplayName() string { return "DigitalOcean" }

// Available implementa provider.Provider.
func (p *Provider) Available() bool { return true }

// Auth implementa provider.Provider: un token de API del cliente, cifrado en la API.
func (p *Provider) Auth() provider.AuthKind { return provider.AuthToken }

// Terminology implementa provider.Provider con los nombres de DigitalOcean.
func (p *Provider) Terminology() provider.Terminology {
	return provider.Terminology{
		Account:     provider.LocalizedTerm{ES: "cuenta de DigitalOcean", EN: "DigitalOcean account"},
		Project:     provider.LocalizedTerm{ES: "proyecto", EN: "project"},
		Cluster:     provider.LocalizedTerm{ES: "cluster DOKS", EN: "DOKS cluster"},
		VM:          provider.LocalizedTerm{ES: "Droplet", EN: "Droplet"},
		Registry:    provider.LocalizedTerm{ES: "DOCR", EN: "DOCR"},
		SecretStore: provider.LocalizedTerm{ES: "Secrets de Kubernetes", EN: "Kubernetes Secrets"},
		Region:      provider.LocalizedTerm{ES: "región", EN: "region"},
	}
}

// ConnectInstructions explica cómo crear un token de API con alcance mínimo y
// pide el token (campo secreto) y, opcional, el nombre del DOCR.
func (p *Provider) ConnectInstructions(req provider.ConnectRequest) provider.Instructions {
	tokenName := "rampa"
	if req.OrgSlug != "" {
		tokenName = "rampa-" + req.OrgSlug
	}
	description := fmt.Sprintf(
		"DigitalOcean no tiene federación de identidad, así que Rampa opera con un token de API que vos creás y podés revocar cuando quieras. "+
			"Rampa lo guarda cifrado (AES-256-GCM) y nunca lo vuelve a mostrar. Pasos: "+
			"1) En el panel de DigitalOcean entrá a API, Tokens, Generate New Token (el botón de abajo abre esa pantalla). "+
			"2) Nombre %s, vencimiento de 90 días (cuando venza, generá otro y pegalo acá de nuevo: eso rota el anterior). "+
			"3) Elegí Custom Scopes con el mínimo que Rampa necesita: account (read), droplet (create, read, update, delete), "+
			"reserved_ip (create, read, delete), firewall (create, read, update, delete), ssh_key (read), vpc (read), "+
			"kubernetes (create, read, update, delete), registry (create, read, update, delete), domain (read, update) si vas a administrar DNS acá, "+
			"y project (read). No hace falta acceso de escritura a billing ni a la cuenta. "+
			"4) Copiá el token (empieza con dop_v1_) y pegalo abajo. "+
			"Si además querés que Rampa publique imágenes en tu DigitalOcean Container Registry, poné su nombre: las imágenes quedan en %s/<nombre>/rampa/<app>. "+
			"El comando de abajo comprueba el token desde tu terminal antes de pegarlo.",
		tokenName, RegistryHost)
	command := "curl -fsS -H \"Authorization: Bearer $DIGITALOCEAN_TOKEN\" " + p.cfg.BaseURL + "/v2/account"
	return provider.Instructions{
		Title:       "Conectar una cuenta de DigitalOcean",
		Description: description,
		URL:         TokensURL,
		Command:     command,
		Fields: []provider.Field{
			{
				Name:        CredentialAPIToken,
				Label:       "Token de API",
				Placeholder: "dop_v1_...",
				Help:        "Se guarda cifrado y no se vuelve a mostrar. Para rotarlo, pegá uno nuevo.",
				Required:    true,
				Secret:      true,
			},
			{
				Name:        SettingRegistryName,
				Label:       "Nombre del Container Registry (DOCR)",
				Placeholder: "acme",
				Help:        "Opcional. El registry de DigitalOcean donde Rampa publica las imágenes de tus apps.",
				Required:    false,
			},
		},
	}
}

// accountResponse es lo que devuelve GET /v2/account.
type accountResponse struct {
	Account struct {
		Email         string `json:"email"`
		UUID          string `json:"uuid"`
		Status        string `json:"status"`
		EmailVerified bool   `json:"email_verified"`
	} `json:"account"`
}

// Verify llama GET /v2/account con el token. Devuelve error si no se pudo
// intentar (sin token, red); si DigitalOcean rechaza el token, devuelve
// Verification con OK false y el motivo.
func (p *Provider) Verify(ctx context.Context, acc provider.CloudAccount) (provider.Verification, error) {
	token, err := apiToken(acc)
	if err != nil {
		return provider.Verification{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.BaseURL+"/v2/account", nil)
	if err != nil {
		return provider.Verification{}, fmt.Errorf("provider digitalocean: armando la request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return provider.Verification{}, fmt.Errorf("provider digitalocean: tiempo de espera agotado hablando con la API de DigitalOcean")
		}
		return provider.Verification{}, fmt.Errorf("provider digitalocean: no pude hablar con la API de DigitalOcean: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		slog.Info("provider digitalocean: token rechazado", "account", acc.Name, "status", resp.StatusCode)
		return provider.Verification{
			OK:      false,
			Message: fmt.Sprintf("DigitalOcean rechazó el token (HTTP %d). Revisá que no esté vencido ni revocado y que tenga el alcance account:read; si lo regeneraste, pegalo de nuevo.", resp.StatusCode),
		}, nil
	case resp.StatusCode != http.StatusOK:
		slog.Info("provider digitalocean: respuesta inesperada", "account", acc.Name, "status", resp.StatusCode)
		return provider.Verification{OK: false, Message: fmt.Sprintf("la API de DigitalOcean respondió HTTP %d", resp.StatusCode)}, nil
	}
	var out accountResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return provider.Verification{OK: false, Message: "la API de DigitalOcean devolvió una respuesta que no entiendo"}, nil
	}
	identity := strings.TrimSpace(out.Account.Email)
	if identity == "" {
		identity = out.Account.UUID
	}
	if identity == "" {
		return provider.Verification{OK: false, Message: "DigitalOcean devolvió una cuenta sin email ni uuid"}, nil
	}
	msg := "token válido"
	if out.Account.Status != "" && out.Account.Status != "active" {
		msg = fmt.Sprintf("token válido, pero la cuenta está en estado %s", out.Account.Status)
	}
	slog.Info("provider digitalocean: cuenta verificada", "account", acc.Name, "identity", identity)
	return provider.Verification{OK: true, Identity: identity, Message: msg}, nil
}

// Registry devuelve registry.digitalocean.com/<settings.registry_name>.
func (p *Provider) Registry(acc provider.CloudAccount) (string, error) {
	name := strings.TrimSpace(acc.Settings[SettingRegistryName])
	if name == "" {
		return "", fmt.Errorf("provider digitalocean: la cuenta %q no tiene settings.%s; poné el nombre de tu Container Registry (DOCR) en la cuenta", acc.Name, SettingRegistryName)
	}
	if !registryNamePattern.MatchString(name) {
		return "", fmt.Errorf("provider digitalocean: settings.%s %q inválido: minúsculas, números y guiones, hasta 63 caracteres", SettingRegistryName, name)
	}
	return RegistryHost + "/" + name, nil
}

// SecretStoreSpec devuelve el store especial "kubernetes" sin objeto:
// DigitalOcean no tiene gestor de secretos, así que no hay ClusterSecretStore
// que instalar; el agente escribe Secrets de Kubernetes con write_secret.
func (p *Provider) SecretStoreSpec(provider.CloudAccount, provider.ClusterInfo) provider.SecretStoreSpec {
	return provider.SecretStoreSpec{Name: SecretStoreName, Object: map[string]any{}}
}

// WorkloadIdentityAnnotations está vacío: DigitalOcean no tiene identidad de
// workload para pods.
func (p *Provider) WorkloadIdentityAnnotations(provider.CloudAccount, string) map[string]string {
	return map[string]string{}
}

// ServiceModule apunta a terraform/modules/digitalocean/<kind>. Ninguno existe todavía.
func (p *Provider) ServiceModule(kind string) (provider.ModuleRef, bool) {
	return provider.ModuleRef{Source: fmt.Sprintf("%s//terraform/modules/digitalocean/%s?ref=main", p.cfg.ModuleSource, kind)}, false
}

// Credentials implementa provider.CredentialsProvider: el runner de
// infraestructura exporta DIGITALOCEAN_TOKEN al subproceso de OpenTofu. El
// token es de larga duración, así que Expires queda en cero.
func (p *Provider) Credentials(_ context.Context, acc provider.CloudAccount) (provider.Credentials, error) {
	token, err := apiToken(acc)
	if err != nil {
		return provider.Credentials{}, err
	}
	return provider.Credentials{Env: map[string]string{EnvToken: token}}, nil
}

// apiToken lee el token de CloudAccount.Credentials.
func apiToken(acc provider.CloudAccount) (string, error) {
	token := strings.TrimSpace(acc.Credentials[CredentialAPIToken])
	if token == "" {
		return "", fmt.Errorf("provider digitalocean: la cuenta %q no tiene %s; pegá el token de API en la cuenta (PATCH con credentials.%s)", acc.Name, CredentialAPIToken, CredentialAPIToken)
	}
	return token, nil
}
