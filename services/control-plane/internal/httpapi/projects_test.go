package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"harness-forge.local/control-plane/internal/projects"

	"github.com/google/uuid"
)

func TestProjectHTTPRoutesAndStatuses(t *testing.T) {
	id := uuid.New()
	inputID := uuid.New()
	now := time.Now().UTC()
	project := projects.Project{ID: id, Name: "Map", ProfileID: "geo-analysis", ProfileVersion: "1", AcceptedInputMediaTypes: []string{"text/csv"}, CreatedAt: now, UpdatedAt: now}
	input := projects.InputFile{ID: inputID, ProjectID: id, DisplayName: "points.csv", MediaType: "text/csv", SizeBytes: 3, SHA256Digest: strings.Repeat("a", 64), CreatedAt: now}
	service := &fakeProjectService{project: project, projects: []projects.Project{project}, input: input, inputs: []projects.InputFile{input}}
	router := NewRouter(service)

	tests := []struct {
		name, method, path string
		body               io.Reader
		contentType        string
		want               int
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/projects", body: strings.NewReader(`{"name":"Map","profile_id":"geo-analysis"}`), contentType: "application/json", want: http.StatusCreated},
		{name: "list", method: http.MethodGet, path: "/api/v1/projects", want: http.StatusOK},
		{name: "read", method: http.MethodGet, path: "/api/v1/projects/" + id.String(), want: http.StatusOK},
		{name: "rename", method: http.MethodPatch, path: "/api/v1/projects/" + id.String(), body: strings.NewReader(`{"name":"New"}`), contentType: "application/json", want: http.StatusOK},
		{name: "delete", method: http.MethodDelete, path: "/api/v1/projects/" + id.String(), want: http.StatusNoContent},
		{name: "inputs", method: http.MethodGet, path: "/api/v1/projects/" + id.String() + "/inputs", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, tt.body)
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.want, response.Body.String())
			}
			if tt.want != http.StatusNoContent && response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
			}
		})
	}

	body, contentType := multipartBody(t, "points.csv", []byte("a,b"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+id.String()+"/inputs", body)
	req.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body=%s", response.Code, response.Body.String())
	}
	if service.uploadName != "points.csv" || service.uploadMedia != "application/octet-stream" || string(service.uploadData) != "a,b" {
		t.Fatalf("upload args = %q/%q/%q", service.uploadName, service.uploadMedia, service.uploadData)
	}

	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id", "project_id", "display_name", "media_type", "size_bytes", "sha256_digest", "created_at"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("upload response missing %q: %#v", field, decoded)
		}
	}
}

func TestProjectHTTPErrorsMatchComponents(t *testing.T) {
	id := uuid.New()
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "bad request", err: projects.ErrInvalid, want: http.StatusBadRequest},
		{name: "not found", err: projects.ErrNotFound, want: http.StatusNotFound},
		{name: "conflict", err: projects.ErrConflict, want: http.StatusConflict},
		{name: "too large", err: projects.ErrPayloadTooLarge, want: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeProjectService{err: tt.err}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+id.String()+"/inputs", strings.NewReader("not multipart"))
			req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
			response := httptest.NewRecorder()
			NewRouter(service).ServeHTTP(response, req)
			// Bad multipart is itself 400; use create for service-level mappings other than 413.
			if tt.want != http.StatusBadRequest {
				req = httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":"Map","profile_id":"geo-analysis"}`))
				req.Header.Set("Content-Type", "application/json")
				response = httptest.NewRecorder()
				NewRouter(service).ServeHTTP(response, req)
			}
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.want, response.Body.String())
			}
			var component struct {
				Code, Message, RequestID string
				Details                  any `json:"details"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &component); err != nil {
				t.Fatal(err)
			}
			if component.Code == "" || component.Message == "" {
				t.Fatalf("error component = %#v", component)
			}
		})
	}
}

func TestProjectHTTPRejectsInvalidUUIDAndJSON(t *testing.T) {
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/projects/not-a-uuid", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"name":`)),
	} {
		response := httptest.NewRecorder()
		NewRouter(&fakeProjectService{}).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s %s status = %d", request.Method, request.URL.Path, response.Code)
		}
	}
}

func multipartBody(t *testing.T, name string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body, writer.FormDataContentType()
}

type fakeProjectService struct {
	project                 projects.Project
	projects                []projects.Project
	input                   projects.InputFile
	inputs                  []projects.InputFile
	err                     error
	uploadName, uploadMedia string
	uploadData              []byte
}

func (s *fakeProjectService) CreateProject(context.Context, string, string) (projects.Project, error) {
	return s.project, s.err
}
func (s *fakeProjectService) ReadProject(context.Context, uuid.UUID) (projects.Project, error) {
	return s.project, s.err
}
func (s *fakeProjectService) RenameProject(context.Context, uuid.UUID, string) (projects.Project, error) {
	return s.project, s.err
}
func (s *fakeProjectService) ListProjects(context.Context) ([]projects.Project, error) {
	return s.projects, s.err
}
func (s *fakeProjectService) UploadInput(_ context.Context, _ uuid.UUID, name, media string, reader io.Reader) (projects.InputFile, error) {
	if s.err != nil {
		return projects.InputFile{}, s.err
	}
	s.uploadName, s.uploadMedia = name, media
	s.uploadData, _ = io.ReadAll(reader)
	return s.input, nil
}
func (s *fakeProjectService) ListInputs(context.Context, uuid.UUID) ([]projects.InputFile, error) {
	return s.inputs, s.err
}
func (s *fakeProjectService) DeleteProject(context.Context, uuid.UUID) error { return s.err }
