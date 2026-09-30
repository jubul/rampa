package httpapi

import (
	"sync"
	"time"
)

// maxLimiterKeys es el tope de claves distintas del limitador: acota la
// memoria aunque un atacante mande intentos con claves siempre nuevas.
const maxLimiterKeys = 10000

// rateLimiter es un límite de intentos por clave con ventana deslizante, en
// memoria y por proceso. Alcanza para acotar el canje de bootstrap tokens:
// un atacante remoto no puede probar tokens al azar más rápido que el límite,
// ni desde una IP ni contra un token en particular.
type rateLimiter struct {
	limit   int
	window  time.Duration
	maxKeys int
	now     func() time.Time

	mu   sync.Mutex
	hits map[string][]time.Time
	last time.Time // última limpieza de claves viejas
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, maxKeys: maxLimiterKeys, now: time.Now, hits: map[string][]time.Time{}}
}

// Allow registra un intento para la clave y dice si entra en el límite. Un
// intento rechazado no se cuenta: la ventana no se corre sola. Con el mapa
// lleno de claves vivas, una clave nueva se rechaza (falla cerrado).
func (l *rateLimiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	// Limpieza periódica, más seguido que una vez por ventana.
	if now.Sub(l.last) > l.window/2 {
		l.gc(now)
		l.last = now
	}
	prev, known := l.hits[key]
	if !known && len(l.hits) >= l.maxKeys {
		l.gc(now)
		l.last = now
		if len(l.hits) >= l.maxKeys {
			return false
		}
	}
	cutoff := now.Add(-l.window)
	kept := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

// gc borra las claves sin intentos dentro de la ventana.
func (l *rateLimiter) gc(now time.Time) {
	cutoff := now.Add(-l.window)
	for k, ts := range l.hits {
		if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
			delete(l.hits, k)
		}
	}
}
