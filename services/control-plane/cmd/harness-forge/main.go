package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"harness-forge.local/control-plane/internal/config"
	"harness-forge.local/control-plane/internal/httpapi"
	"harness-forge.local/control-plane/internal/postgres"
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
	if err := postgres.Migrate(context.Background(), pool, "public"); err != nil {
		pool.Close()
		log.Fatal(err)
	}

	log.Printf("control plane listening on %s", applicationConfig.HTTPAddr)
	err = http.ListenAndServe(applicationConfig.HTTPAddr, httpapi.NewRouter())
	pool.Close()
	if err != nil {
		log.Fatal(err)
	}
}
