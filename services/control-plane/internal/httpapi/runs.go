package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/projects"
	"harness-forge.local/control-plane/internal/runs"
)

type runReader interface {
	Read(context.Context, uuid.UUID) (runs.Run, error)
	ListByConversation(context.Context, uuid.UUID) ([]runs.Run, error)
	ListEvents(context.Context, uuid.UUID, int64) ([]runs.Event, error)
}
type runCanceller interface {
	Cancel(context.Context, uuid.UUID) (runs.Run, error)
}
type runHandlers struct {
	store     runReader
	canceller runCanceller
	broker    *runs.Broker
}

func (h runHandlers) list(w http.ResponseWriter, r *http.Request) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	result, err := h.store.ListByConversation(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (h runHandlers) read(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	result, err := h.store.Read(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (h runHandlers) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	if h.canceller == nil {
		writeError(w, runs.ErrUnavailable)
		return
	}
	result, err := h.canceller.Cancel(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}
func runID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "run_id"))
	if err != nil {
		writeError(w, projects.ErrInvalid)
		return uuid.Nil, false
	}
	return id, true
}
