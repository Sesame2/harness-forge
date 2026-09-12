//go:build integration

package conversations

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"harness-forge.local/control-plane/internal/postgres"
	"harness-forge.local/control-plane/internal/projects"
	"harness-forge.local/control-plane/internal/runs"
	"harness-forge.local/control-plane/internal/testsupport"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func conversationFixture(t *testing.T) (*pgxpool.Pool, *Service, uuid.UUID) {
	t.Helper()
	pool, schema := testsupport.NewPostgresSchema(t, os.Getenv("TEST_DATABASE_URL"))
	if err := postgres.Migrate(context.Background(), pool, schema); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO projects(id,name,profile_id,profile_version) VALUES($1,'p','geo-analysis','1')`, id); err != nil {
		t.Fatal(err)
	}
	return pool, NewService(NewStore(pool), runs.NewStore(pool)), id
}

func TestConversationLifecycleAndMessageTitle(t *testing.T) {
	pool, service, project := conversationFixture(t)
	ctx := context.Background()
	first, err := service.CreateConversation(ctx, project, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateConversation(ctx, project, "  explicit  ")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || second.Title != "explicit" || first.ActiveSDKSessionID != nil {
		t.Fatalf("conversations: %#v %#v", first, second)
	}
	// Make the ordering independent of clock resolution and insertion order.
	if _, err := pool.Exec(ctx, `UPDATE conversations SET updated_at='2000-01-01' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	list, err := service.ListConversations(ctx, project)
	if err != nil || len(list) != 2 || list[0].ID != first.ID {
		t.Fatalf("list=%#v %v", list, err)
	}
	content := " \n" + strings.Repeat("界", 41) + "\t tail "
	result, err := service.SubmitMessage(ctx, first.ID, content)
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Content != strings.TrimSpace(content) || result.Message.Role != "user" || result.Run.Status != runs.Queued || result.Run.TriggerMessageID != result.Message.ID || result.Run.ConversationID != first.ID {
		t.Fatalf("result=%#v", result)
	}
	read, err := service.ReadConversation(ctx, first.ID)
	if err != nil || read.Title != strings.Repeat("界", 40) || !read.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("read=%#v %v", read, err)
	}
	renamed, err := service.RenameConversation(ctx, first.ID, "  Manual title  ")
	if err != nil || renamed.Title != "Manual title" {
		t.Fatalf("rename=%#v %v", renamed, err)
	}
	if _, err := service.SubmitMessage(ctx, first.ID, "another prompt"); err != nil {
		t.Fatal(err)
	}
	read, err = service.ReadConversation(ctx, first.ID)
	if err != nil || read.Title != "Manual title" {
		t.Fatalf("later message changed title: %#v %v", read, err)
	}
	messages, err := service.ListMessages(ctx, first.ID)
	if err != nil || len(messages) != 2 || messages[0].ID != result.Message.ID {
		t.Fatalf("messages=%#v %v", messages, err)
	}
	if _, err := service.SubmitMessage(ctx, second.ID, "automatic?"); err != nil {
		t.Fatal(err)
	}
	read, err = service.ReadConversation(ctx, second.ID)
	if err != nil || read.Title != "explicit" {
		t.Fatalf("explicit title=%#v %v", read, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE runs SET status='succeeded',finalized_at=now()`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := service.DeleteConversation(ctx, first.ID); err != nil {
			t.Fatal(err)
		}
	}
	var deleted bool
	if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM conversations WHERE id=$1`, first.ID).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("not logical delete: %v %v", deleted, err)
	}
	if _, err := service.ReadConversation(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read deleted=%v", err)
	}
	if _, err := service.ListMessages(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("list deleted=%v", err)
	}
	if _, err := service.SubmitMessage(ctx, first.ID, "late"); !errors.Is(err, ErrConflict) {
		t.Fatalf("submit deleted=%v", err)
	}
	if _, err := service.RenameConversation(ctx, first.ID, "late"); !errors.Is(err, ErrConflict) {
		t.Fatalf("rename deleted=%v", err)
	}
	list, err = service.ListConversations(ctx, project)
	if err != nil || len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("deleted list=%#v %v", list, err)
	}
	if err := service.DeleteConversation(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing delete=%v", err)
	}
	if _, err := service.CreateConversation(ctx, uuid.New(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project=%v", err)
	}
	if err := projects.NewStore(pool).DeleteProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitMessage(ctx, second.ID, "late project"); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted project submit=%v", err)
	}
	if _, err := service.CreateConversation(ctx, project, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted project create=%v", err)
	}
	if _, err := service.ListConversations(ctx, project); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted project list=%v", err)
	}
	if _, err := service.ReadConversation(ctx, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted parent read=%v", err)
	}
}

func TestConversationDeleteProtectsEveryUnfinalizedRun(t *testing.T) {
	for _, status := range []string{"queued", "running", "succeeded", "failed", "cancelled", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			pool, service, project := conversationFixture(t)
			ctx := context.Background()
			conversation, err := service.CreateConversation(ctx, project, "")
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.SubmitMessage(ctx, conversation.ID, "prompt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE runs SET status=$2 WHERE id=$1`, result.Run.ID, status); err != nil {
				t.Fatal(err)
			}
			if err := service.DeleteConversation(ctx, conversation.ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("delete %s=%v", status, err)
			}
			if status == "queued" || status == "running" {
				if _, err := pool.Exec(ctx, `UPDATE runs SET finalized_at=now() WHERE id=$1`, result.Run.ID); err != nil {
					t.Fatal(err)
				}
				if err := service.DeleteConversation(ctx, conversation.ID); !errors.Is(err, ErrConflict) {
					t.Fatalf("finalized active delete=%v", err)
				}
			}
		})
	}
}

