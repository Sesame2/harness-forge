package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"harness-forge.local/control-plane/internal/conversations"
	"harness-forge.local/control-plane/internal/runs"

	"github.com/google/uuid"
)

func TestConversationMessageHTTPRoutesAndContract(t *testing.T) {
	now := time.Now().UTC()
	project, id := uuid.New(), uuid.New()
	conversation := conversations.Conversation{ID: id, ProjectID: project, Title: "title", CreatedAt: now, UpdatedAt: now}
	message := conversations.Message{ID: uuid.New(), ConversationID: id, Role: "user", Content: "prompt", CreatedAt: now}
	secret := "must-not-leak"
	run := runs.Run{ID: uuid.New(), ConversationID: id, TriggerMessageID: message.ID, Status: runs.Queued, CreatedAt: now, UpdatedAt: now, SandboxRef: &secret}
	service := &fakeConversationService{conversation: conversation, message: message, run: run}
	router := NewRouter(Dependencies{Projects: &fakeProjectService{}, Conversations: service})
	base := "/api/v1/conversations/" + id.String()
	projectPath := "/api/v1/projects/" + project.String() + "/conversations"
	for _, tc := range []struct {
		method, path, body string
		status             int
		fields             []string
	}{
		{"POST", projectPath, `{"title":null}`, 201, []string{"id", "project_id", "title", "active_sdk_session_id", "created_at", "updated_at"}},
		{"POST", projectPath, `{}`, 201, nil},
		{"GET", projectPath, "", 200, nil},
		{"GET", base, "", 200, nil},
		{"PATCH", base, `{"title":"renamed"}`, 200, nil},
		{"GET", base + "/messages", "", 200, nil},
		{"POST", base + "/messages", `{"content":"prompt"}`, 201, []string{"message", "run"}},
		{"DELETE", base, "", 204, nil},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.status == 204 {
				if response.Body.Len() != 0 {
					t.Fatal("204 body")
				}
				return
			}
			if response.Header().Get("Content-Type") != "application/json" {
				t.Fatal("missing JSON content type")
			}
			if strings.Contains(response.Body.String(), secret) || strings.Contains(response.Body.String(), "deleted_at") {
				t.Fatal("internal fields leaked")
			}
			if tc.method == "GET" && (tc.path == projectPath || strings.HasSuffix(tc.path, "/messages")) {
				var list []json.RawMessage
				if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil || len(list) != 1 {
					t.Fatalf("list=%s %v", response.Body.String(), err)
				}
			}
			if len(tc.fields) > 0 {
				var object map[string]json.RawMessage
				if err := json.Unmarshal(response.Body.Bytes(), &object); err != nil {
					t.Fatal(err)
				}
				if len(object) != len(tc.fields) {
					t.Fatalf("fields=%s", response.Body.String())
				}
				for _, field := range tc.fields {
					if _, ok := object[field]; !ok {
						t.Fatalf("missing %s", field)
					}
				}
				if tc.path == base+"/messages" {
					var got conversations.SubmitMessageResult
					if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					if got.Message.ID != message.ID || got.Run.TriggerMessageID != message.ID || got.Run.Status != runs.Queued {
						t.Fatalf("result=%#v", got)
					}
				}
			}
		})
	}
	if service.projectID != project || service.conversationID != id || service.title != "renamed" || service.content != "prompt" {
		t.Fatalf("route arguments=%#v", service)
	}
}

func TestConversationMessageHTTPErrorAndValidation(t *testing.T) {
	base := "/api/v1/conversations/" + uuid.NewString()
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{conversations.ErrInvalid, 400, "bad_request"}, {conversations.ErrNotFound, 404, "not_found"}, {conversations.ErrConflict, 409, "conflict"}, {errors.New("secret SQL"), 500, "internal_error"},
	} {
		service := &fakeConversationService{err: tc.err}
		router := NewRouter(Dependencies{Conversations: service})
		for _, request := range []struct{ method, path, body string }{
			{"GET", base, ""}, {"DELETE", base, ""}, {"PATCH", base, `{"title":"title"}`}, {"GET", base + "/messages", ""}, {"POST", base + "/messages", `{"content":"prompt"}`}, {"GET", "/api/v1/projects/" + uuid.NewString() + "/conversations", ""}, {"POST", "/api/v1/projects/" + uuid.NewString() + "/conversations", `{}`},
		} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(request.method, request.path, strings.NewReader(request.body)))
			if response.Code != tc.status {
				t.Fatalf("%s %s status=%d want=%d", request.method, request.path, response.Code, tc.status)
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 4 || body["code"] != tc.code || body["request_id"] == "" {
				t.Fatalf("error=%#v", body)
			}
			if tc.status == 500 && strings.Contains(response.Body.String(), "secret") {
				t.Fatal("leaked internal error")
			}
		}
	}
	service := &fakeConversationService{}
	router := NewRouter(Dependencies{Conversations: service})
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/conversations/nope", ""}, {"POST", base + "/messages", `{`}, {"POST", base + "/messages", `{"content":"p","unknown":1}`}, {"POST", base + "/messages", `{"content":"p"} {}`}, {"POST", "/api/v1/projects/nope/conversations", `{}`}, {"PATCH", base, `{"title":123}`}, {"POST", "/api/v1/projects/" + uuid.NewString() + "/conversations", `null`},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if response.Code != 400 {
			t.Fatalf("invalid %s => %d %s", tc.body, response.Code, response.Body.String())
		}
	}
	if service.calls != 0 {
		t.Fatalf("invalid input reached service %d times", service.calls)
	}
}

type fakeConversationService struct {
	conversation              conversations.Conversation
	message                   conversations.Message
	run                       runs.Run
	err                       error
	calls                     int
	projectID, conversationID uuid.UUID
	title, content            string
}

func (f *fakeConversationService) CreateConversation(_ context.Context, id uuid.UUID, title string) (conversations.Conversation, error) {
	f.calls++
	f.projectID = id
	f.title = title
	return f.conversation, f.err
}
func (f *fakeConversationService) ListConversations(_ context.Context, id uuid.UUID) ([]conversations.Conversation, error) {
	f.calls++
	f.projectID = id
	return []conversations.Conversation{f.conversation}, f.err
}
func (f *fakeConversationService) ReadConversation(_ context.Context, id uuid.UUID) (conversations.Conversation, error) {
	f.calls++
	f.conversationID = id
	return f.conversation, f.err
}
func (f *fakeConversationService) RenameConversation(_ context.Context, id uuid.UUID, title string) (conversations.Conversation, error) {
	f.calls++
	f.conversationID = id
	f.title = title
	return f.conversation, f.err
}
func (f *fakeConversationService) DeleteConversation(_ context.Context, id uuid.UUID) error {
	f.calls++
	f.conversationID = id
	return f.err
}
func (f *fakeConversationService) ListMessages(_ context.Context, id uuid.UUID) ([]conversations.Message, error) {
	f.calls++
	f.conversationID = id
	return []conversations.Message{f.message}, f.err
}
func (f *fakeConversationService) SubmitMessage(_ context.Context, id uuid.UUID, content string) (conversations.SubmitMessageResult, error) {
	f.calls++
	f.conversationID = id
	f.content = content
	return conversations.SubmitMessageResult{Message: f.message, Run: f.run}, f.err
}
