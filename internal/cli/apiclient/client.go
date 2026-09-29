// Package apiclient es el cliente HTTP del CLI contra la API de Rampa (v1).
// Solo cubre las rutas que el CLI usa. Sin dependencias fuera de la stdlib.
package apiclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout es el timeout por request normal (no aplica al stream SSE).
const DefaultTimeout = 30 * time.Second

// Client habla con la API. Cero valor no sirve: usá New.
type Client struct {
	baseURL   string
	token     string
	http      *http.Client // requests normales, con timeout
	stream    *http.Client // SSE, sin timeout: lo corta el contexto
	userAgent string
}

// New crea un cliente. base es la URL de la API sin /v1 (ej. https://api.rampa.dev).
func New(base, token string) *Client {
	base = strings.TrimRight(base, "/")
	return &Client{
		baseURL:   base,
		token:     token,
		http:      &http.Client{Timeout: DefaultTimeout},
		stream:    &http.Client{},
		userAgent: "rampa-cli",
	}
}

// WithUserAgent setea el User-Agent (por ejemplo con la versión del CLI).
func (c *Client) WithUserAgent(ua string) *Client {
	c.userAgent = ua
	return c
}

// WithTimeout cambia el timeout de los requests normales.
func (c *Client) WithTimeout(d time.Duration) *Client {
	c.http.Timeout = d
	return c
}

// BaseURL devuelve la URL base configurada.
func (c *Client) BaseURL() string { return c.baseURL }

// Error es un error devuelto por la API, con el code y message del cuerpo
// {"error": {"code": "...", "message": "...", "details": [...]}}.
type Error struct {
	Status  int
	Code    string
	Message string
	Details []string
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.Code != "" {
		fmt.Fprintf(&b, "%s", e.Code)
	} else {
		fmt.Fprintf(&b, "http %d", e.Status)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	for _, d := range e.Details {
		fmt.Fprintf(&b, "\n  - %s", d)
	}
	return b.String()
}

// IsCode dice si err es un Error de la API con ese code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// IsConflict es un atajo para el code "conflict" (o HTTP 409 sin cuerpo).
func IsConflict(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == "conflict" || (e.Code == "" && e.Status == http.StatusConflict)
}

// IsNotFound es un atajo para "not_found" (o HTTP 404 sin cuerpo).
func IsNotFound(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == "not_found" || (e.Code == "" && e.Status == http.StatusNotFound)
}

type errorBody struct {
	Error struct {
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Details []string `json:"details"`
	} `json:"error"`
}

func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("serializando el cuerpo: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do ejecuta un request JSON y decodifica la respuesta en out (si no es nil).
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	req, err := c.newRequest(ctx, method, path, in)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("%s %s: leyendo la respuesta: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		return parseError(resp.StatusCode, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: la API devolvió un cuerpo que no entiendo: %w", method, path, err)
	}
	return nil
}

func parseError(status int, raw []byte) error {
	e := &Error{Status: status}
	var body errorBody
	if json.Unmarshal(raw, &body) == nil && (body.Error.Code != "" || body.Error.Message != "") {
		e.Code = body.Error.Code
		e.Message = body.Error.Message
		e.Details = body.Error.Details
		return e
	}
	// Sin cuerpo JSON: usamos el texto del status y un code razonable.
	e.Message = http.StatusText(status)
	switch status {
	case http.StatusUnauthorized:
		e.Code = "unauthorized"
	case http.StatusForbidden:
		e.Code = "forbidden"
	case http.StatusNotFound:
		e.Code = "not_found"
	case http.StatusConflict:
		e.Code = "conflict"
	}
	if txt := strings.TrimSpace(string(raw)); txt != "" && len(txt) < 300 {
		e.Message = txt
	}
	return e
}

func orgPath(org string, rest string) string {
	return "/v1/orgs/" + url.PathEscape(org) + rest
}

func appPath(org, app, rest string) string {
	return orgPath(org, "/apps/"+url.PathEscape(app)+rest)
}

func scopePath(org, app, scope, rest string) string {
	return appPath(org, app, "/scopes/"+url.PathEscape(scope)+rest)
}

