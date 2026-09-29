package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"time"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/store"
)

// meResponse es Me de docs/api.md.
type meResponse struct {
	User        *store.User               `json:"user"`
	Memberships []store.MembershipWithOrg `json:"memberships"`
}

func (a *API) buildMe(ctx context.Context, p *auth.Principal) (*meResponse, error) {
	if p.Type == auth.PrincipalToken {
		org, err := a.Store.GetOrg(ctx, p.Token.OrgID)
		if err != nil {
			return nil, err
		}
		// El rol que se informa es el efectivo: el menor entre el del token y
		// el de la membresía actual de quien lo creó. Sin membresía, el token
		// no da acceso a ninguna org.
		role, err := a.Auth.EffectiveRole(ctx, p, org.ID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return &meResponse{User: p.User, Memberships: []store.MembershipWithOrg{}}, nil
			}
			return nil, err
		}
		return &meResponse{User: p.User, Memberships: []store.MembershipWithOrg{{Org: *org, Role: string(role)}}}, nil
	}
	ms, err := a.Store.ListOrgsForUser(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}
	return &meResponse{User: p.User, Memberships: ms}, nil
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	me, err := a.buildMe(r.Context(), principal(r))
	if err != nil {
		a.fail(w, r, err, "usuario")
		return
	}
	writeJSON(w, http.StatusOK, me)
}

func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s
}

// devLogin abre sesión sin IdP. Solo con RAMPA_DEV_LOGIN=true.
func (a *API) devLogin(w http.ResponseWriter, r *http.Request) {
	if !a.Config.DevLogin {
		writeError(w, http.StatusNotFound, "not_found", "dev-login está deshabilitado (RAMPA_DEV_LOGIN)")
		return
	}
	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.Email = store.NormalizeEmail(body.Email)
	if !validEmail(body.Email) {
		a.fail(w, r, invalid("email inválido"), "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	u, err := a.Store.UpsertUser(ctx, body.Email, body.Name)
	if err != nil {
		a.fail(w, r, err, "usuario")
		return
	}
	a.Sessions.SetCookie(w, u.ID)
	me, err := a.buildMe(ctx, &auth.Principal{Type: auth.PrincipalUser, User: u})
	if err != nil {
		a.fail(w, r, err, "usuario")
		return
	}
	a.Log.Info("dev-login", "user_id", u.ID)
	writeJSON(w, http.StatusOK, me)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	a.Sessions.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) oidcStart(w http.ResponseWriter, r *http.Request) {
	if a.OIDC == nil {
		writeError(w, http.StatusNotFound, "not_found", "OIDC no está configurado")
		return
	}
	if err := a.OIDC.Start(w, r); err != nil {
		a.Log.Error("oidc start", "err", err)
		writeError(w, http.StatusServiceUnavailable, "internal", "no pude iniciar el login: "+err.Error())
	}
}

func (a *API) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if a.OIDC == nil {
		writeError(w, http.StatusNotFound, "not_found", "OIDC no está configurado")
		return
	}
	id, err := a.OIDC.Callback(w, r)
	if err != nil {
		a.Log.Warn("oidc callback rechazado", "err", err)
		writeError(w, http.StatusUnauthorized, "unauthorized", "login rechazado: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	u, err := a.Store.UpsertUser(ctx, id.Email, id.Name)
	if err != nil {
		a.fail(w, r, err, "usuario")
		return
	}
	a.Sessions.SetCookie(w, u.ID)
	a.Log.Info("login oidc", "user_id", u.ID)
	http.Redirect(w, r, "/", http.StatusFound)
}

// ensureNotLastOwner evita dejar una org sin owner.
func (a *API) ensureNotLastOwner(ctx context.Context, orgID, userID string) error {
	m, err := a.Store.GetMembership(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if m.Role != store.RoleOwner {
		return nil
	}
	n, err := a.Store.CountOwners(ctx, orgID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return conflict("la organización no puede quedar sin owner")
	}
	return nil
}
