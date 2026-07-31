//go:build integration

package postgres

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"harness-forge.local/control-plane/internal/testsupport"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrateInitialSchema(t *testing.T) {
	ctx := context.Background()
	pool, schema := testsupport.NewPostgresSchema(t, os.Getenv("TEST_DATABASE_URL"))

	if err := Migrate(ctx, pool, schema); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := Migrate(ctx, pool, schema); err != nil {
		t.Fatalf("second migration: %v", err)
	}

	assertCount(t, pool, "SELECT count(*) FROM schema_migrations", 1)
	assertTables(t, pool, []string{
		"artifacts", "conversations", "input_files", "messages", "projects", "run_events", "runs",
	})
	for _, foreignKey := range [][4]string{
		{"input_files", "project_id", "projects", "id"},
		{"conversations", "project_id", "projects", "id"},
		{"messages", "conversation_id", "conversations", "id"},
		{"runs", "conversation_id", "conversations", "id"},
		{"runs", "trigger_message_id", "messages", "id"},
		{"run_events", "run_id", "runs", "id"},
		{"artifacts", "run_id", "runs", "id"},
	} {
		assertForeignKey(t, pool, foreignKey[0], foreignKey[1], foreignKey[2], foreignKey[3])
	}
	assertNullableColumn(t, pool, "projects", "deleted_at")
	assertNullableColumn(t, pool, "conversations", "deleted_at")
	for _, column := range []string{
		"status", "phase", "finalized_at", "source_sdk_session_id", "candidate_sdk_session_id", "sandbox_provider", "sandbox_ref",
	} {
		assertColumn(t, pool, "runs", column)
	}
	assertNullableColumn(t, pool, "runs", "sandbox_provider")
	assertNullableColumn(t, pool, "runs", "sandbox_ref")
	assertRunConstraints(t, pool)
	assertUniqueColumns(t, pool, "run_events", []string{"run_id", "sequence"})
	assertFIFOIndex(t, pool)
}

func TestMigrateConcurrentCalls(t *testing.T) {
	ctx := context.Background()
	pool, schema := testsupport.NewPostgresSchema(t, os.Getenv("TEST_DATABASE_URL"))

	const callers = 4
	errors := make(chan error, callers)
	var waitGroup sync.WaitGroup
	for range callers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			errors <- Migrate(ctx, pool, schema)
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Errorf("concurrent migration: %v", err)
		}
	}
	assertCount(t, pool, "SELECT count(*) FROM schema_migrations", 1)
}

func assertCount(t *testing.T, pool *pgxpool.Pool, query string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), query).Scan(&got); err != nil {
		t.Fatalf("query count: %v", err)
	}
	if got != want {
		t.Errorf("count = %d, want %d", got, want)
	}
}

func assertTables(t *testing.T, pool *pgxpool.Pool, want []string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = current_schema() AND tablename <> 'schema_migrations'
		ORDER BY tablename`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		got = append(got, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tables = %v, want %v", got, want)
	}
}

func assertForeignKey(t *testing.T, pool *pgxpool.Pool, childTable, childColumn, parentTable, parentColumn string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM pg_constraint con
			JOIN pg_class child ON child.oid = con.conrelid
			JOIN pg_namespace namespace ON namespace.oid = child.relnamespace
			JOIN pg_class parent ON parent.oid = con.confrelid
			WHERE con.contype = 'f'
				AND namespace.nspname = current_schema()
				AND child.relname = $1
				AND parent.relname = $3
				AND con.conkey = ARRAY[(
					SELECT attnum FROM pg_attribute WHERE attrelid = child.oid AND attname = $2
				)]::smallint[]
				AND con.confkey = ARRAY[(
					SELECT attnum FROM pg_attribute WHERE attrelid = parent.oid AND attname = $4
				)]::smallint[]
		)`, childTable, childColumn, parentTable, parentColumn).Scan(&exists)
	if err != nil {
		t.Fatalf("query foreign key %s.%s: %v", childTable, childColumn, err)
	}
	if !exists {
		t.Errorf("missing foreign key %s.%s -> %s.%s", childTable, childColumn, parentTable, parentColumn)
	}
}

func assertColumn(t *testing.T, pool *pgxpool.Pool, table, column string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists)
	if err != nil {
		t.Fatalf("query column %s.%s: %v", table, column, err)
	}
	if !exists {
		t.Errorf("missing column %s.%s", table, column)
	}
}

