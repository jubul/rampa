// Package auth resuelve quién llama a la API: sesión web con cookie firmada,
// tokens de API para CLI y CI, tokens de cluster para los agentes, y el rol
// que el principal tiene en una organización.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SessionCookie es el nombre de la cookie de sesión.
const SessionCookie = "rampa_session"

// DefaultSessionTTL es la vida de una sesión web.
const DefaultSessionTTL = 7 * 24 * time.Hour

// Errores de sesión.
var (
	ErrBadSession     = errors.New("sesión inválida")
	ErrSessionExpired = errors.New("sesión vencida")
)

// Sessions firma y verifica cookies de sesión con HMAC-SHA256.
// El valor de la cookie es base64url(user_id|expires_unix) + "." + base64url(hmac).
type Sessions struct {
	secret []byte
	ttl    time.Duration
	secure bool
	now    func() time.Time
}

// NewSessions crea el firmador. secure marca la cookie como Secure (https).
func NewSessions(secret []byte, ttl time.Duration, secure bool) *Sessions {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &Sessions{secret: secret, ttl: ttl, secure: secure, now: time.Now}
}

func (s *Sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Issue devuelve el valor firmado de una sesión para userID y su vencimiento.
func (s *Sessions) Issue(userID string) (string, time.Time) {
	exp := s.now().Add(s.ttl).UTC()
	payload := base64.RawURLEncoding.EncodeToString([]byte(userID + "|" + strconv.FormatInt(exp.Unix(), 10)))
	return payload + "." + s.sign(payload), exp
}

// Verify valida la firma y el vencimiento y devuelve el user id.
func (s *Sessions) Verify(value string) (string, error) {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok || payload == "" || sig == "" {
		return "", ErrBadSession
	}
	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return "", ErrBadSession
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", ErrBadSession
	}
	userID, expRaw, ok := strings.Cut(string(raw), "|")
	if !ok || userID == "" {
		return "", ErrBadSession
	}
	exp, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil {
		return "", ErrBadSession
	}
	if s.now().Unix() >= exp {
		return "", ErrSessionExpired
	}
	return userID, nil
}

// SetCookie escribe la cookie de sesión en la respuesta.
func (s *Sessions) SetCookie(w http.ResponseWriter, userID string) {
	value, exp := s.Issue(userID)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie borra la cookie de sesión.
func (s *Sessions) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// UserFromRequest lee la cookie y devuelve el user id, o error si no hay sesión válida.
func (s *Sessions) UserFromRequest(r *http.Request) (string, error) {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return "", fmt.Errorf("sin cookie de sesión: %w", ErrBadSession)
	}
	return s.Verify(c.Value)
}

// SignState firma un valor corto (state de OIDC) con el mismo secreto.
func (s *Sessions) SignState(value string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(value))
	return payload + "." + s.sign(payload)
}

// VerifyState valida un state firmado y devuelve el valor original.
func (s *Sessions) VerifyState(signed string) (string, error) {
	payload, sig, ok := strings.Cut(signed, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return "", ErrBadSession
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", ErrBadSession
	}
	return string(raw), nil
}
