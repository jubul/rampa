package events

import (
	"context"
	"testing"
	"time"

	"rampa.dev/internal/api/store"
)

type memPersister struct{ events []*store.Event }

func (m *memPersister) InsertEvent(_ context.Context, e *store.Event) error {
	m.events = append(m.events, e)
	return nil
}

func TestPublishFanoutAndFilter(t *testing.T) {
	p := &memPersister{}
	b := New(p, nil)
	all := b.Subscribe("org-a", store.EventFilter{})
	onlyDeploy := b.Subscribe("org-a", store.EventFilter{SubjectType: "deployment", SubjectID: "d1"})
	otherOrg := b.Subscribe("org-b", store.EventFilter{})
	defer b.Unsubscribe(all)
	defer b.Unsubscribe(onlyDeploy)
	defer b.Unsubscribe(otherOrg)

	b.Emit(t.Context(), "org-a", store.Actor{Type: "user", ID: "u1"}, "deployment.queued", "deployment", "d1", "encolado", map[string]string{"image": "x"})
	b.Emit(t.Context(), "org-a", store.Actor{Type: "system"}, "cluster.online", "cluster", "c1", "", nil)

	if len(p.events) != 2 {
		t.Fatalf("esperaba 2 eventos persistidos, hay %d", len(p.events))
	}
	if p.events[0].ID == "" || p.events[0].CreatedAt.IsZero() || string(p.events[1].Payload) != "{}" {
		t.Errorf("defaults no aplicados: %+v", p.events)
	}

	recv := func(s *Subscription) *store.Event {
		select {
		case e := <-s.C:
			return e
		case <-time.After(time.Second):
			return nil
		}
	}
	if e := recv(all); e == nil || e.Kind != "deployment.queued" {
		t.Errorf("all: primer evento inesperado: %+v", e)
	}
	if e := recv(all); e == nil || e.Kind != "cluster.online" {
		t.Errorf("all: segundo evento inesperado: %+v", e)
	}
	if e := recv(onlyDeploy); e == nil || e.SubjectID != "d1" {
		t.Errorf("filtro: esperaba el deployment, tengo %+v", e)
	}
	select {
	case e := <-onlyDeploy.C:
		t.Errorf("filtro: no debería recibir %+v", e)
	default:
	}
	select {
	case e := <-otherOrg.C:
		t.Errorf("otra org no debería recibir %+v", e)
	default:
	}
}

func TestUnsubscribe(t *testing.T) {
	b := New(nil, nil)
	s := b.Subscribe("org", store.EventFilter{})
	if b.Subscribers("org") != 1 {
		t.Fatal("debería haber un suscriptor")
	}
	b.Unsubscribe(s)
	b.Unsubscribe(s)
	if b.Subscribers("org") != 0 {
		t.Fatal("debería haber cero suscriptores")
	}
	// Publicar sin suscriptores ni persister no debe fallar.
	if err := b.Publish(t.Context(), &store.Event{OrgID: "org", Kind: "x", SubjectType: "org"}); err != nil {
		t.Fatal(err)
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	b := New(nil, nil)
	s := b.Subscribe("org", store.EventFilter{})
	defer b.Unsubscribe(s)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			b.Emit(context.Background(), "org", store.Actor{}, "k", "org", "", "", nil)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish bloqueó con un suscriptor lento")
	}
}

func TestBroadcastDoesNotPersist(t *testing.T) {
	p := &memPersister{}
	b := New(p, nil)
	sub := b.Subscribe("org-a", store.EventFilter{SubjectType: "infra_run", SubjectID: "r1"})
	defer b.Unsubscribe(sub)
	b.Broadcast("org-a", store.Actor{}, "infra_run.line", "infra_run", "r1", "", map[string]any{"run_id": "r1", "line": "hola"})
	select {
	case e := <-sub.C:
		if e.Kind != "infra_run.line" || e.ID == "" || e.Actor.Type != "system" || e.CreatedAt.IsZero() {
			t.Errorf("evento: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("el suscriptor no recibió el broadcast")
	}
	if len(p.events) != 0 {
		t.Errorf("Broadcast no debe persistir, hay %d eventos", len(p.events))
	}
}
