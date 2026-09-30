// Package bootstrap resuelve el token del cluster al arrancar el agente.
//
// Orden de fuentes:
//  1. RAMPA_CLUSTER_TOKEN, si está seteada.
//  2. El Secret <namespace>/rampa-agent-token (clave token) del namespace del agente.
//  3. RAMPA_BOOTSTRAP_TOKEN: se canjea contra POST <api>/v1/agent/bootstrap por el
//     token real, que se guarda en ese Secret para los próximos arranques.
//
// El bootstrap token es de un solo uso y vence a las dos horas: existe para que
// un cloud-init o un script de instalación no lleven escrito el token real del
// cluster. Si la API todavía no responde (la VM arranca antes que el plano de
// control termine de desplegar), el canje se reintenta con backoff hasta
// MaxWait. Si la API responde 401, el bootstrap ya se usó o venció: se sigue
// intentando leer el Secret, porque otro proceso (un pod anterior) pudo haberlo
// canjeado y guardado. Ningún token se loguea nunca.
package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// Path es la ruta del canje en la API.
	Path = "/v1/agent/bootstrap"
	// DefaultNamespace es el namespace del agente si RAMPA_NAMESPACE está vacía.
	DefaultNamespace = "rampa-system"
	// SecretName es el Secret donde el agente guarda el token del cluster. El
	// Role del chart rampa-agent lo acota por resourceNames a este nombre.
	SecretName = "rampa-agent-token"
	// SecretKey es la clave del Secret con el token.
	SecretKey = "token"
	// DefaultMaxWait es cuánto se insiste con el canje y la lectura del Secret
	// antes de darse por vencido.
	DefaultMaxWait = 10 * time.Minute

	defaultMinBackoff = time.Second
	defaultMaxBackoff = 30 * time.Second
	requestTimeout    = 30 * time.Second
	maxErrorBody      = 4 << 10
	fieldManager      = "rampa-agent"
)

// ErrBootstrapUsed indica que la API rechazó el bootstrap token (401): ya se
// canjeó o venció.
var ErrBootstrapUsed = errors.New("la API rechazó el bootstrap token: ya se usó o venció")

// Source dice de dónde salió el token.
type Source string

const (
	SourceEnv       Source = "env"       // RAMPA_CLUSTER_TOKEN
	SourceSecret    Source = "secret"    // Secret del namespace del agente
	SourceBootstrap Source = "bootstrap" // canje de RAMPA_BOOTSTRAP_TOKEN
)

// Token es el token del cluster resuelto y de dónde salió.
type Token struct {
	Value  string
	Source Source
	// ClusterID viene solo cuando la fuente es el canje del bootstrap token.
	ClusterID string
	// Persisted dice si el token quedó guardado en el Secret. Es false cuando
	// vino por env (no hace falta) o cuando el canje anduvo pero el Secret no
	// se pudo escribir: en ese caso el agente funciona hasta el próximo reinicio.
	Persisted bool
}

// Config del resolutor.
type Config struct {
	APIURL         string // https://api.rampa.dev (acepta http, https, ws, wss)
	ClusterToken   string // RAMPA_CLUSTER_TOKEN
	BootstrapToken string // RAMPA_BOOTSTRAP_TOKEN
	Namespace      string // default rampa-system
	SecretName     string // default rampa-agent-token
	SecretKey      string // default token
	AgentVersion   string
	// Client es el clientset con el que se lee y escribe el Secret. Requerido
	// salvo que ClusterToken esté seteado.
	Client     kubernetes.Interface
	HTTPClient *http.Client
	Logger     *slog.Logger
	MinBackoff time.Duration
	MaxBackoff time.Duration
	MaxWait    time.Duration
}

// response es lo que devuelve POST /v1/agent/bootstrap.
type response struct {
	ClusterToken string `json:"cluster_token"`
	ClusterID    string `json:"cluster_id"`
	APIURL       string `json:"api_url"`
}

