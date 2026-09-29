package httpapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/store"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38}[a-z0-9])?$`)

func (a *API) listOrgs(w http.ResponseWriter, r *http.Request) {
	me, err := a.buildMe(r.Context(), principal(r))
	if err != nil {
		a.fail(w, r, err, "organizaciones")
		return
	}
	orgs := make([]store.Organization, 0, len(me.Memberships))
	for _, m := range me.Memberships {
		orgs = append(orgs, m.Org)
	}
	writeJSON(w, http.StatusOK, items(orgs))
}

func (a *API) createOrg(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.Type != auth.PrincipalUser {
		a.fail(w, r, forbidden("las organizaciones se crean con una sesión de usuario, no con un token"), "")
		return
	}
	var body struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.Slug = strings.TrimSpace(body.Slug)
	body.Name = strings.TrimSpace(body.Name)
	if !slugRe.MatchString(body.Slug) {
		a.fail(w, r, invalid("slug inválido: minúsculas, números y guiones, de 3 a 40 caracteres"), "")
		return
	}
	if body.Name == "" {
		body.Name = body.Slug
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	org, err := a.Store.CreateOrg(ctx, body.Slug, body.Name, p.User.ID)
	if err != nil {
		a.fail(w, r, err, "organización "+body.Slug)
		return
	}
	a.Bus.Emit(ctx, org.ID, p.Actor(), "org.created", "org", org.ID, "organización "+org.Slug+" creada", map[string]string{"slug": org.Slug, "name": org.Name})
	writeJSON(w, http.StatusCreated, org)
}

func (a *API) getOrg(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, orgCtx(r).Org)
}

