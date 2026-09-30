package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const orgColumns = "id, slug, name, plan, settings, created_at, updated_at"

func scanOrg(row interface{ Scan(dest ...any) error }) (*Organization, error) {
	var o Organization
	var settings []byte
	if err := row.Scan(&o.ID, &o.Slug, &o.Name, &o.Plan, &settings, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	o.Settings = OrgSettings{RequireApprovalProd: true}
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &o.Settings); err != nil {
			return nil, fmt.Errorf("settings de la org %s corruptos: %w", o.Slug, err)
		}
	}
	return &o, nil
}

// CreateOrg crea la organización y deja al creador como owner.
func (s *Store) CreateOrg(ctx context.Context, slug, name, ownerUserID string) (*Organization, error) {
	var org *Organization
	err := s.WithTx(ctx, func(tx *Store) error {
		settings, _ := json.Marshal(OrgSettings{RequireApprovalProd: true})
		row := tx.db.QueryRow(ctx, `INSERT INTO organizations (id, slug, name, settings) VALUES ($1, $2, $3, $4)
			RETURNING `+orgColumns, uuid.NewString(), slug, name, settings)
		o, err := scanOrg(row)
		if err != nil {
			return err
		}
		if _, err := tx.db.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3)`, o.ID, ownerUserID, RoleOwner); err != nil {
			return translate(err)
		}
		org = o
		return nil
	})
	return org, err
}

// GetOrgBySlug busca una organización por slug.
func (s *Store) GetOrgBySlug(ctx context.Context, slug string) (*Organization, error) {
	return scanOrg(s.db.QueryRow(ctx, `SELECT `+orgColumns+` FROM organizations WHERE slug = $1`, slug))
}

// GetOrg busca una organización por id.
func (s *Store) GetOrg(ctx context.Context, orgID string) (*Organization, error) {
	return scanOrg(s.db.QueryRow(ctx, `SELECT `+orgColumns+` FROM organizations WHERE id = $1`, orgID))
}

// UpdateOrg cambia nombre y settings.
func (s *Store) UpdateOrg(ctx context.Context, orgID, name string, settings OrgSettings) (*Organization, error) {
	b, _ := json.Marshal(settings)
	return scanOrg(s.db.QueryRow(ctx, `UPDATE organizations SET name = $2, settings = $3, updated_at = now()
		WHERE id = $1 RETURNING `+orgColumns, orgID, name, b))
}

// DeleteOrg borra la organización y, en cascada, todo lo suyo.
func (s *Store) DeleteOrg(ctx context.Context, orgID string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, orgID)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListOrgsForUser devuelve las organizaciones donde el usuario es miembro, con su rol.
func (s *Store) ListOrgsForUser(ctx context.Context, userID string) ([]MembershipWithOrg, error) {
	rows, err := s.db.Query(ctx, `SELECT o.id, o.slug, o.name, o.plan, o.settings, o.created_at, o.updated_at, m.role
		FROM memberships m JOIN organizations o ON o.id = m.org_id
		WHERE m.user_id = $1 ORDER BY o.slug`, userID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []MembershipWithOrg{}
	for rows.Next() {
		var o Organization
		var settings []byte
		var role string
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name, &o.Plan, &settings, &o.CreatedAt, &o.UpdatedAt, &role); err != nil {
			return nil, err
		}
		o.Settings = OrgSettings{RequireApprovalProd: true}
		if len(settings) > 0 {
			_ = json.Unmarshal(settings, &o.Settings)
		}
		out = append(out, MembershipWithOrg{Org: o, Role: role})
	}
	return out, rows.Err()
}

// ---- users ----

const userColumns = "id, email, name, locale, created_at, updated_at"

func scanUser(row interface{ Scan(dest ...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Locale, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

// NormalizeEmail deja el email en minúsculas y sin espacios.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// UpsertUser crea el usuario si no existe (por email) y devuelve la fila.
// Si name viene vacío y el usuario existe, conserva el nombre anterior.
func (s *Store) UpsertUser(ctx context.Context, email, name string) (*User, error) {
	email = NormalizeEmail(email)
	if name == "" {
		if at := strings.Index(email, "@"); at > 0 {
			name = email[:at]
		} else {
			name = email
		}
	}
	return scanUser(s.db.QueryRow(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)
		ON CONFLICT (email) DO UPDATE SET name = CASE WHEN EXCLUDED.name = '' THEN users.name ELSE EXCLUDED.name END, updated_at = now()
		RETURNING `+userColumns, uuid.NewString(), email, name))
}

// GetUser busca un usuario por id.
func (s *Store) GetUser(ctx context.Context, userID string) (*User, error) {
	return scanUser(s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, userID))
}

// SetUserLocale guarda el idioma preferido ("es", "en" o "" para ninguno).
func (s *Store) SetUserLocale(ctx context.Context, userID, locale string) (*User, error) {
	return scanUser(s.db.QueryRow(ctx, `UPDATE users SET locale = $2, updated_at = now() WHERE id = $1 RETURNING `+userColumns, userID, locale))
}

