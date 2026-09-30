package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"rampa.dev/internal/api/store"
)

// PrincipalType distingue cómo se autenticó el llamador.
type PrincipalType string

const (
	PrincipalUser  PrincipalType = "user"
	PrincipalToken PrincipalType = "token"
)

// Principal es quién llama.
type Principal struct {
	Type  PrincipalType
	User  *store.User     // siempre presente: para tokens, es el usuario que lo creó
	Token *store.APIToken // solo para PrincipalToken
}

// ActorID es el id para auditoría.
func (p *Principal) ActorID() string {
	if p.Type == PrincipalToken && p.Token != nil {
		return p.Token.ID
	}
	if p.User != nil {
		return p.User.ID
	}
	return ""
}

// ActorName es el nombre para auditoría.
func (p *Principal) ActorName() string {
	if p.Type == PrincipalToken && p.Token != nil {
		return p.Token.Name
	}
	if p.User != nil {
		if p.User.Name != "" {
			return p.User.Name
		}
		return p.User.Email
	}
	return ""
}

// Actor arma el actor de un evento.
func (p *Principal) Actor() store.Actor {
	return store.Actor{Type: string(p.Type), ID: p.ActorID(), Name: p.ActorName()}
}

// OrgContext es la organización de la ruta y el rol del principal en ella.
type OrgContext struct {
	Org  *store.Organization
	Role Role
}

type ctxKey int

const (
	principalKey ctxKey = iota
	orgKey
)

// PrincipalFrom devuelve el principal del contexto, si hay.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey).(*Principal)
	return p, ok && p != nil
}

// OrgFrom devuelve la organización y rol del contexto, si hay.
func OrgFrom(ctx context.Context) (*OrgContext, bool) {
	o, ok := ctx.Value(orgKey).(*OrgContext)
	return o, ok && o != nil
}

// WithPrincipal y WithOrg existen para tests de handlers.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// WithOrg agrega el contexto de organización.
func WithOrg(ctx context.Context, o *OrgContext) context.Context {
	return context.WithValue(ctx, orgKey, o)
}

// ErrorWriter escribe un error en el formato de la API. Lo provee httpapi
// para que auth no dependa de él.
type ErrorWriter func(w http.ResponseWriter, status int, code, message string)

// Authenticator resuelve principales y membresías.
type Authenticator struct {
	Store    *store.Store
	Sessions *Sessions
	Log      *slog.Logger
	WriteErr ErrorWriter
	now      func() time.Time
}

// NewAuthenticator arma el autenticador.
func NewAuthenticator(st *store.Store, sessions *Sessions, log *slog.Logger, writeErr ErrorWriter) *Authenticator {
	if log == nil {
		log = slog.Default()
	}
	return &Authenticator{Store: st, Sessions: sessions, Log: log, WriteErr: writeErr, now: time.Now}
}

// Resolve identifica al llamador por header Bearer o por cookie. Devuelve
// (nil, nil) si no hay credenciales; error si las hay y son inválidas.
func (a *Authenticator) Resolve(r *http.Request) (*Principal, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if tok, ok := BearerToken(r.Header.Get("Authorization")); ok {
		if !IsAPIToken(tok) {
			return nil, errors.New("el token no tiene el formato rampa_<...>")
		}
		t, err := a.Store.GetAPITokenByHash(ctx, HashToken(tok))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, errors.New("token desconocido")
			}
			return nil, err
		}
		if t.Expired(a.now()) {
			return nil, errors.New("token vencido")
		}
		u, err := a.Store.GetUser(ctx, t.UserID)
		if err != nil {
			return nil, err
		}
		if err := a.Store.TouchAPIToken(ctx, t.ID); err != nil {
			a.Log.Warn("no pude registrar el uso del token", "token_id", t.ID, "err", err)
		}
		return &Principal{Type: PrincipalToken, User: u, Token: t}, nil
	}

	if _, err := r.Cookie(SessionCookie); err != nil {
		return nil, nil
	}
	userID, err := a.Sessions.UserFromRequest(r)
	if err != nil {
		return nil, err
	}
	u, err := a.Store.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, errors.New("la sesión apunta a un usuario que ya no existe")
		}
		return nil, err
	}
	return &Principal{Type: PrincipalUser, User: u}, nil
}

