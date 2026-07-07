package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/middleware"
	"github.com/xorhub/waas/api-server/internal/service"
	"github.com/xorhub/waas/shared/auth"
)

// EventsHandler streams change notifications over SSE. Messages carry
// only a kind ("workspaces" / "remote-workspaces"): the client re-fetches
// through the normal authorized API, so the stream leaks nothing and the
// polling fallback stays untouched.
type EventsHandler struct {
	hub *service.EventHub
}

func NewEventsHandler(hub *service.EventHub) *EventsHandler {
	return &EventsHandler{hub: hub}
}

// heartbeat keeps intermediaries from timing the idle stream out.
const heartbeat = 25 * time.Second

// Stream handles GET /api/v1/events.
func (h *EventsHandler) Stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, r, apierror.BadRequest("streaming is not supported by this connection"))
		return
	}
	actor := middleware.Actor(r)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Disable proxy buffering (nginx honors this; traefik streams by default).
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, cancel := h.hub.Subscribe(actor.ID, actor.Role == string(auth.RoleAdmin))
	defer cancel()
	ping := time.NewTicker(heartbeat)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case kind, open := <-events:
			if !open {
				return
			}
			// SSE write errors are unrecoverable here; a dead client
			// is detected via r.Context() on the next iteration.
			_, _ = fmt.Fprintf(w, "data: %s\n\n", kind)
			flusher.Flush()
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