// GetUsersByID devuelve un mapa id -> usuario. Los ids que no existen se omiten.
func (s *Store) GetUsersByID(ctx context.Context, ids []string) (map[string]*User, error) {
	out := map[string]*User{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+userColumns+` FROM users WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u
	}
	return out, rows.Err()
}

// ---- memberships ----

// GetMembership devuelve el rol de un usuario en una organización.
func (s *Store) GetMembership(ctx context.Context, orgID, userID string) (*Membership, error) {
	var m Membership
	err := s.db.QueryRow(ctx, `SELECT org_id, user_id, role, created_at FROM memberships WHERE org_id = $1 AND user_id = $2`, orgID, userID).
		Scan(&m.OrgID, &m.UserID, &m.Role, &m.CreatedAt)
	if err != nil {
		return nil, translate(err)
	}
	return &m, nil
}

// SetMembership agrega un miembro o le cambia el rol.
func (s *Store) SetMembership(ctx context.Context, orgID, userID, role string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role, updated_at = now()`, orgID, userID, role)
	return translate(err)
}

// RemoveMembership saca a un miembro de la organización y, en la misma
// transacción, borra los tokens de API que ese usuario creó en la org: un ex
// miembro no conserva ningún acceso.
func (s *Store) RemoveMembership(ctx context.Context, orgID, userID string) error {
	return s.WithTx(ctx, func(tx *Store) error {
		tag, err := tx.db.Exec(ctx, `DELETE FROM memberships WHERE org_id = $1 AND user_id = $2`, orgID, userID)
		if err != nil {
			return translate(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return tx.DeleteAPITokensForUser(ctx, orgID, userID)
	})
}

// CountOwners cuenta los owners de la org, para no dejarla sin ninguno.
func (s *Store) CountOwners(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE org_id = $1 AND role = $2`, orgID, RoleOwner).Scan(&n)
	return n, translate(err)
}

// ListMembers devuelve los miembros con su usuario.
func (s *Store) ListMembers(ctx context.Context, orgID string) ([]MemberWithUser, error) {
	rows, err := s.db.Query(ctx, `SELECT u.id, u.email, u.name, u.locale, u.created_at, u.updated_at, m.role
		FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.org_id = $1 ORDER BY u.email`, orgID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []MemberWithUser{}
	for rows.Next() {
		var u User
		var role string
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Locale, &u.CreatedAt, &u.UpdatedAt, &role); err != nil {
			return nil, err
		}
		out = append(out, MemberWithUser{User: u, Role: role})
	}
	return out, rows.Err()
}

// ---- api tokens ----

const tokenColumns = "id, org_id, user_id, name, hash, role, expires_at, last_used_at, created_at, updated_at"

func scanToken(row interface{ Scan(dest ...any) error }) (*APIToken, error) {
	var t APIToken
	if err := row.Scan(&t.ID, &t.OrgID, &t.UserID, &t.Name, &t.Hash, &t.Role, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, translate(err)
	}
	return &t, nil
}

// CreateAPIToken guarda un token ya hasheado.
func (s *Store) CreateAPIToken(ctx context.Context, orgID, userID, name, hash, role string, expiresAt *time.Time) (*APIToken, error) {
	return scanToken(s.db.QueryRow(ctx, `INSERT INTO api_tokens (id, org_id, user_id, name, hash, role, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+tokenColumns,
		uuid.NewString(), orgID, userID, name, hash, role, nullTime(expiresAt)))
}

// GetAPITokenByHash busca un token por su hash. Es global: el hash identifica
// la org. Es la excepción documentada a "todo filtra por org_id".
func (s *Store) GetAPITokenByHash(ctx context.Context, hash string) (*APIToken, error) {
	return scanToken(s.db.QueryRow(ctx, `SELECT `+tokenColumns+` FROM api_tokens WHERE hash = $1`, hash))
}

// TouchAPIToken registra el último uso. Es best effort.
func (s *Store) TouchAPIToken(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE api_tokens SET last_used_at = now() WHERE id = $1`, id)
	return translate(err)
}

// ListAPITokens lista los tokens de una organización, sin secretos.
func (s *Store) ListAPITokens(ctx context.Context, orgID string) ([]APIToken, error) {
	rows, err := s.db.Query(ctx, `SELECT `+tokenColumns+` FROM api_tokens WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// DeleteAPITokensForUser borra todos los tokens que un usuario creó en la org.
func (s *Store) DeleteAPITokensForUser(ctx context.Context, orgID, userID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM api_tokens WHERE org_id = $1 AND user_id = $2`, orgID, userID)
	return translate(err)
}

// CapAPITokensForUser baja a maxRole los tokens del usuario en la org cuyo rol
// esté en aboveRoles (los roles superiores al nuevo). Devuelve cuántos tocó.
func (s *Store) CapAPITokensForUser(ctx context.Context, orgID, userID, maxRole string, aboveRoles []string) (int64, error) {
	if len(aboveRoles) == 0 {
		return 0, nil
	}
	tag, err := s.db.Exec(ctx, `UPDATE api_tokens SET role = $3, updated_at = now()
		WHERE org_id = $1 AND user_id = $2 AND role = ANY($4::text[])`, orgID, userID, maxRole, aboveRoles)
	if err != nil {
		return 0, translate(err)
	}
	return tag.RowsAffected(), nil
}

// DeleteAPIToken borra un token de la organización.
func (s *Store) DeleteAPIToken(ctx context.Context, orgID, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM api_tokens WHERE org_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
