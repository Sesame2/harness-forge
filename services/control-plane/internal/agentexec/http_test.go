package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"harness-forge.local/control-plane/internal/contracts"
)

func requestFixture() ExecuteRequest {
	source := SessionID("source-session")
	return ExecuteRequest{Version: "1", RunID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), ProjectID: uuid.New(), ConversationID: uuid.New(), Prompt: "build report", SourceSDKSessionID: &source, Profile: Profile{ID: "geo-analysis", Version: "1", Digest: "sha256:test", Config: map[string]any{"allowed_tools": []any{"Read", "Write"}}}, Paths: Paths{Inputs: "/runs/1/inputs", Workspace: "/runs/1/workspace", Outputs: "/runs/1/outputs"}, Limits: Limits{MaxTurns: 8, MaxBudgetUSD: 2}}
}

func runtimeClient(t *testing.T, handler http.HandlerFunc) *HTTPExecutor {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewHTTPExecutor(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func collect(events <-chan Event, errs <-chan error) ([]Event, error) {
	var got []Event
	for event := range events {
		got = append(got, event)
	}
	var failure error
	for err := range errs {
		failure = errors.Join(failure, err)
	}
	return got, failure
}

func TestRuntimeExecuteCarriesSnapshotAndTypedEvents(t *testing.T) {
	request := requestFixture()
	client := runtimeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/runs/"+request.RunID.String()+"/execute" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		var got ExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(got, request) {
			t.Errorf("request mismatch: %#v", got)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintf(w, `{"version":"1","run_id":%q,"sequence":1,"type":"assistant.delta","occurred_at":"2026-07-19T00:00:01Z","payload":{"text":"hello"}}`+"\n", request.RunID)
		fmt.Fprintf(w, `{"version":"1","run_id":%q,"sequence":2,"type":"agent.failed","occurred_at":"2026-07-19T00:00:02Z","payload":{"code":"test","message":"done","retryable":false}}`+"\n", request.RunID)
	})
	events, err := collect(client.Execute(context.Background(), request))
	if err != nil || len(events) != 2 {
		t.Fatalf("events=%#v error=%v", events, err)
	}
	if got, ok := events[0].Payload.(contracts.AssistantDeltaPayload); !ok || got.Text != "hello" {
		t.Fatalf("payload=%#v", events[0].Payload)
	}
}

func TestRuntimeExecuteRejectionsAndFinalizedNeverRerun(t *testing.T) {
	for _, tc := range []struct {
		status   int
		body     string
		want     error
		decision Decision
	}{
		{409, `{"code":"already_running","message":"busy"}`, ErrConflict, ""},
		{409, `{"code":"awaiting_finalize"}`, ErrConflict, ""},
		{400, `{"code":"invalid_request"}`, ErrInvalid, ""},
		{503, `{"code":"unavailable"}`, ErrUnavailable, ""},
		{200, `{"decision":"commit"}`, ErrFinalized, Commit},
		{200, `{"decision":"abort"}`, ErrFinalized, Abort},
	} {
		t.Run(tc.body, func(t *testing.T) {
			client := runtimeClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			events, err := collect(client.Execute(context.Background(), requestFixture()))
			if len(events) != 0 || !errors.Is(err, tc.want) {
				t.Fatalf("events=%v error=%v", events, err)
			}
			var typed *RuntimeError
			if !errors.As(err, &typed) || typed.Operation != "execute" || typed.RunID != requestFixture().RunID || typed.Decision != tc.decision {
				t.Fatalf("typed=%#v", typed)
			}
		})
	}
}

