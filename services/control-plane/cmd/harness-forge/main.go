package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"harness-forge.local/control-plane/internal/config"
	"harness-forge.local/control-plane/internal/conversations"
	"harness-forge.local/control-plane/internal/httpapi"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/projects"
	"harness-forge.local/control-plane/internal/runs"
	"harness-forge.local/control-plane/internal/sandbox"
)

func main() {
	applicationConfig, err := config.ConfigFromEnv(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	pool, err := postgres.Open(context.Background(), applicationConfig.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.Migrate(context.Background(), pool, "public"); err != nil {
		log.Fatal(err)
	}
	profileResolver, err := profiles.NewResolver(applicationConfig.ProfileRoot)
	if err != nil {
		log.Fatal(err)
	}
	objects, err := objectstore.NewMinIO(context.Background(), applicationConfig.MinIOEndpoint, applicationConfig.MinIOAccessKey, applicationConfig.MinIOSecretKey, applicationConfig.MinIOBucket)
	if err != nil {
		log.Fatal(err)
	}
	projectService := projects.NewService(projects.NewStore(pool), profileResolver, objects)
	binding, err := sandbox.NewProvider(applicationConfig)
	if err != nil {
		log.Fatal(err)
	}
	broker := runs.NewBroker()
	runStore := runs.NewStore(pool, broker)
	conversationService := conversations.NewService(conversations.NewStore(pool), runStore)
	// Task10 supplies the Coordinator-owned active cancellation callback and consumes this binding.
	// Until then queued cancellation works; active cancellation explicitly returns 503.
	canceller := runs.NewCanceller(runStore, nil)
	log.Printf("sandbox provider configured: %s", binding.ID)

	log.Printf("control plane listening on %s", applicationConfig.HTTPAddr)
	err = http.ListenAndServe(applicationConfig.HTTPAddr, httpapi.NewRouter(httpapi.Dependencies{Projects: projectService, Conversations: conversationService, Runs: runStore, Canceller: canceller, Broker: broker}))
	if err != nil {
		log.Fatal(err)
	}
}