// RequireAuth exige un principal válido y lo deja en el contexto.
func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.Resolve(r)
		if err != nil {
			a.WriteErr(w, http.StatusUnauthorized, "unauthorized", "credenciales inválidas: "+err.Error())
			return
		}
		if p == nil {
			a.WriteErr(w, http.StatusUnauthorized, "unauthorized", "hace falta iniciar sesión o mandar un token de API")
			return
		}
		ctx := WithPrincipal(r.Context(), p)
		// El idioma guardado del usuario gana sobre Accept-Language.
		if p.User != nil {
			ctx = WithLocale(ctx, ResolveLocale(p.User.Locale, r.Header.Get("Accept-Language")))
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireOrg resuelve {org} de la ruta y exige que el principal sea miembro.
// Un no miembro recibe 404: la organización no existe para él.
func (a *Authenticator) RequireOrg(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			a.WriteErr(w, http.StatusUnauthorized, "unauthorized", "hace falta autenticarse")
			return
		}
		slug := chi.URLParam(r, "org")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		org, err := a.Store.GetOrgBySlug(ctx, slug)
		cancel()
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				a.WriteErr(w, http.StatusNotFound, "not_found", "organización no encontrada")
				return
			}
			a.Log.Error("buscando org", "slug", slug, "err", err)
			a.WriteErr(w, http.StatusInternalServerError, "internal", "error interno")
			return
		}

		if p.Type == PrincipalToken && p.Token.OrgID != org.ID {
			a.WriteErr(w, http.StatusNotFound, "not_found", "organización no encontrada")
			return
		}
		ctx, cancel = context.WithTimeout(r.Context(), 5*time.Second)
		role, err := a.EffectiveRole(ctx, p, org.ID)
		cancel()
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				a.WriteErr(w, http.StatusNotFound, "not_found", "organización no encontrada")
				return
			}
			a.Log.Error("buscando membresía", "org", slug, "err", err)
			a.WriteErr(w, http.StatusInternalServerError, "internal", "error interno")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithOrg(r.Context(), &OrgContext{Org: org, Role: role})))
	})
}

// EffectiveRole resuelve el rol del principal en la org a partir de la
// membresía actual del usuario. Para un token es el MENOR entre el rol del
// token y el de la membresía de quien lo creó: si a esa persona la sacaron de
// la org el token deja de valer (ErrNotFound), y si le bajaron el rol el token
// baja con ella. Así un token nunca otorga más de lo que su dueño tiene hoy.
func (a *Authenticator) EffectiveRole(ctx context.Context, p *Principal, orgID string) (Role, error) {
	if p == nil || p.User == nil {
		return "", ErrUnauthorized
	}
	m, err := a.Store.GetMembership(ctx, orgID, p.User.ID)
	if err != nil {
		return "", err
	}
	role := Role(m.Role)
	if p.Type == PrincipalToken {
		if p.Token == nil || p.Token.OrgID != orgID {
			return "", store.ErrNotFound
		}
		role = Min(role, Role(p.Token.Role))
	}
	return role, nil
}

// StillAuthorized dice si el principal conserva acceso a la org: la membresía
// sigue existiendo y, para un token, el token sigue en la base y sin vencer.
// Lo usan las conexiones largas (SSE) para cortar cuando el acceso se revoca.
func (a *Authenticator) StillAuthorized(ctx context.Context, p *Principal, orgID string) bool {
	if _, err := a.EffectiveRole(ctx, p, orgID); err != nil {
		return false
	}
	if p.Type == PrincipalToken {
		t, err := a.Store.GetAPITokenByHash(ctx, p.Token.Hash)
		if err != nil || t.OrgID != orgID || t.Expired(a.now()) {
			return false
		}
	}
	return true
}

// RequireRole exige un rol mínimo en la organización del contexto.
func (a *Authenticator) RequireRole(min Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			oc, ok := OrgFrom(r.Context())
			if !ok {
				a.WriteErr(w, http.StatusUnauthorized, "unauthorized", "hace falta autenticarse")
				return
			}
			if !oc.Role.AtLeast(min) {
				a.WriteErr(w, http.StatusForbidden, "forbidden", "esta acción requiere rol "+string(min)+" o superior")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CheckRole es la versión para usar dentro de un handler cuando el rol
// mínimo depende del recurso (por ejemplo, desplegar a prod).
func CheckRole(ctx context.Context, min Role) error {
	oc, ok := OrgFrom(ctx)
	if !ok {
		return ErrUnauthorized
	}
	if !oc.Role.AtLeast(min) {
		return &ForbiddenError{Min: min}
	}
	return nil
}

// ErrUnauthorized indica que no hay principal.
var ErrUnauthorized = errors.New("hace falta autenticarse")

// ForbiddenError indica que el rol no alcanza.
type ForbiddenError struct{ Min Role }

func (e *ForbiddenError) Error() string {
	return "esta acción requiere rol " + string(e.Min) + " o superior"
}
