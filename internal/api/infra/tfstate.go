package infra

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/store"
)

// Métodos del backend http de OpenTofu para el lock.
const (
	MethodLock   = "LOCK"
	MethodUnlock = "UNLOCK"
	// maxStateBytes acota el estado que aceptamos guardar.
	maxStateBytes = 64 << 20
)

func init() {
	// chi solo enruta métodos registrados.
	chi.RegisterMethod(MethodLock)
	chi.RegisterMethod(MethodUnlock)
}

// lockInfo es lo que manda OpenTofu en LOCK y UNLOCK.
type lockInfo struct {
	ID        string `json:"ID"`
	Operation string `json:"Operation,omitempty"`
	Info      string `json:"Info,omitempty"`
	Who       string `json:"Who,omitempty"`
	Version   string `json:"Version,omitempty"`
	Created   string `json:"Created,omitempty"`
	Path      string `json:"Path,omitempty"`
}

// StateHandler devuelve el backend http de estado, para montar en /v1/tfstate.
// Rutas: GET, POST, DELETE, LOCK y UNLOCK sobre /{run}. Autentica con el token
// del run (Bearer, o Basic con el token como contraseña, que es lo que manda
// OpenTofu con TF_HTTP_USERNAME/TF_HTTP_PASSWORD). Un run solo ve su estado.
func (s *Service) StateHandler() http.Handler {
	r := chi.NewRouter()
	r.Get("/{run}", s.stateGet)
	r.Post("/{run}", s.statePost)
	r.Delete("/{run}", s.stateDelete)
	r.MethodFunc(MethodLock, "/{run}", s.stateLock)
	r.MethodFunc(MethodUnlock, "/{run}", s.stateUnlock)
	return r
}

type stateError struct {
	status int
	msg    string
}

func (e *stateError) Error() string { return e.msg }

// authenticate resuelve el run del token y exige que coincida con el de la ruta.
func (s *Service) authenticate(r *http.Request) (*store.InfraRun, error) {
	if s.cipher == nil {
		return nil, &stateError{http.StatusNotImplemented, "el backend de estado está deshabilitado: falta RAMPA_STATE_KEY"}
	}
	token := tokenFromRequest(r)
	if token == "" || !strings.HasPrefix(token, StateTokenPrefix) {
		return nil, &stateError{http.StatusUnauthorized, "hace falta el token del run"}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	run, err := s.st.GetInfraRunByStateTokenHash(ctx, auth.HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, &stateError{http.StatusUnauthorized, "token desconocido"}
		}
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(run.ID), []byte(chi.URLParam(r, "run"))) != 1 {
		return nil, &stateError{http.StatusUnauthorized, "el token no corresponde a este run"}
	}
	return run, nil
}

// tokenFromRequest saca el token de Authorization: Bearer <t> o Basic <user:t>.
func tokenFromRequest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if tok, ok := auth.BearerToken(h); ok {
		return tok
	}
	const basic = "basic "
	if len(h) > len(basic) && strings.EqualFold(h[:len(basic)], basic) {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[len(basic):]))
		if err != nil {
			return ""
		}
		if i := strings.IndexByte(string(raw), ':'); i >= 0 {
			return string(raw[i+1:])
		}
	}
	return ""
}

func (s *Service) stateFail(w http.ResponseWriter, err error) {
	var se *stateError
	if errors.As(err, &se) {
		writeStateJSON(w, se.status, map[string]string{"error": se.msg})
		return
	}
	s.log.Error("backend de estado", "err", err)
	writeStateJSON(w, http.StatusInternalServerError, map[string]string{"error": "error interno"})
}

func writeStateJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Service) stateGet(w http.ResponseWriter, r *http.Request) {
	run, err := s.authenticate(r)
	if err != nil {
		s.stateFail(w, err)
		return
	}
	st, err := s.st.GetInfraState(r.Context(), run.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && len(st.Ciphertext) == 0) {
		writeStateJSON(w, http.StatusNotFound, map[string]string{"error": "sin estado"})
		return
	}
	if err != nil {
		s.stateFail(w, err)
		return
	}
	plain, err := s.cipher.Open(st.Ciphertext, []byte(run.ID))
	if err != nil {
		s.stateFail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(plain)
}

func (s *Service) statePost(w http.ResponseWriter, r *http.Request) {
	run, err := s.authenticate(r)
	if err != nil {
		s.stateFail(w, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStateBytes))
	if err != nil {
		s.stateFail(w, &stateError{http.StatusRequestEntityTooLarge, "el estado supera el máximo permitido"})
		return
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		s.stateFail(w, &stateError{http.StatusBadRequest, "el cuerpo está vacío"})
		return
	}
	// Con lock tomado, el escritor tiene que ser quien lo tiene (query ID).
	if current, err := s.currentLock(r.Context(), run.ID); err != nil {
		s.stateFail(w, err)
		return
	} else if current != nil && current.ID != r.URL.Query().Get("ID") {
		writeStateJSON(w, http.StatusConflict, current)
		return
	}
	ct, err := s.cipher.Seal(body, []byte(run.ID))
	if err != nil {
		s.stateFail(w, err)
		return
	}
	if err := s.st.PutInfraState(r.Context(), run.ID, ct); err != nil {
		s.stateFail(w, err)
		return
	}
	writeStateJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) stateDelete(w http.ResponseWriter, r *http.Request) {
	run, err := s.authenticate(r)
	if err != nil {
		s.stateFail(w, err)
		return
	}
	if err := s.st.DeleteInfraState(r.Context(), run.ID); err != nil {
		s.stateFail(w, err)
		return
	}
	writeStateJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) stateLock(w http.ResponseWriter, r *http.Request) {
	run, err := s.authenticate(r)
	if err != nil {
		s.stateFail(w, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.stateFail(w, &stateError{http.StatusBadRequest, "no pude leer el lock"})
		return
	}
	var info lockInfo
	if err := json.Unmarshal(body, &info); err != nil || info.ID == "" {
		s.stateFail(w, &stateError{http.StatusBadRequest, "el lock tiene que ser un JSON con ID"})
		return
	}
	acquired, current, err := s.st.LockInfraState(r.Context(), run.ID, json.RawMessage(body))
	if err != nil {
		s.stateFail(w, err)
		return
	}
	if acquired {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	var held lockInfo
	_ = json.Unmarshal(current, &held)
	if held.ID == info.ID {
		// El mismo dueño repite el LOCK: es idempotente.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(current)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusLocked)
	_, _ = w.Write(current)
}

func (s *Service) stateUnlock(w http.ResponseWriter, r *http.Request) {
	run, err := s.authenticate(r)
	if err != nil {
		s.stateFail(w, err)
		return
	}
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	var info lockInfo
	_ = json.Unmarshal(body, &info)
	current, err := s.currentLock(r.Context(), run.ID)
	if err != nil {
		s.stateFail(w, err)
		return
	}
	if current == nil {
		writeStateJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if info.ID != "" && info.ID != current.ID {
		writeStateJSON(w, http.StatusConflict, current)
		return
	}
	if err := s.st.UnlockInfraState(r.Context(), run.ID); err != nil {
		s.stateFail(w, err)
		return
	}
	writeStateJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// currentLock devuelve el lock vigente o nil.
func (s *Service) currentLock(ctx context.Context, runID string) (*lockInfo, error) {
	st, err := s.st.GetInfraState(ctx, runID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(st.LockInfo) == 0 {
		return nil, nil
	}
	var info lockInfo
	if err := json.Unmarshal(st.LockInfo, &info); err != nil {
		return nil, err
	}
	return &info, nil
}
