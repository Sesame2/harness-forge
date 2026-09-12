//go:build integration

package conversations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/projects"
	"harness-forge.local/control-plane/internal/runs"
)

func TestPromotionAndParentDeletionBothLockOrders(t *testing.T) {
	for _, parent := range []string{"project", "conversation"} {
		for _, first := range []string{"deletion", "promotion"} {
			t.Run(parent+"/"+first, func(t *testing.T) {
				pool, service, projectID := conversationFixture(t)
				s := runs.NewStore(pool)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				conversation, err := service.CreateConversation(ctx, projectID, "promotion race")
				if err != nil {
					t.Fatal(err)
				}
				message, err := service.SubmitMessage(ctx, conversation.ID, "build")
				if err != nil {
					t.Fatal(err)
				}
				run := message.Run
				_, _ = s.ClaimNext(ctx)
				data, err := s.LoadContext(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				_ = s.SetPhase(ctx, run.ID, runs.Agent)
				_ = s.SetPhase(ctx, run.ID, runs.Publishing)
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
					if err := <-result; !errors.Is(err, runs.ErrConflict) {
						t.Fatalf("promotion after deleted parent=%v", err)
					}
					got, _ := s.Read(ctx, run.ID)
					var n int
					_ = pool.QueryRow(ctx, `SELECT count(*) FROM artifacts`).Scan(&n)
					if got.Status != runs.Running || n != 0 {
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
				go func() {
					if parent == "conversation" {
						deleted <- service.DeleteConversation(ctx, run.ConversationID)
						return
					}
					deleted <- projects.NewStore(pool).DeleteProject(ctx, data.ProjectID)
				}()
				waitDatabaseLock(t, ctx, pool, "%lock project%")
				// Both deletion paths lock the Project before the Conversation.
				if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(918283)`); err != nil {
					t.Fatal(err)
				}
				if err := <-promoted; err != nil {
					t.Fatal(err)
				}
				wantConflict := projects.ErrConflict
				if parent == "conversation" {
					wantConflict = ErrConflict
				}
				if err := <-deleted; !errors.Is(err, wantConflict) {
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
