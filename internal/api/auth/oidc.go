package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCConfig es lo que hace falta para el login federado.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string // <RAMPA_PUBLIC_URL>/v1/auth/oidc/callback
}

// OIDCStateCookie guarda el state firmado entre start y callback.
const OIDCStateCookie = "rampa_oidc_state"

// OIDC implementa el flujo authorization code con go-oidc. El discovery del
// issuer se hace en el primer uso, así arrancar no depende del IdP.
type OIDC struct {
	cfg      OIDCConfig
	sessions *Sessions
	secure   bool

	mu       sync.Mutex
	provider *oidc.Provider
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewOIDC arma el cliente. Devuelve nil si la configuración está incompleta.
func NewOIDC(cfg OIDCConfig, sessions *Sessions, secure bool) *OIDC {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil
	}
	return &OIDC{cfg: cfg, sessions: sessions, secure: secure}
}

func (o *OIDC) init(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(ctx, o.cfg.Issuer)
	if err != nil {
		return fmt.Errorf("descubriendo el issuer OIDC: %w", err)
	}
	o.provider = p
	o.oauth = &oauth2.Config{
		ClientID:     o.cfg.ClientID,
		ClientSecret: o.cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  o.cfg.RedirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	o.verifier = p.Verifier(&oidc.Config{ClientID: o.cfg.ClientID})
	return nil
}

// Start redirige al IdP con un state firmado guardado en cookie.
func (o *OIDC) Start(w http.ResponseWriter, r *http.Request) error {
	if err := o.init(r.Context()); err != nil {
		return err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	state := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     OIDCStateCookie,
		Value:    o.sessions.SignState(state),
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   o.secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, o.oauth.AuthCodeURL(state), http.StatusFound)
	return nil
}

// Identity es lo que sacamos del id_token.
type Identity struct {
	Subject string
	Email   string
	Name    string
}

// Callback valida state y code y devuelve la identidad. No abre sesión: eso
// lo hace el handler, que conoce el store.
func (o *OIDC) Callback(w http.ResponseWriter, r *http.Request) (*Identity, error) {
	if err := o.init(r.Context()); err != nil {
		return nil, err
	}
	if e := r.URL.Query().Get("error"); e != "" {
		return nil, fmt.Errorf("el IdP devolvió un error: %s", e)
	}
	c, err := r.Cookie(OIDCStateCookie)
	if err != nil {
		return nil, errors.New("falta la cookie de state; volvé a iniciar el login")
	}
	http.SetCookie(w, &http.Cookie{Name: OIDCStateCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: o.secure, SameSite: http.SameSiteLaxMode})
	expected, err := o.sessions.VerifyState(c.Value)
	if err != nil || expected != r.URL.Query().Get("state") {
		return nil, errors.New("state inválido")
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		return nil, errors.New("falta el code")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tok, err := o.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("intercambiando el code: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, errors.New("el IdP no devolvió id_token")
	}
	idTok, err := o.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("id_token inválido: %w", err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idTok.Claims(&claims); err != nil {
		return nil, fmt.Errorf("leyendo claims: %w", err)
	}
	if claims.Email == "" {
		return nil, errors.New("el id_token no trae email")
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		return nil, errors.New("el email no está verificado en el IdP")
	}
	return &Identity{Subject: idTok.Subject, Email: claims.Email, Name: claims.Name}, nil
}
