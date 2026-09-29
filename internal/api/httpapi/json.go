package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/store"
)

const maxBodyBytes = 1 << 20

// Error es el cuerpo de error de la API.
type Error struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error Error `json:"error"`
}

// apiError es un error con código HTTP ya decidido.
type apiError struct {
	status  int
	code    string
	message string
	details []string
}

func (e *apiError) Error() string { return e.message }

func invalid(msg string, details ...string) *apiError {
	return &apiError{status: http.StatusBadRequest, code: "invalid", message: msg, details: details}
}

func notFound(msg string) *apiError {
	return &apiError{status: http.StatusNotFound, code: "not_found", message: msg}
}

func conflict(msg string) *apiError {
	return &apiError{status: http.StatusConflict, code: "conflict", message: msg}
}

func forbidden(msg string) *apiError {
	return &apiError{status: http.StatusForbidden, code: "forbidden", message: msg}
}

// writeJSON escribe una respuesta JSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

// writeError escribe un error en el formato de la API.
func writeError(w http.ResponseWriter, status int, code, message string, details ...string) {
	writeJSON(w, status, errorEnvelope{Error: Error{Code: code, Message: message, Details: details}})
}

// WriteError es writeError exportado para que auth lo use como ErrorWriter.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	writeError(w, status, code, message)
}

// items envuelve listados como {"items": [...]}.
func items(v any) map[string]any { return map[string]any{"items": v} }

// decode lee el cuerpo JSON en v. Acota el tamaño y rechaza JSON malformado.
func decode(r *http.Request, v any) error {
	body := http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	defer body.Close()
	b, err := io.ReadAll(body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return invalid("el cuerpo supera 1 MiB")
		}
		return invalid("no pude leer el cuerpo: " + err.Error())
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return invalid("el cuerpo JSON está vacío")
	}
	if err := json.Unmarshal(b, v); err != nil {
		return invalid("JSON inválido: " + err.Error())
	}
	return nil
}

// fail traduce un error a la respuesta HTTP que corresponde.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error, what string) {
	var ae *apiError
	var fe *auth.ForbiddenError
	switch {
	case errors.As(err, &ae):
		writeError(w, ae.status, ae.code, ae.message, ae.details...)
	case errors.As(err, &fe):
		writeError(w, http.StatusForbidden, "forbidden", fe.Error())
	case errors.Is(err, auth.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
	case errors.Is(err, store.ErrNotFound):
		msg := "no encontrado"
		if what != "" {
			msg = what + " no encontrado"
		}
		writeError(w, http.StatusNotFound, "not_found", msg)
	case errors.Is(err, store.ErrConflict):
		msg := "conflicto"
		if what != "" {
			msg = what + ": ya existe o está en uso"
		}
		writeError(w, http.StatusConflict, "conflict", msg)
	default:
		a.Log.Error("error interno", "method", r.Method, "path", r.URL.Path, "what", what, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "error interno")
	}
}

// principal y org son atajos para leer el contexto. Los middlewares garantizan que existen.
func principal(r *http.Request) *auth.Principal {
	p, _ := auth.PrincipalFrom(r.Context())
	return p
}

func orgCtx(r *http.Request) *auth.OrgContext {
	o, _ := auth.OrgFrom(r.Context())
	return o
}

// requireString valida un campo obligatorio.
func requireString(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return invalid(fmt.Sprintf("%s es requerido", name))
	}
	return nil
}
