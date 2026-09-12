package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"harness-forge.local/control-plane/internal/artifacthttp"
	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/config"
	"harness-forge.local/control-plane/internal/conversations"
	"harness-forge.local/control-plane/internal/httpapi"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/profiles"
	"harness-forge.local/control-plane/internal/projects"
	"harness-forge.local/control-plane/internal/runs"
	"harness-forge.local/control-plane/internal/sandbox"
	"harness-forge.local/control-plane/internal/workspaces"
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
	materializer := workspaces.NewMaterializer(applicationConfig.WorkspaceRoot, objects)
	coordinator := runs.NewCoordinator(runStore, profileResolver, materializer, binding, artifacts.NewPublisher(pool, objects))
	reconciler := runs.NewReconciler(runStore, binding, materializer)
	scheduler := runs.NewScheduler(runStore, coordinator, reconciler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scheduler.Run(ctx)
	canceller := runs.NewCanceller(runStore, coordinator.Cancel)
	log.Printf("sandbox provider configured: %s", binding.ID)

	artifactStore := artifacts.NewStore(pool)
	err = serveListeners(applicationConfig.HTTPAddr, applicationConfig.ArtifactAddr,
		httpapi.NewRouter(httpapi.Dependencies{Projects: projectService, Conversations: conversationService, Runs: runStore, Canceller: canceller, Broker: broker, Artifacts: artifactStore, ArtifactPublicOrigin: applicationConfig.ArtifactPublicOrigin}),
		artifacthttp.NewServer(artifactStore, objects, applicationConfig.WebOrigin))
	if err != nil {
		log.Fatal(err)
	}
}

// Bind both ports before serving either; a failed gateway must fail startup.
func serveListeners(controlAddr, artifactAddr string, control, gateway http.Handler) error {
	controlListener, err := net.Listen("tcp", controlAddr)
	if err != nil {
		return fmt.Errorf("bind control plane: %w", err)
	}
	defer controlListener.Close()
	artifactListener, err := net.Listen("tcp", artifactAddr)
	if err != nil {
		return fmt.Errorf("bind artifact gateway: %w", err)
	}
	defer artifactListener.Close()
	controlServer := &http.Server{Handler: control, ReadHeaderTimeout: 5 * time.Second}
	defer controlServer.Close()
	artifactServer := &http.Server{Handler: gateway, ReadHeaderTimeout: 5 * time.Second}
	defer artifactServer.Close()
	log.Printf("control plane listening on %s; artifact gateway on %s", controlListener.Addr(), artifactListener.Addr())
	errors := make(chan error, 2)
	go func() { errors <- controlServer.Serve(controlListener) }()
	go func() { errors <- artifactServer.Serve(artifactListener) }()
	return <-errors
}
