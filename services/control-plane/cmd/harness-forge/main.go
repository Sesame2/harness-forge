package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"harness-forge.local/control-plane/internal/config"
	"harness-forge.local/control-plane/internal/httpapi"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/projects"
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

	log.Printf("control plane listening on %s", applicationConfig.HTTPAddr)
	err = http.ListenAndServe(applicationConfig.HTTPAddr, httpapi.NewRouter(projectService))
	if err != nil {
		log.Fatal(err)
	}
}
