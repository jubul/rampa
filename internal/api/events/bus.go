// Package events persiste eventos de auditoría y los reparte en memoria a
// los suscriptores del stream SSE de cada organización.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"rampa.dev/internal/api/store"
)

// Persister guarda eventos. Lo implementa *store.Store. Puede ser nil en tests.
type Persister interface {
	InsertEvent(ctx context.Context, e *store.Event) error
}

// Subscription es un suscriptor del stream de una organización.
type Subscription struct {
	C      <-chan *store.Event
	ch     chan *store.Event
	filter store.EventFilter
	orgID  string
	id     string
}

// Bus es el fanout en memoria. Es por proceso: con varias réplicas de la API
// cada una ve solo los eventos que ella emite (ver README).
type Bus struct {
	persister Persister
	log       *slog.Logger

	mu   sync.RWMutex
	subs map[string]map[string]*Subscription // orgID -> subID -> sub
}

// New crea el bus. persister puede ser nil (solo fanout).
func New(p Persister, log *slog.Logger) *Bus {
	if log == nil {
		log = slog.Default()
	}
	return &Bus{persister: p, log: log, subs: map[string]map[string]*Subscription{}}
}

// Publish persiste y reparte. Nunca bloquea: los suscriptores lentos pierden
// eventos (el buffer es de 64) y lo notamos en el log.
func (b *Bus) Publish(ctx context.Context, e *store.Event) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("{}")
	}
	if e.Actor.Type == "" {
		e.Actor.Type = "system"
	}
	if b.persister != nil {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := b.persister.InsertEvent(pctx, e); err != nil {
			return err
		}
	}
	b.fanout(e)
	return nil
}

// Emit es Publish con los campos sueltos. Loguea si falla y sigue: un evento
// perdido no debe romper la operación que lo generó.
func (b *Bus) Emit(ctx context.Context, orgID string, actor store.Actor, kind, subjectType, subjectID, message string, payload any) {
	var raw json.RawMessage
	if payload != nil {
		bts, err := json.Marshal(payload)
		if err != nil {
			b.log.Warn("payload de evento no serializable", "kind", kind, "err", err)
		} else {
			raw = bts
		}
	}
	e := &store.Event{OrgID: orgID, Actor: actor, Kind: kind, SubjectType: subjectType, SubjectID: subjectID, Message: message, Payload: raw}
	if err := b.Publish(ctx, e); err != nil {
		b.log.Error("no pude persistir el evento", "kind", kind, "org_id", orgID, "err", err)
	}
}

func (b *Bus) fanout(e *store.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subs[e.OrgID] {
		if !sub.filter.Matches(e) {
			continue
		}
		select {
		case sub.ch <- e:
		default:
			b.log.Warn("suscriptor lento, evento descartado", "org_id", e.OrgID, "kind", e.Kind)
		}
	}
}

// Subscribe registra un suscriptor de la org con filtro. Hay que llamar a Unsubscribe.
func (b *Bus) Subscribe(orgID string, f store.EventFilter) *Subscription {
	ch := make(chan *store.Event, 64)
	sub := &Subscription{C: ch, ch: ch, filter: f, orgID: orgID, id: uuid.NewString()}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs[orgID] == nil {
		b.subs[orgID] = map[string]*Subscription{}
	}
	b.subs[orgID][sub.id] = sub
	return sub
}

// Unsubscribe saca al suscriptor. Es idempotente.
func (b *Bus) Unsubscribe(sub *Subscription) {
	if sub == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if m := b.subs[sub.orgID]; m != nil {
		delete(m, sub.id)
		if len(m) == 0 {
			delete(b.subs, sub.orgID)
		}
	}
}

// Subscribers cuenta los suscriptores de una org (para tests y métricas).
func (b *Bus) Subscribers(orgID string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[orgID])
}