func TestRuntimeExecuteLostHeadersAndInvalidTerminalAreUnknown(t *testing.T) {
	for _, mode := range []string{"lost_headers", "unknown_terminal", "invalid_terminal", "wrong_run"} {
		t.Run(mode, func(t *testing.T) {
			client := runtimeClient(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "lost_headers" {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				typ, payload, id := "agent.future_completed", `{}`, requestFixture().RunID
				if mode == "invalid_terminal" {
					typ = "agent.completed"
				}
				if mode == "wrong_run" {
					typ = "assistant.delta"
					payload = `{"text":"hi"}`
					id = uuid.New()
				}
				fmt.Fprintf(w, `{"version":"1","run_id":%q,"sequence":1,"type":%q,"occurred_at":"2026-07-19T00:00:01Z","payload":%s}`+"\n", id, typ, payload)
			})
			_, err := collect(client.Execute(context.Background(), requestFixture()))
			if !errors.Is(err, ErrOutcomeUnknown) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestRuntimeExecuteRejectsRelativePathsBeforeTransport(t *testing.T) {
	client := runtimeClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("transport called") })
	request := requestFixture()
	request.Paths.Outputs = "relative"
	_, err := collect(client.Execute(context.Background(), request))
	if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrOutcomeUnknown) {
		t.Fatal(err)
	}
}

func TestRuntimeExecuteRejectsKnownEventsAfterTerminalBeforeDeliveringTerminal(t *testing.T) {
	for _, extra := range []string{"assistant.delta", "agent.failed"} {
		t.Run(extra, func(t *testing.T) {
			client := runtimeClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/x-ndjson")
				fmt.Fprintf(w, `{"version":"1","run_id":%q,"sequence":1,"type":"agent.failed","occurred_at":"2026-07-19T00:00:02Z","payload":{"code":"test","message":"done","retryable":false}}`+"\n", requestFixture().RunID)
				payload := `{"text":"late"}`
				if extra == "agent.failed" {
					payload = `{"code":"test","message":"twice","retryable":false}`
				}
				fmt.Fprintf(w, `{"version":"1","run_id":%q,"sequence":2,"type":%q,"occurred_at":"2026-07-19T00:00:02Z","payload":%s}`+"\n", requestFixture().RunID, extra, payload)
			})
			events, err := collect(client.Execute(context.Background(), requestFixture()))
			if !errors.Is(err, ErrOutcomeUnknown) {
				t.Fatalf("error=%v", err)
			}
			for _, event := range events {
				if contracts.IsTerminalEvent(event) {
					t.Fatal("delivered terminal before validating EOF")
				}
			}
		})
	}
}

func TestRuntimeLifecycleMethods(t *testing.T) {
	id := requestFixture().RunID
	var calls []string
	client := runtimeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/executions":
			json.NewEncoder(w).Encode([]Execution{{RunID: id, Lifecycle: Starting}, {RunID: uuid.New(), Lifecycle: Running}, {RunID: uuid.New(), Lifecycle: AwaitingFinalize}})
		case strings.HasSuffix(r.URL.Path, "/finalize"):
			var body struct {
				Decision Decision `json:"decision"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Decision != Abort {
				t.Errorf("decision=%s", body.Decision)
			}
		}
	})
	ctx := context.Background()
	if err := client.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := client.Finalize(ctx, id, Abort); err != nil {
			t.Fatal(err)
		}
	}
	executions, err := client.ListExecutions(ctx)
	if err != nil || len(executions) != 3 || executions[2].Lifecycle != AwaitingFinalize {
		t.Fatalf("%#v %v", executions, err)
	}
	if exists, err := client.SessionExists(ctx, "source/session"); !exists || err != nil {
		t.Fatalf("%v %v", exists, err)
	}
	if err := client.DeleteExecution(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteSession(ctx, "source/session"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 7 {
		t.Fatalf("calls=%v", calls)
	}
}

func TestRuntimeSessionExistsAndDeleteStatus(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := runtimeClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
			exists, err := client.SessionExists(context.Background(), "session")
			if exists != (status == 200) || (err != nil) != (status == 500) {
				t.Fatalf("exists=%v error=%v", exists, err)
			}
			err = client.DeleteExecution(context.Background(), uuid.New())
			if (err != nil) != (status == 500) {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeListRejectsMalformedAuthoritativeEvidence(t *testing.T) {
	for _, body := range []string{`[] trailing`, `null`, `[{"run_id":"00000000-0000-0000-0000-000000000001","lifecycle":"active"}]`} {
		t.Run(body, func(t *testing.T) {
			client := runtimeClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
			if records, err := client.ListExecutions(context.Background()); err == nil {
				t.Fatalf("accepted corrupt authoritative list: %#v", records)
			}
		})
	}
}

func TestRuntimeUnknownNonterminalDoesNotPreventValidCompletion(t *testing.T) {
	client := runtimeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintf(w, `{"version":"1","run_id":%q,"sequence":1,"type":"future.progress","occurred_at":"2026-07-19T00:00:01Z","payload":{"progress":20}}`+"\n", requestFixture().RunID)
		fmt.Fprintf(w, `{"version":"1","run_id":%q,"sequence":2,"type":"agent.failed","occurred_at":"2026-07-19T00:00:02Z","payload":{"code":"test","message":"done","retryable":false}}`+"\n", requestFixture().RunID)
	})
	events, err := collect(client.Execute(context.Background(), requestFixture()))
	if err != nil || len(events) != 2 || !contracts.IsTerminalEvent(events[1]) {
		t.Fatalf("%#v %v", events, err)
	}
}