// updateOrg cambia nombre y settings. No está en docs/api.md pero sin esto
// require_approval_prod no se puede tocar; ver README.
func (a *API) updateOrg(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	var body struct {
		Name     *string            `json:"name"`
		Settings *store.OrgSettings `json:"settings"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	name := oc.Org.Name
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		name = strings.TrimSpace(*body.Name)
	}
	settings := oc.Org.Settings
	if body.Settings != nil {
		settings = *body.Settings
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	org, err := a.Store.UpdateOrg(ctx, oc.Org.ID, name, settings)
	if err != nil {
		a.fail(w, r, err, "organización")
		return
	}
	a.Bus.Emit(ctx, org.ID, principal(r).Actor(), "org.updated", "org", org.ID, "organización actualizada", map[string]any{"name": org.Name, "settings": org.Settings})
	writeJSON(w, http.StatusOK, org)
}

func (a *API) deleteOrg(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := a.Store.DeleteOrg(ctx, oc.Org.ID); err != nil {
		a.fail(w, r, err, "organización")
		return
	}
	a.Log.Info("organización borrada", "org", oc.Org.Slug, "by", principal(r).ActorID())
	w.WriteHeader(http.StatusNoContent)
}

// ---- members ----

func (a *API) listMembers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ms, err := a.Store.ListMembers(ctx, orgCtx(r).Org.ID)
	if err != nil {
		a.fail(w, r, err, "miembros")
		return
	}
	writeJSON(w, http.StatusOK, items(ms))
}

func (a *API) setMember(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
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
	role, err := auth.ParseRole(body.Role)
	if err != nil {
		a.fail(w, r, invalid(err.Error()), "")
		return
	}
	// Nadie puede otorgar un rol mayor al propio.
	if !oc.Role.AtLeast(role) {
		a.fail(w, r, forbidden("no podés otorgar un rol superior al tuyo"), "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	u, err := a.Store.UpsertUser(ctx, body.Email, "")
	if err != nil {
		a.fail(w, r, err, "usuario")
		return
	}
	current, err := a.Store.GetMembership(ctx, oc.Org.ID, u.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.fail(w, r, err, "membresía")
		return
	}
	demoted := false
	if current != nil {
		// Nadie puede tocar a alguien con rol superior al propio: un admin no
		// degrada a un owner, igual que no puede sacarlo (removeMember).
		if !oc.Role.AtLeast(auth.Role(current.Role)) {
			a.fail(w, r, forbidden("no podés cambiar el rol de alguien con rol superior al tuyo"), "")
			return
		}
		demoted = !role.AtLeast(auth.Role(current.Role))
		// Si era owner, no puede dejar de serlo si es el último.
		if current.Role == store.RoleOwner && role != auth.RoleOwner {
			if err := a.ensureNotLastOwner(ctx, oc.Org.ID, u.ID); err != nil {
				a.fail(w, r, err, "")
				return
			}
		}
	}
	var capped int64
	err = a.Store.WithTx(ctx, func(tx *store.Store) error {
		if err := tx.SetMembership(ctx, oc.Org.ID, u.ID, string(role)); err != nil {
			return err
		}
		if !demoted {
			return nil
		}
		// Al bajar de rol, los tokens que creó con un rol superior bajan al nuevo.
		n, err := tx.CapAPITokensForUser(ctx, oc.Org.ID, u.ID, string(role), auth.Above(role))
		capped = n
		return err
	})
	if err != nil {
		a.fail(w, r, err, "membresía")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "org.member_set", "org", oc.Org.ID, u.Email+" ahora es "+string(role), map[string]any{"user_id": u.ID, "email": u.Email, "role": string(role), "tokens_capped": capped})
	writeJSON(w, http.StatusOK, store.MemberWithUser{User: *u, Role: string(role)})
}

func (a *API) removeMember(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	userID := chi.URLParam(r, "user_id")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	target, err := a.Store.GetMembership(ctx, oc.Org.ID, userID)
	if err != nil {
		a.fail(w, r, err, "miembro")
		return
	}
	if !oc.Role.AtLeast(auth.Role(target.Role)) {
		a.fail(w, r, forbidden("no podés sacar a alguien con rol superior al tuyo"), "")
		return
	}
	if err := a.ensureNotLastOwner(ctx, oc.Org.ID, userID); err != nil {
		a.fail(w, r, err, "")
		return
	}
	// RemoveMembership borra también sus tokens de API en la org, en la misma transacción.
	if err := a.Store.RemoveMembership(ctx, oc.Org.ID, userID); err != nil {
		a.fail(w, r, err, "miembro")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "org.member_removed", "org", oc.Org.ID, "miembro removido; sus tokens de API en la organización fueron revocados", map[string]string{"user_id": userID})
	w.WriteHeader(http.StatusNoContent)
}

// ---- tokens ----

type tokenResponse struct {
	store.APIToken
	Token string `json:"token,omitempty"`
}

func (a *API) listTokens(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ts, err := a.Store.ListAPITokens(ctx, orgCtx(r).Org.ID)
	if err != nil {
		a.fail(w, r, err, "tokens")
		return
	}
	writeJSON(w, http.StatusOK, items(ts))
}

func (a *API) createToken(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	p := principal(r)
	var body struct {
		Name          string `json:"name"`
		Role          string `json:"role"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := decode(r, &body); err != nil {
		a.fail(w, r, err, "")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if err := requireString("name", body.Name); err != nil {
		a.fail(w, r, err, "")
		return
	}
	if body.Role == "" {
		body.Role = store.RoleDeveloper
	}
	role, err := auth.ParseRole(body.Role)
	if err != nil {
		a.fail(w, r, invalid(err.Error()), "")
		return
	}
	if role == auth.RoleOwner {
		a.fail(w, r, invalid("un token no puede ser owner"), "")
		return
	}
	if !oc.Role.AtLeast(role) {
		a.fail(w, r, forbidden("no podés crear un token con rol superior al tuyo"), "")
		return
	}
	if body.ExpiresInDays < 0 || body.ExpiresInDays > 3650 {
		a.fail(w, r, invalid("expires_in_days debe estar entre 0 (sin vencimiento) y 3650"), "")
		return
	}
	var expires *time.Time
	if body.ExpiresInDays > 0 {
		t := time.Now().UTC().Add(time.Duration(body.ExpiresInDays) * 24 * time.Hour)
		expires = &t
	}
	plain, hash, err := auth.NewAPIToken()
	if err != nil {
		a.fail(w, r, err, "token")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tok, err := a.Store.CreateAPIToken(ctx, oc.Org.ID, p.User.ID, body.Name, hash, string(role), expires)
	if err != nil {
		a.fail(w, r, err, "token")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, p.Actor(), "org.token_created", "org", oc.Org.ID, "token "+tok.Name+" creado", map[string]string{"token_id": tok.ID, "role": tok.Role})
	writeJSON(w, http.StatusCreated, tokenResponse{APIToken: *tok, Token: plain})
}

func (a *API) deleteToken(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	id := chi.URLParam(r, "id")
	if err := a.Store.DeleteAPIToken(ctx, oc.Org.ID, id); err != nil {
		a.fail(w, r, err, "token")
		return
	}
	a.Bus.Emit(ctx, oc.Org.ID, principal(r).Actor(), "org.token_deleted", "org", oc.Org.ID, "token borrado", map[string]string{"token_id": id})
	w.WriteHeader(http.StatusNoContent)
}