// apiError es el cuerpo de error de la API: {"error": {"code", "message"}}.
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Resolve devuelve el token del cluster siguiendo el orden de fuentes del
// paquete. Bloquea hasta resolverlo, hasta MaxWait o hasta que ctx termine.
func Resolve(ctx context.Context, cfg Config) (Token, error) {
	cfg = cfg.withDefaults()
	log := cfg.Logger

	if cfg.ClusterToken != "" {
		log.Info("token del cluster tomado de RAMPA_CLUSTER_TOKEN")
		return Token{Value: cfg.ClusterToken, Source: SourceEnv}, nil
	}
	if cfg.Client == nil {
		return Token{}, errors.New("sin RAMPA_CLUSTER_TOKEN necesito acceso a Kubernetes para leer el Secret del token")
	}
	var endpoint string
	if cfg.BootstrapToken != "" {
		var err error
		endpoint, err = bootstrapURL(cfg.APIURL)
		if err != nil {
			return Token{}, err
		}
	}

	secretRef := cfg.Namespace + "/" + cfg.SecretName
	deadline := time.Now().Add(cfg.MaxWait)
	attempt := 0
	used := false
	// pending es un token ya canjeado que todavía no pudimos guardar en el
	// Secret. No se vuelve a canjear: el bootstrap token ya quedó consumido.
	var pending *response

	for {
		if pending != nil {
			err := cfg.saveSecret(ctx, pending.ClusterToken)
			switch {
			case err == nil:
				log.Info("token del cluster guardado en el Secret", "secret", secretRef, "cluster_id", pending.ClusterID)
				return Token{Value: pending.ClusterToken, Source: SourceBootstrap, ClusterID: pending.ClusterID, Persisted: true}, nil
			case isPermissionError(err):
				log.Error("canjeé el bootstrap token pero el ServiceAccount no tiene permiso para escribir el Secret; el agente funciona hasta el próximo reinicio, porque el bootstrap token ya quedó usado. Dale al agente el Role del chart (rbac.create) o creá el Secret a mano y reiniciá",
					"secret", secretRef, "err", err)
				return Token{Value: pending.ClusterToken, Source: SourceBootstrap, ClusterID: pending.ClusterID}, nil
			case time.Now().After(deadline):
				log.Error("canjeé el bootstrap token pero no pude guardar el Secret; el agente funciona hasta el próximo reinicio", "secret", secretRef, "err", err)
				return Token{Value: pending.ClusterToken, Source: SourceBootstrap, ClusterID: pending.ClusterID}, nil
			default:
				log.Warn("no pude guardar el token en el Secret, reintento", "secret", secretRef, "err", err)
			}
		} else {
			tok, err := cfg.readSecret(ctx)
			switch {
			case err != nil && isPermissionError(err):
				return Token{}, fmt.Errorf("el ServiceAccount del agente no tiene permiso para leer el Secret %s: instalá el chart con rbac.create=true (crea un Role acotado a ese Secret) o dale permiso de get sobre secrets en su namespace: %w", secretRef, err)
			case err != nil:
				log.Warn("no pude leer el Secret del token, reintento", "secret", secretRef, "err", err)
			case tok != "":
				log.Info("token del cluster tomado del Secret", "secret", secretRef)
				return Token{Value: tok, Source: SourceSecret, Persisted: true}, nil
			case cfg.BootstrapToken == "":
				return Token{}, fmt.Errorf("no encontré el token del cluster: RAMPA_CLUSTER_TOKEN está vacía, el Secret %s no existe o no tiene la clave %q, y RAMPA_BOOTSTRAP_TOKEN está vacía", secretRef, cfg.SecretKey)
			case used:
				log.Info("el bootstrap token ya se usó o venció; sigo esperando a que el Secret aparezca", "secret", secretRef)
			default:
				res, err := cfg.exchange(ctx, endpoint)
				switch {
				case err == nil:
					log.Info("bootstrap token canjeado por el token del cluster", "cluster_id", res.ClusterID)
					if res.APIURL != "" && !sameAPI(res.APIURL, cfg.APIURL) {
						log.Warn("la API informó otra URL pública que la configurada en RAMPA_API_URL; sigo usando la configurada", "api_url", res.APIURL, "configured", cfg.APIURL)
					}
					pending = res
					continue
				case errors.Is(err, ErrBootstrapUsed):
					used = true
					log.Warn("la API rechazó el bootstrap token (HTTP 401): ya se usó o venció. Si otro proceso lo canjeó, el token aparece en el Secret; si no, generá otro bootstrap token desde la web", "secret", secretRef)
				case ctx.Err() != nil:
					return Token{}, ctx.Err()
				default:
					log.Warn("el canje del bootstrap token falló, reintento", "err", err)
				}
			}
		}

		if time.Now().After(deadline) {
			switch {
			case used:
				return Token{}, fmt.Errorf("el bootstrap token ya se usó o venció y el Secret %s no apareció en %s; generá un bootstrap token nuevo (install-command en la web) o creá el Secret con el token del cluster", secretRef, cfg.MaxWait)
			case cfg.BootstrapToken == "":
				return Token{}, fmt.Errorf("no pude leer el Secret %s en %s; revisá el estado del API server", secretRef, cfg.MaxWait)
			default:
				return Token{}, fmt.Errorf("no pude canjear el bootstrap token en %s; revisá que el cluster tenga salida HTTPS hacia %s", cfg.MaxWait, cfg.APIURL)
			}
		}
		wait := cfg.backoff(attempt)
		attempt++
		select {
		case <-ctx.Done():
			return Token{}, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (c Config) withDefaults() Config {
	c.APIURL = strings.TrimSpace(c.APIURL)
	c.ClusterToken = strings.TrimSpace(c.ClusterToken)
	c.BootstrapToken = strings.TrimSpace(c.BootstrapToken)
	if c.Namespace == "" {
		c.Namespace = DefaultNamespace
	}
	if c.SecretName == "" {
		c.SecretName = SecretName
	}
	if c.SecretKey == "" {
		c.SecretKey = SecretKey
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: requestTimeout}
	}
	if c.MinBackoff <= 0 {
		c.MinBackoff = defaultMinBackoff
	}
	if c.MaxBackoff < c.MinBackoff {
		c.MaxBackoff = defaultMaxBackoff
		if c.MaxBackoff < c.MinBackoff {
			c.MaxBackoff = c.MinBackoff
		}
	}
	if c.MaxWait <= 0 {
		c.MaxWait = DefaultMaxWait
	}
	if c.AgentVersion == "" {
		c.AgentVersion = "dev"
	}
	return c
}

// bootstrapURL arma la URL del canje a partir de RAMPA_API_URL.
func bootstrapURL(api string) (string, error) {
	u, err := url.Parse(strings.TrimRight(api, "/"))
	if err != nil {
		return "", fmt.Errorf("RAMPA_API_URL inválida: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return "", fmt.Errorf("RAMPA_API_URL inválida: esquema %q (esperaba http o https)", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("RAMPA_API_URL inválida: sin host")
	}
	u.Path = strings.TrimRight(u.Path, "/") + Path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// sameAPI compara dos URLs de API ignorando esquema ws/http y barras finales.
func sameAPI(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimRight(strings.TrimSpace(s), "/")
		s = strings.TrimPrefix(s, "wss://")
		s = strings.TrimPrefix(s, "ws://")
		s = strings.TrimPrefix(s, "https://")
		s = strings.TrimPrefix(s, "http://")
		return s
	}
	return norm(a) == norm(b)
}

// exchange hace el POST del canje. Devuelve ErrBootstrapUsed con 401; cualquier
// otro fallo es reintentable.
func (c Config) exchange(ctx context.Context, endpoint string) (*response, error) {
	body, err := json.Marshal(map[string]string{"bootstrap_token": c.BootstrapToken})
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("no pude armar la petición de canje: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "rampa-agent/"+c.AgentVersion)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no pude llamar a %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return nil, fmt.Errorf("leyendo la respuesta del canje (HTTP %d): %w", resp.StatusCode, err)
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		var res response
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("la respuesta del canje no es JSON válido: %w", err)
		}
		if strings.TrimSpace(res.ClusterToken) == "" {
			return nil, errors.New("la respuesta del canje no trae cluster_token")
		}
		res.ClusterToken = strings.TrimSpace(res.ClusterToken)
		return &res, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrBootstrapUsed
	default:
		// Solo el código y el mensaje estructurado de la API: nunca el cuerpo
		// crudo, por si trae algo que no queremos en los logs.
		var ae apiError
		if json.Unmarshal(raw, &ae) == nil && ae.Error.Code != "" {
			return nil, fmt.Errorf("la API respondió HTTP %d al canje: %s (%s)", resp.StatusCode, ae.Error.Message, ae.Error.Code)
		}
		return nil, fmt.Errorf("la API respondió HTTP %d al canje", resp.StatusCode)
	}
}

// readSecret devuelve el token guardado en el Secret, o "" si el Secret no
// existe o no tiene la clave.
func (c Config) readSecret(ctx context.Context) (string, error) {
	s, err := c.Client.CoreV1().Secrets(c.Namespace).Get(ctx, c.SecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(s.Data[c.SecretKey]))
	if tok == "" {
		c.Logger.Warn("el Secret del token existe pero no tiene la clave o está vacía", "secret", c.Namespace+"/"+c.SecretName, "key", c.SecretKey)
	}
	return tok, nil
}

// saveSecret crea o actualiza el Secret con el token.
func (c Config) saveSecret(ctx context.Context, token string) error {
	secrets := c.Client.CoreV1().Secrets(c.Namespace)
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "rampa",
		"app.kubernetes.io/part-of":    "rampa",
		"rampa.dev/component":          "agent",
	}
	existing, err := secrets.Get(ctx, c.SecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: c.SecretName, Namespace: c.Namespace, Labels: labels},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{c.SecretKey: []byte(token)},
		}
		_, err = secrets.Create(ctx, s, metav1.CreateOptions{FieldManager: fieldManager})
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		// Otro proceso lo creó entre el Get y el Create: seguimos por la
		// rama de actualización.
		existing, err = secrets.Get(ctx, c.SecretName, metav1.GetOptions{})
	}
	if err != nil {
		return err
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	existing.Data[c.SecretKey] = []byte(token)
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	for k, v := range labels {
		existing.Labels[k] = v
	}
	_, err = secrets.Update(ctx, existing, metav1.UpdateOptions{FieldManager: fieldManager})
	return err
}

// isPermissionError dice si el API server negó la operación por RBAC.
func isPermissionError(err error) bool {
	return apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err)
}

// backoff exponencial con jitter, acotado entre MinBackoff y MaxBackoff.
func (c Config) backoff(attempt int) time.Duration {
	if attempt > 30 {
		attempt = 30
	}
	d := c.MinBackoff << uint(attempt)
	if d <= 0 || d > c.MaxBackoff {
		d = c.MaxBackoff
	}
	if half := d / 2; half > 0 {
		d = half + time.Duration(rand.Int64N(int64(half)+1))
	}
	if d < c.MinBackoff {
		d = c.MinBackoff
	}
	if d > c.MaxBackoff {
		d = c.MaxBackoff
	}
	return d
}
