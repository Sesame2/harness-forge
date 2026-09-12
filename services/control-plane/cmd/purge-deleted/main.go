package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"harness-forge.local/control-plane/internal/cleanup"
	"harness-forge.local/control-plane/internal/config"
	"harness-forge.local/control-plane/internal/objectstore"
	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/sandbox"
)

const maintenanceTimeout = 5 * time.Minute

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), maintenanceTimeout)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, output io.Writer) error {
	apply, err := parseApplyMode(args)
	if err != nil {
		return writeResult(output, cleanup.MaintenanceSummary{DryRun: true}, err)
	}
	applicationConfig, err := config.ConfigFromEnv(getenv)
	if err != nil {
		return writeResult(output, cleanup.MaintenanceSummary{DryRun: !apply}, err)
	}
	pool, err := postgres.Open(ctx, applicationConfig.DatabaseURL)
	if err != nil {
		return writeResult(output, cleanup.MaintenanceSummary{DryRun: !apply}, err)
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool, "public"); err != nil {
		return writeResult(output, cleanup.MaintenanceSummary{DryRun: !apply}, err)
	}
	objects, err := objectstore.NewMinIO(ctx, applicationConfig.MinIOEndpoint, applicationConfig.MinIOAccessKey, applicationConfig.MinIOSecretKey, applicationConfig.MinIOBucket)
	if err != nil {
		return writeResult(output, cleanup.MaintenanceSummary{DryRun: !apply}, err)
	}
	binding, err := sandbox.NewProvider(applicationConfig)
	if err != nil {
		return writeResult(output, cleanup.MaintenanceSummary{DryRun: !apply}, err)
	}
	summary, err := cleanup.RunMaintenance(ctx, cleanup.NewPurger(pool, objects, binding, applicationConfig.WorkspaceRoot), cleanup.NewOrphanScanner(pool, objects), apply)
	return writeResult(output, summary, err)
}

func parseApplyMode(args []string) (bool, error) {
	flags := flag.NewFlagSet("purge-deleted", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	apply := flags.Bool("apply", false, "delete eligible resources")
	dryRun := flags.Bool("dry-run", false, "preview eligible resources")
	if err := flags.Parse(args); err != nil {
		return false, err
	}
	if flags.NArg() != 0 {
		return false, errors.New("purge-deleted does not accept positional arguments")
	}
	if *apply && *dryRun {
		return false, errors.New("--apply and --dry-run are mutually exclusive")
	}
	return *apply, nil
}

func writeResult(output io.Writer, summary cleanup.MaintenanceSummary, err error) error {
	result := struct {
		cleanup.MaintenanceSummary
		Error string `json:"error,omitempty"`
	}{MaintenanceSummary: summary}
	if err != nil {
		result.Error = err.Error()
	}
	if encodeErr := json.NewEncoder(output).Encode(result); encodeErr != nil {
		return fmt.Errorf("write purge summary: %w", encodeErr)
	}
	return err
}
