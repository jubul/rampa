package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// trustedProxies devuelve un middleware que reemplaza r.RemoteAddr por la IP
// real del cliente, pero solo cuando la conexión viene de uno de los proxies
// de confianza. Desde cualquier otra dirección X-Forwarded-For y X-Real-IP se
// ignoran, así un cliente no puede falsear su IP para evadir límites. Con la
// lista vacía no toca nada.
//
// X-Forwarded-For se recorre de derecha a izquierda: el cliente es el primer
// salto que no es un proxy de confianza. Sin ese header se usa X-Real-IP.
func trustedProxies(nets []netip.Prefix) func(http.Handler) http.Handler {
	trusted := func(a netip.Addr) bool {
		a = a.Unmap()
		for _, p := range nets {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		if len(nets) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = strings.TrimSpace(r.RemoteAddr)
			}
			peer, err := netip.ParseAddr(host)
			if err == nil && trusted(peer) {
				if ip, ok := forwardedClient(r, trusted); ok {
					r.RemoteAddr = net.JoinHostPort(ip.String(), "0")
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// forwardedClient resuelve la IP del cliente a partir de los headers de reenvío.
func forwardedClient(r *http.Request, trusted func(netip.Addr) bool) (netip.Addr, bool) {
	if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
		var hops []string
		for _, v := range values {
			hops = append(hops, strings.Split(v, ",")...)
		}
		var last netip.Addr
		found := false
		for i := len(hops) - 1; i >= 0; i-- {
			ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			if err != nil {
				// Un salto ilegible corta la cadena: no se confía en lo que está a su izquierda.
				break
			}
			ip = ip.Unmap()
			last, found = ip, true
			if !trusted(ip) {
				return ip, true
			}
		}
		if found {
			// Toda la cadena es de confianza: el cliente es el salto más lejano legible.
			return last, true
		}
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		if ip, err := netip.ParseAddr(v); err == nil {
			return ip.Unmap(), true
		}
	}
	return netip.Addr{}, false
}
