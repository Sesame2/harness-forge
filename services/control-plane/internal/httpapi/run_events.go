package httpapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"harness-forge.local/control-plane/internal/projects"
	"harness-forge.local/control-plane/internal/runs"
)

func eventCursor(w http.ResponseWriter, r *http.Request, stream bool) (int64, bool) {
	raw := r.URL.Query().Get("after_sequence")
	if stream && r.Header.Get("Last-Event-ID") != "" {
		raw = r.Header.Get("Last-Event-ID")
	}
	if raw == "" {
		return 0, true
	}
	after, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		writeError(w, projects.ErrInvalid)
		return 0, false
	}
	// OpenAPI accepts uint64 cursors; PostgreSQL durable sequences fit signed bigint.
	return int64(min(after, math.MaxInt64)), true
}
func (h runHandlers) events(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	after, ok := eventCursor(w, r, false)
	if !ok {
		return
	}
	if _, err := h.store.Read(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	events, err := h.store.ListEvents(r.Context(), id, after)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, events)
}
func (h runHandlers) stream(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	after, ok := eventCursor(w, r, true)
	if !ok {
		return
	}
	if _, err := h.store.Read(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	if h.broker == nil {
		writeError(w, runs.ErrUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, runs.ErrUnavailable)
		return
	}
	// Subscribe before the first durable read. Notifications are hints; the
	// periodic read also covers missing hints and commits completing after one.
	notifications, unsubscribe := h.broker.Subscribe(r.Context(), id)
	defer unsubscribe()
	events, err := h.store.ListEvents(r.Context(), id, after)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	poll := time.NewTicker(5 * time.Second)
	defer poll.Stop()
	for {
		for _, event := range events {
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			name := event.Type
			if strings.ContainsAny(name, "\r\n") {
				name = "message"
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, name, data); err != nil {
				return
			}
			after = event.Sequence
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-notifications:
		case <-poll.C:
		}
		events, err = h.store.ListEvents(r.Context(), id, after)
		if err != nil {
			return
		}
	}
}
