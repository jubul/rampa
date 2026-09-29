package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const eventColumns = "id, org_id, actor_type, actor_id, actor_name, kind, subject_type, subject_id, message, payload, created_at"

func scanEvent(row interface{ Scan(dest ...any) error }) (*Event, error) {
	var e Event
	var payload []byte
	if err := row.Scan(&e.ID, &e.OrgID, &e.Actor.Type, &e.Actor.ID, &e.Actor.Name, &e.Kind, &e.SubjectType, &e.SubjectID, &e.Message, &payload, &e.CreatedAt); err != nil {
		return nil, translate(err)
	}
	e.Payload = json.RawMessage(payload)
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("{}")
	}
	return &e, nil
}

// InsertEvent persiste un evento. Completa id y created_at si faltan.
func (s *Store) InsertEvent(ctx context.Context, e *Event) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	payload := e.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
		e.Payload = payload
	}
	if e.Actor.Type == "" {
		e.Actor.Type = "system"
	}
	_, err := s.db.Exec(ctx, `INSERT INTO events (id, org_id, actor_type, actor_id, actor_name, kind, subject_type, subject_id, message, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		e.ID, e.OrgID, e.Actor.Type, e.Actor.ID, e.Actor.Name, e.Kind, e.SubjectType, e.SubjectID, e.Message, payload, e.CreatedAt)
	return translate(err)
}

// EventFilter son los filtros del listado y del stream.
type EventFilter struct {
	SubjectType string
	SubjectID   string
	Limit       int
	Before      *time.Time
}

// Matches dice si un evento pasa el filtro (sin considerar limit ni before).
func (f EventFilter) Matches(e *Event) bool {
	if f.SubjectType != "" && e.SubjectType != f.SubjectType {
		return false
	}
	if f.SubjectID != "" && e.SubjectID != f.SubjectID {
		return false
	}
	return true
}

// ListEvents lista eventos de la org del más nuevo al más viejo.
func (s *Store) ListEvents(ctx context.Context, orgID string, f EventFilter) ([]Event, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `SELECT `+eventColumns+` FROM events
		WHERE org_id = $1 AND ($2 = '' OR subject_type = $2) AND ($3 = '' OR subject_id = $3) AND ($4::timestamptz IS NULL OR created_at < $4)
		ORDER BY created_at DESC LIMIT $5`, orgID, f.SubjectType, f.SubjectID, nullTime(f.Before), limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}
