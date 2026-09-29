package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDoSendsBearerAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/me" {
			t.Errorf("path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer rampa_x" {
			t.Errorf("authorization %q", got)
		}
		_ = json.NewEncoder(w).Encode(Me{User: User{Email: "juan@acme.com"}, Memberships: []Membership{{Org: Org{Slug: "acme"}, Role: "owner"}}})
	}))
	defer srv.Close()
	me, err := New(srv.URL+"/", "rampa_x").Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if me.User.Email != "juan@acme.com" || me.Memberships[0].Org.Slug != "acme" {
		t.Errorf("me: %+v", me)
	}
}

func TestErrorBodyParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"error":{"code":"invalid","message":"rampa.yaml inválido","details":["spec.type: no puede estar vacío"]}}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "t").CreateApp(context.Background(), "acme", CreateAppRequest{Manifest: "x"})
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("esperaba *Error, tengo %T: %v", err, err)
	}
	if e.Code != "invalid" || e.Status != 422 || len(e.Details) != 1 {
		t.Errorf("error: %+v", e)
	}
	if IsCode(err, "conflict") || !IsCode(err, "invalid") {
		t.Error("IsCode")
	}
	want := "invalid: rampa.yaml inválido\n  - spec.type: no puede estar vacío"
	if err.Error() != want {
		t.Errorf("mensaje %q", err.Error())
	}
}

func TestErrorWithoutJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusConflict)
	}))
	defer srv.Close()
	err := New(srv.URL, "t").DeleteScope(context.Background(), "acme", "app", "pr-1")
	if !IsConflict(err) {
		t.Errorf("esperaba conflict, tengo %v", err)
	}
}

func TestPathsEscaped(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "t")
	ctx := context.Background()
	_, _ = c.ListScopes(ctx, "acme", "checkout-api")
	_, _ = c.BindScope(ctx, "acme", "checkout-api", "prod", "c1")
	_ = c.DeleteScope(ctx, "acme", "checkout-api", "pr-42")
	_, _ = c.CreateDeployment(ctx, "acme", "checkout-api", "dev", CreateDeploymentRequest{Image: "sha-1"})
	_, _ = c.GetDeployment(ctx, "acme", "d1")
	_, _ = c.PutManifest(ctx, "acme", "checkout-api", "m")
	want := []string{
		"GET /v1/orgs/acme/apps/checkout-api/scopes",
		"PUT /v1/orgs/acme/apps/checkout-api/scopes/prod",
		"DELETE /v1/orgs/acme/apps/checkout-api/scopes/pr-42",
		"POST /v1/orgs/acme/apps/checkout-api/scopes/dev/deployments",
		"GET /v1/orgs/acme/deployments/d1",
		"PUT /v1/orgs/acme/apps/checkout-api/manifest",
	}
	if len(seen) != len(want) {
		t.Fatalf("requests: %v", seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("request %d: %s, esperaba %s", i, seen[i], want[i])
		}
	}
}

func sseHandler(t *testing.T, events []Event, keepOpen bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("accept %q", r.Header.Get("Accept"))
		}
		if r.URL.Query().Get("subject_type") != "deployment" || r.URL.Query().Get("subject_id") != "d1" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		fmt.Fprint(w, ": ping\n\n")
		for _, ev := range events {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, b)
			if fl != nil {
				fl.Flush()
			}
		}
		if keepOpen {
			<-r.Context().Done()
		}
	}
}

func TestStreamEventsStopsOnCallback(t *testing.T) {
	events := []Event{
		{ID: "1", Kind: "deployment.progressing", SubjectType: "deployment", SubjectID: "d1", Message: "1/3 listos"},
		{ID: "2", Kind: "deployment.healthy", SubjectType: "deployment", SubjectID: "d1"},
		{ID: "3", Kind: "deployment.healthy", SubjectType: "deployment", SubjectID: "d1", Message: "no debería llegar"},
	}
	srv := httptest.NewServer(sseHandler(t, events, true))
	defer srv.Close()
	var got []string
	err := New(srv.URL, "t").StreamEvents(context.Background(), "acme", "deployment", "d1", func(ev Event) error {
		got = append(got, ev.Kind)
		if ev.Kind == EventDeploymentHealthy {
			return ErrStopStream
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "deployment.progressing" || got[1] != "deployment.healthy" {
		t.Errorf("eventos: %v", got)
	}
}

func TestStreamEventsEOFWhenServerCloses(t *testing.T) {
	srv := httptest.NewServer(sseHandler(t, []Event{{Kind: "deployment.queued", SubjectType: "deployment", SubjectID: "d1"}}, false))
	defer srv.Close()
	n := 0
	err := New(srv.URL, "t").StreamEvents(context.Background(), "acme", "deployment", "d1", func(Event) error { n++; return nil })
	if !errors.Is(err, io.EOF) || n != 1 {
		t.Errorf("err=%v n=%d", err, n)
	}
}

func TestStreamEventsContextTimeout(t *testing.T) {
	srv := httptest.NewServer(sseHandler(t, nil, true))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := New(srv.URL, "t").StreamEvents(ctx, "acme", "deployment", "d1", func(Event) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("esperaba deadline exceeded, tengo %v", err)
	}
}

func TestStreamEventsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"code":"unauthorized","message":"token inválido"}}`)
	}))
	defer srv.Close()
	err := New(srv.URL, "t").StreamEvents(context.Background(), "acme", "deployment", "d1", func(Event) error { return nil })
	if !IsCode(err, "unauthorized") {
		t.Errorf("esperaba unauthorized, tengo %v", err)
	}
}
