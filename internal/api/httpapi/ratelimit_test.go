package httpapi

import (
	"fmt"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	l := newRateLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.Allow("ip:1.2.3.4") {
			t.Fatalf("intento %d debería pasar", i+1)
		}
	}
	if l.Allow("ip:1.2.3.4") {
		t.Error("el cuarto intento en la ventana debería rechazarse")
	}
	// Otra clave no se ve afectada.
	if !l.Allow("ip:5.6.7.8") || !l.Allow("token:abc") {
		t.Error("otras claves tienen su propio límite")
	}
	// Los rechazos no corren la ventana: al pasar el minuto, entra de nuevo.
	now = now.Add(30 * time.Second)
	if l.Allow("ip:1.2.3.4") {
		t.Error("a los 30 s sigue bloqueado")
	}
	now = now.Add(31 * time.Second)
	if !l.Allow("ip:1.2.3.4") {
		t.Error("pasada la ventana debería entrar")
	}
	// Las claves viejas se limpian.
	now = now.Add(5 * time.Minute)
	l.Allow("ip:9.9.9.9")
	if _, still := l.hits["token:abc"]; still {
		t.Error("gc debería borrar las claves sin intentos recientes")
	}
}

// El mapa del limitador no crece sin cota: con claves siempre nuevas se llena
// y rechaza (falla cerrado); las claves vencidas se liberan con el gc.
func TestRateLimiterBoundedKeys(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	l := newRateLimiter(3, time.Minute)
	l.maxKeys = 100
	l.now = func() time.Time { return now }
	allowed := 0
	for i := 0; i < 1000; i++ {
		if l.Allow(fmt.Sprintf("ip:%d", i)) {
			allowed++
		}
	}
	if len(l.hits) > 100 || allowed != 100 {
		t.Errorf("el mapa debería quedar en 100 claves: %d claves, %d permitidas", len(l.hits), allowed)
	}
	// Una clave conocida sigue funcionando con el mapa lleno.
	if !l.Allow("ip:0") {
		t.Error("una clave existente debería seguir contando")
	}
	// Vencida la ventana, el gc libera lugar para claves nuevas.
	now = now.Add(2 * time.Minute)
	if !l.Allow("ip:nueva") {
		t.Error("tras la ventana debería haber lugar")
	}
	if len(l.hits) != 1 {
		t.Errorf("gc: %d claves", len(l.hits))
	}
}

// El gc corre más de una vez por ventana.
func TestRateLimiterGCMoreThanOncePerWindow(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	l := newRateLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	l.Allow("a")
	now = now.Add(31 * time.Second)
	l.Allow("b")
	now = now.Add(31 * time.Second) // "a" venció; pasaron 31 s desde el último gc
	l.Allow("c")
	if _, ok := l.hits["a"]; ok {
		t.Error("a debería haberse limpiado sin esperar una ventana completa")
	}
}
