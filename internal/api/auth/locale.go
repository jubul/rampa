package auth

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Idiomas de la plataforma. El default es español.
const (
	LocaleES      = "es"
	LocaleEN      = "en"
	DefaultLocale = LocaleES
)

// SupportedLocales son los idiomas que acepta PATCH /me y Accept-Language.
var SupportedLocales = []string{LocaleES, LocaleEN}

type localeKey struct{}

// WithLocale anota el idioma resuelto en el contexto.
func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeKey{}, locale)
}

// Locale devuelve el idioma del contexto, o el default si no se resolvió.
func Locale(ctx context.Context) string {
	if v, ok := ctx.Value(localeKey{}).(string); ok && v != "" {
		return v
	}
	return DefaultLocale
}

// ValidLocale dice si el valor es uno de los idiomas soportados.
func ValidLocale(s string) bool {
	for _, l := range SupportedLocales {
		if l == s {
			return true
		}
	}
	return false
}

// ResolveLocale elige el idioma: la preferencia guardada del usuario gana; si
// no hay, el primer idioma soportado del header Accept-Language; si no, es.
func ResolveLocale(userPreference, acceptLanguage string) string {
	if ValidLocale(userPreference) {
		return userPreference
	}
	if l := ParseAcceptLanguage(acceptLanguage); l != "" {
		return l
	}
	return DefaultLocale
}

// ParseAcceptLanguage devuelve el primer idioma soportado del header, en orden
// de preferencia (q), o "" si ninguno lo es. Solo mira el subtag primario:
// es-AR y es-419 son es; en-US y en-GB son en.
func ParseAcceptLanguage(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	type candidate struct {
		lang string
		q    float64
		pos  int
	}
	var cands []candidate
	for i, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lang, params, _ := strings.Cut(part, ";")
		lang = strings.ToLower(strings.TrimSpace(lang))
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			p = strings.TrimSpace(p)
			if v, ok := strings.CutPrefix(p, "q="); ok {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		if q <= 0 {
			continue
		}
		if primary, _, _ := strings.Cut(lang, "-"); primary != "" {
			cands = append(cands, candidate{lang: primary, q: q, pos: i})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].q != cands[j].q {
			return cands[i].q > cands[j].q
		}
		return cands[i].pos < cands[j].pos
	})
	for _, c := range cands {
		if ValidLocale(c.lang) {
			return c.lang
		}
	}
	return ""
}

// LocaleFromHeader es el middleware que resuelve el idioma desde
// Accept-Language para toda request, autenticada o no. RequireAuth lo vuelve a
// resolver con la preferencia del usuario cuando hay principal.
func LocaleFromHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithLocale(r.Context(), ResolveLocale("", r.Header.Get("Accept-Language")))))
	})
}
