package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func resolvedIP(t *testing.T, proxies []string, remote string, headers map[string]string) string {
	t.Helper()
	var nets []netip.Prefix
	for _, p := range proxies {
		nets = append(nets, netip.MustParsePrefix(p))
	}
	var got string
	h := trustedProxies(nets)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = clientIP(r) }))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestTrustedProxiesMiddleware(t *testing.T) {
	proxies := []string{"10.0.0.0/8"}
	cases := []struct {
		name    string
		proxies []string
		remote  string
		headers map[string]string
		want    string
	}{
		{"sin proxies configurados ignora XFF", nil, "203.0.113.9:4000", map[string]string{"X-Forwarded-For": "1.2.3.4"}, "203.0.113.9"},
		{"cliente no confiable ignora XFF", proxies, "203.0.113.9:4000", map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "5.6.7.8"}, "203.0.113.9"},
		{"proxy confiable usa XFF", proxies, "10.1.1.1:4000", map[string]string{"X-Forwarded-For": "198.51.100.5"}, "198.51.100.5"},
		{"XFF falseado a la izquierda no cuenta", proxies, "10.1.1.1:4000", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.5, 10.2.2.2"}, "198.51.100.5"},
		{"proxy confiable usa X-Real-IP sin XFF", proxies, "10.1.1.1:4000", map[string]string{"X-Real-IP": "198.51.100.6"}, "198.51.100.6"},
		{"proxy confiable sin headers deja el socket", proxies, "10.1.1.1:4000", nil, "10.1.1.1"},
		{"XFF basura deja el socket", proxies, "10.1.1.1:4000", map[string]string{"X-Forwarded-For": "no-es-ip"}, "10.1.1.1"},
	}
	for _, c := range cases {
		if got := resolvedIP(t, c.proxies, c.remote, c.headers); got != c.want {
			t.Errorf("%s: esperaba %s, tengo %s", c.name, c.want, got)
		}
	}
}
