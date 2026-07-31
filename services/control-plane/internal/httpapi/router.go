package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func NewRouter(services ...projectService) http.Handler {
	router := chi.NewRouter()
	router.Get("/health", health)
	if len(services) > 0 && services[0] != nil {
		handlers := projectHandlers{service: services[0]}
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
	return router
}
