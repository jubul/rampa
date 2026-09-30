package infra

import (
	"sort"
	"strings"
	"sync"
)

// minSecretLen es el largo mínimo de un valor para redactarlo: uno más corto
// aparece por casualidad en cualquier línea y rompería el log.
const minSecretLen = 8

// redactMask reemplaza cada ocurrencia de un secreto.
const redactMask = "***"

// redactor reemplaza por "***" los valores secretos conocidos de un run
// (token de estado, bootstrap token, token del agente y credenciales de la
// cuenta) en cada línea de salida de tofu, antes de guardarla, emitirla o
// usarla en un mensaje de error. Es seguro para uso concurrente.
type redactor struct {
	mu      sync.RWMutex
	secrets []string
}

// add registra valores secretos; ignora los vacíos y los de menos de
// minSecretLen caracteres. Los más largos se aplican primero para que un
// secreto que contiene a otro se oculte entero.
func (r *redactor) add(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if len(v) < minSecretLen {
			continue
		}
		dup := false
		for _, s := range r.secrets {
			if s == v {
				dup = true
				break
			}
		}
		if !dup {
			r.secrets = append(r.secrets, v)
		}
	}
	sort.SliceStable(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
}

// redact devuelve s sin ninguno de los secretos registrados. Un redactor nil
// no cambia nada.
func (r *redactor) redact(s string) string {
	if r == nil {
		return s
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, v := range r.secrets {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, redactMask)
		}
	}
	return s
}