func TestMessageAndRunAtomicity(t *testing.T) {
	for _, failure := range []string{"messages", "runs", "none"} {
		t.Run(failure, func(t *testing.T) {
			pool, service, project := conversationFixture(t)
			ctx := context.Background()
			conversation, err := service.CreateConversation(ctx, project, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE conversations SET active_sdk_session_id='active-session' WHERE id=$1`, conversation.ID); err != nil {
				t.Fatal(err)
			}
			if failure != "none" {
				if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced insert failure'; END $$`); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `CREATE TRIGGER reject_insert BEFORE INSERT ON `+failure+` FOR EACH ROW EXECUTE FUNCTION reject_insert()`); err != nil {
					t.Fatal(err)
				}
			}
			result, err := service.SubmitMessage(ctx, conversation.ID, "prompt")
			want := 0
			if failure == "none" {
				want = 1
				if err != nil || result.Run.SourceSDKSessionID == nil || *result.Run.SourceSDKSessionID != "active-session" {
					t.Fatalf("success=%#v %v", result, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "forced insert failure") {
				t.Fatalf("failure=%v", err)
			}
			var messages, runCount int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM messages),(SELECT count(*) FROM runs)`).Scan(&messages, &runCount); err != nil {
				t.Fatal(err)
			}
			if messages != want || runCount != want {
				t.Fatalf("partial commit: messages=%d runs=%d want=%d", messages, runCount, want)
			}
			if want == 0 {
				read, err := service.ReadConversation(ctx, conversation.ID)
				if err != nil || read.Title != "" || !read.UpdatedAt.Equal(conversation.UpdatedAt) {
					t.Fatalf("rollback title/time=%#v %v", read, err)
				}
			} else {
				if _, err := runs.NewStore(pool).Read(ctx, result.Run.ID); err != nil {
					t.Fatal(err)
				}
				got, err := service.ListMessages(ctx, conversation.ID)
				if err != nil || len(got) != 1 || got[0].ID != result.Message.ID {
					t.Fatalf("visible messages=%#v %v", got, err)
				}
			}
		})
	}
}

func TestConversationSubmitDeleteConcurrentLockOrder(t *testing.T) {
	for _, deleteFirst := range []bool{true, false} {
		name := "submit_first"
		if deleteFirst {
			name = "delete_first"
		}
		t.Run(name, func(t *testing.T) {
			pool, service, project := conversationFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conversation, err := service.CreateConversation(ctx, project, "")
			if err != nil {
				t.Fatal(err)
			}
			// A trigger stalls the first real operation after it acquires BOTH row locks.
			if _, err := pool.Exec(ctx, `CREATE FUNCTION pause_operation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(7021); RETURN NEW; END $$`); err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER pause_operation BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION pause_operation()`
			if deleteFirst {
				trigger = `CREATE TRIGGER pause_operation BEFORE UPDATE ON conversations FOR EACH ROW WHEN (NEW.deleted_at IS NOT NULL) EXECUTE FUNCTION pause_operation()`
			}
			if _, err := pool.Exec(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			gate, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock(7021)`); err != nil {
				t.Fatal(err)
			}
			dedicated := func(label string) (*Service, uint32) {
				config := pool.Config()
				config.MaxConns = 1
				config.MinConns = 0
				config.ConnConfig.RuntimeParams["application_name"] = "task7-" + label + uuid.NewString()
				p, err := pgxpool.NewWithConfig(ctx, config)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(p.Close)
				conn, err := p.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				pid := conn.Conn().PgConn().PID()
				conn.Release()
				return NewService(NewStore(p), runs.NewStore(p)), pid
			}
			first, pid1 := dedicated("first")
			second, pid2 := dedicated("second")
			done1, done2 := make(chan error, 1), make(chan error, 1)
			operation := func(s *Service, del bool) error {
				if del {
					return s.DeleteConversation(ctx, conversation.ID)
				}
				_, err := s.SubmitMessage(ctx, conversation.ID, "prompt")
				return err
			}
			go func() { done1 <- operation(first, deleteFirst) }()
			waitLock := func(pid uint32, blocker *uint32) {
				t.Helper()
				for {
					var waiting bool
					err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock' AND ($2::integer IS NULL OR $2=ANY(pg_blocking_pids(pid))))`, pid, blocker).Scan(&waiting)
					if err != nil {
						t.Fatal(err)
					}
					if waiting {
						return
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(5 * time.Millisecond):
					}
				}
			}
			waitLock(pid1, nil)
			go func() { done2 <- operation(second, !deleteFirst) }()
			waitLock(pid2, &pid1)
			t.Logf("verified backend %d is lock-waiting behind first operation backend %d", pid2, pid1)
			if err := gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done1; err != nil {
				t.Fatalf("first=%v", err)
			}
			if err := <-done2; !errors.Is(err, ErrConflict) {
				t.Fatalf("second=%v", err)
			}
			var deleted bool
			var messages, runCount int
			if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL,(SELECT count(*) FROM messages WHERE conversation_id=$1),(SELECT count(*) FROM runs WHERE conversation_id=$1) FROM conversations WHERE id=$1`, conversation.ID).Scan(&deleted, &messages, &runCount); err != nil {
				t.Fatal(err)
			}
			want := 1
			if deleteFirst {
				want = 0
			}
			if deleted != deleteFirst || messages != want || runCount != want {
				t.Fatalf("late write: deleted=%v messages=%d runs=%d", deleted, messages, runCount)
			}
		})
	}
}
