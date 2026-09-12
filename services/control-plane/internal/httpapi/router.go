package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"harness-forge.local/control-plane/internal/runs"
)

type Dependencies struct {
	Artifacts            artifactReader
	ArtifactPublicOrigin string
	Projects             projectService
	Conversations        conversationService
	Runs                 runReader
	Canceller            runCanceller
	Broker               *runs.Broker
}

func NewRouter(dependencies ...Dependencies) http.Handler {
	router := chi.NewRouter()
	router.Get("/health", health)
	var services Dependencies
	if len(dependencies) > 0 {
		services = dependencies[0]
	}
	if services.Projects != nil {
		handlers := projectHandlers{service: services.Projects}
		router.Route("/api/v1/projects", func(router chi.Router) {
			router.Get("/", handlers.list)
			router.Post("/", handlers.create)
			router.Route("/{project_id}", func(router chi.Router) {
				router.Get("/", handlers.read)
				router.Patch("/", handlers.rename)
				router.Delete("/", handlers.delete)
				router.Get("/inputs", handlers.listInputs)
				router.Post("/inputs", handlers.uploadInput)
			})
		})
	}
	if services.Conversations != nil {
		handlers := conversationHandlers{service: services.Conversations}
		router.Get("/api/v1/projects/{project_id}/conversations", handlers.list)
		router.Post("/api/v1/projects/{project_id}/conversations", handlers.create)
		router.Route("/api/v1/conversations/{conversation_id}", func(router chi.Router) {
			router.Get("/", handlers.read)
			router.Patch("/", handlers.rename)
			router.Delete("/", handlers.delete)
			router.Get("/messages", handlers.listMessages)
			router.Post("/messages", handlers.submitMessage)
		})
	}
	if services.Runs != nil {
		handlers := runHandlers{store: services.Runs, canceller: services.Canceller, broker: services.Broker}
		router.Get("/api/v1/conversations/{conversation_id}/runs", handlers.list)
		router.Get("/api/v1/runs/{run_id}", handlers.read)
		router.Get("/api/v1/runs/{run_id}/events", handlers.events)
		router.Get("/api/v1/runs/{run_id}/events/stream", handlers.stream)
		router.Post("/api/v1/runs/{run_id}/cancel", handlers.cancel)
	}
	if services.Artifacts != nil {
		handlers := artifactHandlers{store: services.Artifacts, publicOrigin: services.ArtifactPublicOrigin}
		router.Get("/api/v1/runs/{run_id}/artifacts", handlers.list)
	}
	return router
}
