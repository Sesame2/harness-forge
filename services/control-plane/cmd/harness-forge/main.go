package main

import (
	"log"
	"net/http"
	"os"

	"harness-forge.local/control-plane/internal/config"
	"harness-forge.local/control-plane/internal/httpapi"
)

func main() {
	applicationConfig, err := config.ConfigFromEnv(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("control plane listening on %s", applicationConfig.HTTPAddr)
	if err := http.ListenAndServe(applicationConfig.HTTPAddr, httpapi.NewRouter()); err != nil {
		log.Fatal(err)
	}
}
