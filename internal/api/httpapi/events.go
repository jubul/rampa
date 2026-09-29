package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"rampa.dev/internal/api/store"
)

// SSEPingInterval es cada cuánto el stream SSE manda ": ping" y vuelve a
// comprobar que el principal siga con acceso a la org. Es variable para que
// los tests de integración lo acorten.
var SSEPingInterval = 25 * time.Second

func eventFilterFromQuery(r *http.Request) (store.EventFilter, error) {
	q := r.URL.Query()
	f := store.EventFilter{SubjectType: q.Get("subject_type"), SubjectID: q.Get("subject_id")}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 500 {
			return f, invalid("limit debe ser un entero entre 1 y 500")
		}
		f.Limit = n
	}
	if b := q.Get("before"); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			return f, invalid("before debe ser una fecha RFC 3339")
		}
		f.Before = &t
	}
	return f, nil
}

func (a *API) listEvents(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	f, err := eventFilterFromQuery(r)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	evs, err := a.Store.ListEvents(ctx, oc.Org.ID, f)
	if err != nil {
		a.fail(w, r, err, "eventos")
		return
	}
	writeJSON(w, http.StatusOK, items(evs))
}

// streamEvents es el stream SSE de la org. Cada evento va como
// "event: <kind>" más "data: <json>"; cada 25 segundos ": ping".
func (a *API) streamEvents(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	f, err := eventFilterFromQuery(r)
	if err != nil {
		a.fail(w, r, err, "")
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		a.Log.Warn("SSE sin flush disponible", "err", err)
		return
	}

	sub := a.Bus.Subscribe(oc.Org.ID, f)
	defer a.Bus.Unsubscribe(sub)

	ticker := time.NewTicker(SSEPingInterval)
	defer ticker.Stop()

	p := principal(r)
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// La autorización se resolvió al abrir el stream; una conexión que
			// vive horas tiene que enterarse si sacaron al miembro o borraron
			// el token. Si perdió acceso, se corta.
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			ok := a.Auth.StillAuthorized(checkCtx, p, oc.Org.ID)
			cancel()
			if !ok {
				a.Log.Info("stream SSE cerrado: el principal perdió acceso a la org", "org", oc.Org.Slug, "actor", p.ActorID())
				return
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case e := <-sub.C:
			data, err := json.Marshal(e)
			if err != nil {
				a.Log.Warn("evento no serializable", "kind", e.Kind, "err", err)
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\nid: %s\ndata: %s\n\n", e.Kind, e.ID, data); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
