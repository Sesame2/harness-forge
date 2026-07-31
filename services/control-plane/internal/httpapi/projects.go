package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"harness-forge.local/control-plane/internal/projects"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type projectService interface {
	CreateProject(context.Context, string, string) (projects.Project, error)
	ReadProject(context.Context, uuid.UUID) (projects.Project, error)
	RenameProject(context.Context, uuid.UUID, string) (projects.Project, error)
	ListProjects(context.Context) ([]projects.Project, error)
	UploadInput(context.Context, uuid.UUID, string, string, io.Reader) (projects.InputFile, error)
	ListInputs(context.Context, uuid.UUID) ([]projects.InputFile, error)
	DeleteProject(context.Context, uuid.UUID) error
}

type projectHandlers struct{ service projectService }

func (h projectHandlers) create(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Name      string `json:"name"`
		ProfileID string `json:"profile_id"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(response, projects.ErrInvalid)
		return
	}
	project, err := h.service.CreateProject(request.Context(), body.Name, body.ProfileID)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, project)
}

func (h projectHandlers) list(response http.ResponseWriter, request *http.Request) {
	result, err := h.service.ListProjects(request.Context())
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (h projectHandlers) read(response http.ResponseWriter, request *http.Request) {
	id, ok := projectID(response, request)
	if !ok {
		return
	}
	result, err := h.service.ReadProject(request.Context(), id)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (h projectHandlers) rename(response http.ResponseWriter, request *http.Request) {
	id, ok := projectID(response, request)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(response, projects.ErrInvalid)
		return
	}
	result, err := h.service.RenameProject(request.Context(), id, body.Name)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (h projectHandlers) delete(response http.ResponseWriter, request *http.Request) {
	id, ok := projectID(response, request)
	if !ok {
		return
	}
	if err := h.service.DeleteProject(request.Context(), id); err != nil {
		writeError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h projectHandlers) listInputs(response http.ResponseWriter, request *http.Request) {
	id, ok := projectID(response, request)
	if !ok {
		return
	}
	result, err := h.service.ListInputs(request.Context(), id)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (h projectHandlers) uploadInput(response http.ResponseWriter, request *http.Request) {
	id, ok := projectID(response, request)
	if !ok {
		return
	}
	reader, err := request.MultipartReader()
	if err != nil {
		writeError(response, projects.ErrInvalid)
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		if part != nil {
			_ = part.Close()
		}
		writeError(response, fmt.Errorf("%w: multipart body must contain exactly one file field", projects.ErrInvalid))
		return
	}
	mediaType := part.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	file := &singleMultipartFile{part: part, multipart: reader}
	result, uploadErr := h.service.UploadInput(request.Context(), id, part.FileName(), mediaType, file)
	_ = part.Close()
	if uploadErr != nil {
		writeError(response, uploadErr)
		return
	}
	writeJSON(response, http.StatusCreated, result)
}

type singleMultipartFile struct {
	part        *multipart.Part
	multipart   *multipart.Reader
	partAtEOF   bool
	terminalErr error
}

func (r *singleMultipartFile) Read(buffer []byte) (int, error) {
	if r.terminalErr != nil {
		return 0, r.terminalErr
	}
	if !r.partAtEOF {
		count, err := r.part.Read(buffer)
		if err == nil {
			return count, nil
		}
		if !errors.Is(err, io.EOF) {
			r.terminalErr = fmt.Errorf("%w: read multipart file: %v", projects.ErrInvalid, err)
			return count, r.terminalErr
		}
		r.partAtEOF = true
		if count > 0 {
			return count, nil
		}
	}
	next, err := r.multipart.NextPart()
	if errors.Is(err, io.EOF) {
		r.terminalErr = io.EOF
		return 0, io.EOF
	}
	if next != nil {
		_ = next.Close()
	}
	r.terminalErr = fmt.Errorf("%w: multipart body must contain exactly one file field", projects.ErrInvalid)
	return 0, r.terminalErr
}

func projectID(response http.ResponseWriter, request *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(request, "project_id"))
	if err != nil {
		writeError(response, projects.ErrInvalid)
		return uuid.Nil, false
	}
	return id, true
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, projects.ErrInvalid):
		status, code = http.StatusBadRequest, "bad_request"
	case errors.Is(err, projects.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, projects.ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, projects.ErrPayloadTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "payload_too_large"
	}
	message := http.StatusText(status)
	if status < 500 {
		message = strings.TrimSpace(err.Error())
	}
	writeJSON(response, status, map[string]any{"code": code, "message": message, "details": nil, "request_id": uuid.NewString()})
}
