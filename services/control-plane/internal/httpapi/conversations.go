package httpapi

import (
	"context"
	"net/http"

	"harness-forge.local/control-plane/internal/conversations"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type conversationService interface {
	CreateConversation(context.Context, uuid.UUID, string) (conversations.Conversation, error)
	ListConversations(context.Context, uuid.UUID) ([]conversations.Conversation, error)
	ReadConversation(context.Context, uuid.UUID) (conversations.Conversation, error)
	RenameConversation(context.Context, uuid.UUID, string) (conversations.Conversation, error)
	DeleteConversation(context.Context, uuid.UUID) error
	ListMessages(context.Context, uuid.UUID) ([]conversations.Message, error)
	SubmitMessage(context.Context, uuid.UUID, string) (conversations.SubmitMessageResult, error)
}

type conversationHandlers struct{ service conversationService }

func (h conversationHandlers) create(response http.ResponseWriter, request *http.Request) {
	id, ok := projectID(response, request)
	if !ok {
		return
	}
	var body *struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(request, &body); err != nil || body == nil {
		writeError(response, conversations.ErrInvalid)
		return
	}
	result, err := h.service.CreateConversation(request.Context(), id, body.Title)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, result)
}

func (h conversationHandlers) list(response http.ResponseWriter, request *http.Request) {
	id, ok := projectID(response, request)
	if !ok {
		return
	}
	result, err := h.service.ListConversations(request.Context(), id)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (h conversationHandlers) read(response http.ResponseWriter, request *http.Request) {
	id, ok := conversationID(response, request)
	if !ok {
		return
	}
	result, err := h.service.ReadConversation(request.Context(), id)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (h conversationHandlers) rename(response http.ResponseWriter, request *http.Request) {
	id, ok := conversationID(response, request)
	if !ok {
		return
	}
	var body *struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(request, &body); err != nil || body == nil {
		writeError(response, conversations.ErrInvalid)
		return
	}
	result, err := h.service.RenameConversation(request.Context(), id, body.Title)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (h conversationHandlers) delete(response http.ResponseWriter, request *http.Request) {
	id, ok := conversationID(response, request)
	if !ok {
		return
	}
	if err := h.service.DeleteConversation(request.Context(), id); err != nil {
		writeError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (h conversationHandlers) listMessages(response http.ResponseWriter, request *http.Request) {
	id, ok := conversationID(response, request)
	if !ok {
		return
	}
	result, err := h.service.ListMessages(request.Context(), id)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (h conversationHandlers) submitMessage(response http.ResponseWriter, request *http.Request) {
	id, ok := conversationID(response, request)
	if !ok {
		return
	}
	var body *struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(request, &body); err != nil || body == nil {
		writeError(response, conversations.ErrInvalid)
		return
	}
	result, err := h.service.SubmitMessage(request.Context(), id, body.Content)
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, result)
}

func conversationID(response http.ResponseWriter, request *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(request, "conversation_id"))
	if err != nil {
		writeError(response, conversations.ErrInvalid)
		return uuid.Nil, false
	}
	return id, true
}
