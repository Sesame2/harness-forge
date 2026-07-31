package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"harness-forge.local/control-plane/migrations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Migrate(ctx context.Context, pool *pgxpool.Pool, schema string) (err error) {
	if schema == "" {
		return fmt.Errorf("migrate postgres: schema is required")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin postgres migration: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); err == nil && rollbackErr != nil && rollbackErr != pgx.ErrTxClosed {
			err = fmt.Errorf("rollback postgres migration: %w", rollbackErr)
		}
	}()

	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", schema); err != nil {
		return fmt.Errorf("lock postgres migrations for schema %q: %w", schema, err)
	}
	searchPath := pgx.Identifier{schema}.Sanitize()
	if _, err = tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", searchPath); err != nil {
		return fmt.Errorf("set postgres migration schema %q: %w", schema, err)
	}
	if _, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create postgres migration version table: %w", err)
	}

	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return fmt.Errorf("read embedded postgres migrations: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var applied bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", entry.Name()).Scan(&applied); err != nil {
			return fmt.Errorf("check postgres migration %s: %w", entry.Name(), err)
		}
		if applied {
			continue
		}
		migration, readErr := migrations.Files.ReadFile(entry.Name())
		if readErr != nil {
			return fmt.Errorf("read postgres migration %s: %w", entry.Name(), readErr)
		}
		if _, err = tx.Exec(ctx, string(migration), pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("apply postgres migration %s: %w", entry.Name(), err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", entry.Name()); err != nil {
			return fmt.Errorf("record postgres migration %s: %w", entry.Name(), err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit postgres migrations: %w", err)
	}
	return nil
}
