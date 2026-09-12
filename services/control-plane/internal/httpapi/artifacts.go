package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/projects"
)

type artifactReader interface {
	ListByRun(context.Context, uuid.UUID) ([]artifacts.Artifact, error)
}

type artifactHandlers struct {
	store        artifactReader
	publicOrigin string
}

func (h artifactHandlers) list(w http.ResponseWriter, r *http.Request) {
	id, ok := runID(w, r)
	if !ok {
		return
	}
	records, err := h.store.ListByRun(r.Context(), id)
	if errors.Is(err, artifacts.ErrNotFound) {
		writeError(w, projects.ErrNotFound)
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	type response struct {
		artifacts.Artifact
		GatewayURL string `json:"gateway_url"`
	}
	result := make([]response, 0, len(records))
	for _, record := range records {
		entry := (&url.URL{Path: "/artifacts/" + record.ID.String() + "/" + record.EntryPath}).EscapedPath()
		result = append(result, response{Artifact: record, GatewayURL: h.publicOrigin + entry})
	}
	writeJSON(w, http.StatusOK, result)
}