// Me devuelve el usuario del token y sus organizaciones.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var me Me
	if err := c.do(ctx, http.MethodGet, "/v1/me", nil, &me); err != nil {
		return nil, err
	}
	return &me, nil
}

// CreateApp importa una application desde su rampa.yaml.
func (c *Client) CreateApp(ctx context.Context, org string, req CreateAppRequest) (*Application, error) {
	var app Application
	if err := c.do(ctx, http.MethodPost, orgPath(org, "/apps"), req, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// PutManifest reemplaza el manifest de una application existente.
func (c *Client) PutManifest(ctx context.Context, org, app, manifest string) (*Application, error) {
	var out Application
	if err := c.do(ctx, http.MethodPut, appPath(org, app, "/manifest"), PutManifestRequest{Manifest: manifest}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetApp devuelve una application.
func (c *Client) GetApp(ctx context.Context, org, app string) (*Application, error) {
	var out Application
	if err := c.do(ctx, http.MethodGet, appPath(org, app, ""), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListScopes devuelve los scopes de una app con su estado en runtime.
func (c *Client) ListScopes(ctx context.Context, org, app string) ([]Scope, error) {
	var out list[Scope]
	if err := c.do(ctx, http.MethodGet, appPath(org, app, "/scopes"), nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// BindScope ata un scope a un cluster.
func (c *Client) BindScope(ctx context.Context, org, app, scope, clusterID string) (*Scope, error) {
	var out Scope
	if err := c.do(ctx, http.MethodPut, scopePath(org, app, scope, ""), BindScopeRequest{ClusterID: clusterID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteScope encola el undeploy de un scope.
func (c *Client) DeleteScope(ctx context.Context, org, app, scope string) error {
	return c.do(ctx, http.MethodDelete, scopePath(org, app, scope, ""), nil, nil)
}

// CreateDeployment pide un deploy de una imagen a un scope.
func (c *Client) CreateDeployment(ctx context.Context, org, app, scope string, req CreateDeploymentRequest) (*Deployment, error) {
	var out Deployment
	if err := c.do(ctx, http.MethodPost, scopePath(org, app, scope, "/deployments"), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDeployment devuelve un deployment por id.
func (c *Client) GetDeployment(ctx context.Context, org, id string) (*Deployment, error) {
	var out Deployment
	if err := c.do(ctx, http.MethodGet, orgPath(org, "/deployments/"+url.PathEscape(id)), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ErrStopStream lo devuelve el callback de StreamEvents para cortar el stream sin error.
var ErrStopStream = errors.New("stop stream")

// StreamEvents sigue GET /events/stream (Server-Sent Events) filtrado por
// subject y llama fn por cada evento. Termina cuando fn devuelve ErrStopStream
// (sin error), cuando fn devuelve otro error (lo propaga), cuando el contexto
// se cancela (devuelve ctx.Err()) o cuando el servidor cierra la conexión
// (devuelve io.EOF para que el que llama decida si reconecta).
func (c *Client) StreamEvents(ctx context.Context, org, subjectType, subjectID string, fn func(Event) error) error {
	q := url.Values{}
	if subjectType != "" {
		q.Set("subject_type", subjectType)
	}
	if subjectID != "" {
		q.Set("subject_id", subjectID)
	}
	path := orgPath(org, "/events/stream")
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := c.stream.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return parseError(resp.StatusCode, raw)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	var data bytes.Buffer
	dispatch := func() error {
		if data.Len() == 0 {
			return nil
		}
		defer data.Reset()
		var ev Event
		if err := json.Unmarshal(data.Bytes(), &ev); err != nil {
			return fmt.Errorf("evento SSE que no entiendo: %w", err)
		}
		return fn(ev)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := dispatch(); err != nil {
				if errors.Is(err, ErrStopStream) {
					return nil
				}
				return err
			}
		case strings.HasPrefix(line, ":"):
			// comentario o ping, se ignora
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		default:
			// event:, id:, retry: no nos hacen falta: el kind viene en el JSON
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("leyendo el stream de eventos: %w", err)
	}
	// Último evento sin línea en blanco final.
	if err := dispatch(); err != nil {
		if errors.Is(err, ErrStopStream) {
			return nil
		}
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return io.EOF
}
