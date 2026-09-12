//go:build integration

package runs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/projects"
)

func TestPromotionAndParentDeletionBothLockOrders(t *testing.T) {
	for _, parent := range []string{"project", "conversation"} {
		for _, first := range []string{"deletion", "promotion"} {
			t.Run(parent+"/"+first, func(t *testing.T) {
				pool := integrationPool(t)
				s := NewStore(pool)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				run := enqueue(t, pool)
				_, _ = s.ClaimNext(ctx)
				data, err := s.LoadContext(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				_ = s.SetPhase(ctx, run.ID, Agent)
				_ = s.SetPhase(ctx, run.ID, Publishing)
				records := []artifacts.Artifact{{ID: uuid.New(), RunID: run.ID, Title: "report", Type: "html", EntryPath: "index.html", ObjectPrefix: "report/", ManifestVersion: 1}}
				if first == "deletion" {
					tx, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(context.Background())
					// Deliberately model retained/stale deletion state; public Delete rejects
					// all active Runs, so it cannot create this recovery fixture itself.
					query := `UPDATE projects SET deleted_at=now() WHERE id=$1`
					id := data.ProjectID
					if parent == "conversation" {
						query = `UPDATE conversations SET deleted_at=now() WHERE id=$1`
						id = run.ConversationID
					}
					if _, err := tx.Exec(ctx, query, id); err != nil {
						t.Fatal(err)
					}
					result := make(chan error, 1)
					go func() { result <- s.CommitProducts(ctx, run.ID, data.ProjectID, "candidate", records) }()
					waitDatabaseLock(t, ctx, pool, "%FOR UPDATE%")
					if err := tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
					if err := <-result; !errors.Is(err, ErrConflict) {
						t.Fatalf("promotion after deleted parent=%v", err)
					}
					got, _ := s.Read(ctx, run.ID)
					var n int
					_ = pool.QueryRow(ctx, `SELECT count(*) FROM artifacts`).Scan(&n)
					if got.Status != Running || n != 0 {
						t.Fatal("deleted parent promoted", got, n)
					}
					return
				}
				// Hold a test advisory lock inside artifact insertion. CommitProducts has
				// already locked Project and Conversation when this trigger blocks.
				conn, err := pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Release()
				defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(918283)`)
				if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(918283)`); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `CREATE FUNCTION pause_products() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM pg_advisory_xact_lock(918283); RETURN NEW; END$$; CREATE TRIGGER pause_products BEFORE INSERT ON artifacts FOR EACH ROW EXECUTE FUNCTION pause_products()`); err != nil {
					t.Fatal(err)
				}
				promoted := make(chan error, 1)
				go func() { promoted <- s.CommitProducts(ctx, run.ID, data.ProjectID, "candidate", records) }()
				waitDatabaseLock(t, ctx, pool, "%INSERT INTO artifacts%")
				deleted := make(chan error, 1)
				go func() { deleted <- projects.NewStore(pool).DeleteProject(ctx, data.ProjectID) }()
				waitDatabaseLock(t, ctx, pool, "%lock project%")
				// DeleteProject's project row query has no comment; explicitly verify a
				// blocked SELECT on projects before allowing publication to commit.
				if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(918283)`); err != nil {
					t.Fatal(err)
				}
				if err := <-promoted; err != nil {
					t.Fatal(err)
				}
				if err := <-deleted; !errors.Is(err, projects.ErrConflict) {
					t.Fatalf("delete bypassed unfinalized success: %v", err)
				}
			})
		}
	}
}

func waitDatabaseLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pattern string) {
	t.Helper()
	if pattern == "%lock project%" {
		pattern = "%FROM projects WHERE id=$1 FOR UPDATE%"
	}
	for {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}