func assertNullableColumn(t *testing.T, pool *pgxpool.Pool, table, column string) {
	t.Helper()
	var nullable string
	err := pool.QueryRow(context.Background(), `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2
	`, table, column).Scan(&nullable)
	if err != nil {
		t.Fatalf("query nullable column %s.%s: %v", table, column, err)
	}
	if nullable != "YES" {
		t.Errorf("%s.%s is_nullable = %s, want YES", table, column, nullable)
	}
}

func assertRunConstraints(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO projects (id, name, profile_id, profile_version)
		VALUES ('00000000-0000-0000-0000-000000000001', 'Project', 'default', 1);
		INSERT INTO conversations (id, project_id, title)
		VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'Conversation');
		INSERT INTO messages (id, conversation_id, role, content)
		VALUES ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000002', 'user', 'Run');
	`); err != nil {
		t.Fatalf("seed run parents: %v", err)
	}
	for index, status := range []string{"queued", "running", "succeeded", "failed", "cancelled", "interrupted"} {
		id := "10000000-0000-0000-0000-00000000000" + string(rune('1'+index))
		insertRun(t, pool, id, status, nil, nil, nil, false)
	}
	insertRun(t, pool, "20000000-0000-0000-0000-000000000001", "unknown", nil, nil, nil, true)
	for index, phase := range []string{"preparing", "agent", "publishing"} {
		id := "30000000-0000-0000-0000-00000000000" + string(rune('1'+index))
		insertRun(t, pool, id, "queued", &phase, nil, nil, false)
	}
	invalidPhase := "unknown"
	insertRun(t, pool, "40000000-0000-0000-0000-000000000001", "queued", &invalidPhase, nil, nil, true)
	insertRun(t, pool, "50000000-0000-0000-0000-000000000001", "queued", nil, nil, nil, false)
	provider := "docker"
	ref := "sandbox-1"
	insertRun(t, pool, "50000000-0000-0000-0000-000000000002", "queued", nil, &provider, nil, false)
	insertRun(t, pool, "50000000-0000-0000-0000-000000000003", "queued", nil, &provider, &ref, false)
	insertRun(t, pool, "50000000-0000-0000-0000-000000000004", "queued", nil, nil, &ref, true)

}

func insertRun(t *testing.T, pool *pgxpool.Pool, id, status string, phase, provider, ref *string, wantError bool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO runs (id, conversation_id, trigger_message_id, status, phase, sandbox_provider, sandbox_ref)
		VALUES ($1, '00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000003', $2, $3, $4, $5)
	`, id, status, phase, provider, ref)
	if wantError && err == nil {
		t.Errorf("insert run status=%q phase=%v provider=%v ref=%v unexpectedly succeeded", status, phase, provider, ref)
	}
	if !wantError && err != nil {
		t.Errorf("insert run status=%q phase=%v provider=%v ref=%v: %v", status, phase, provider, ref, err)
	}
}

func assertUniqueColumns(t *testing.T, pool *pgxpool.Pool, table string, columns []string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM pg_constraint con
			JOIN pg_class relation ON relation.oid = con.conrelid
			JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = current_schema()
				AND relation.relname = $1
				AND con.contype IN ('p', 'u')
				AND (
					SELECT array_agg(attribute.attname::text ORDER BY key.ordinality)
					FROM unnest(con.conkey) WITH ORDINALITY AS key(attnum, ordinality)
					JOIN pg_attribute attribute
						ON attribute.attrelid = relation.oid AND attribute.attnum = key.attnum
				) = $2::text[]
		)`, table, columns).Scan(&exists)
	if err != nil {
		t.Fatalf("query unique columns on %s: %v", table, err)
	}
	if !exists {
		t.Errorf("missing unique constraint on %s(%s)", table, strings.Join(columns, ", "))
	}
}

func assertFIFOIndex(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM pg_index idx
			JOIN pg_class relation ON relation.oid = idx.indrelid
			JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = current_schema()
				AND relation.relname = 'runs'
				AND idx.indnkeyatts >= 2
				AND idx.indkey[0] = (
					SELECT attnum FROM pg_attribute WHERE attrelid = relation.oid AND attname = 'status'
				)
				AND idx.indkey[1] = (
					SELECT attnum FROM pg_attribute WHERE attrelid = relation.oid AND attname = 'created_at'
				)
		)`).Scan(&exists)
	if err != nil {
		t.Fatalf("query FIFO index: %v", err)
	}
	if !exists {
		t.Error("missing runs index prefixed by (status, created_at)")
	}
}
