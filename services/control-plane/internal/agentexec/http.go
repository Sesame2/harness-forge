package agentexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"harness-forge.local/control-plane/internal/contracts"
)

type HTTPExecutor struct {
	baseURL string
	client  *http.Client
}

func NewHTTPExecutor(baseURL string, client *http.Client) (*HTTPExecutor, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrInvalid
	}
	if client == nil {
		client = http.DefaultClient
	}
	// Redirects could resend an execute or forward a snapshot to another origin.
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPExecutor{strings.TrimRight(baseURL, "/"), &copyClient}, nil
}

func (h *HTTPExecutor) Execute(ctx context.Context, request ExecuteRequest) (<-chan Event, <-chan error) {
	events, errs := make(chan Event), make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		fail := func(kind error) { errs <- &RuntimeError{Operation: "execute", RunID: request.RunID, Kind: kind} }
		if err := request.Validate(); err != nil {
			fail(err)
			return
		}
		response, err := h.do(ctx, "POST", "/v1/runs/"+request.RunID.String()+"/execute", request)
		if err != nil {
			fail(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			errs <- responseError("execute", request.RunID, response)
			return
		}
		contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if contentType == "application/json" {
			var result struct {
				Decision Decision `json:"decision"`
			}
			if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result) == nil && (result.Decision == Commit || result.Decision == Abort) {
				errs <- &RuntimeError{Operation: "execute", RunID: request.RunID, StatusCode: response.StatusCode, Decision: result.Decision, Kind: ErrFinalized}
				return
			}
			fail(ErrOutcomeUnknown)
			return
		}
		if contentType != "application/x-ndjson" {
			fail(ErrOutcomeUnknown)
			return
		}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 4<<20)
		var lastSequence uint64
		var seen bool
		var candidate *Event
		var terminal *Event
		for scanner.Scan() {
			event, err := contracts.ParseRuntimeEvent(scanner.Bytes())
			if err != nil || event.RunID != request.RunID.String() || (seen && event.Sequence <= lastSequence) {
				fail(ErrOutcomeUnknown)
				return
			}
			lastSequence, seen = event.Sequence, true
			if terminal != nil {
				if _, unknown := event.Payload.(map[string]any); !unknown {
					fail(ErrOutcomeUnknown)
					return
				}
				continue
			}
			if event.Type == "artifact.candidate" {
				copyEvent := event
				candidate = &copyEvent
			}
			if event.Type == "agent.completed" {
				if candidate == nil || contracts.ValidateRuntimeEventSequence([]Event{*candidate, event}) != nil {
					fail(ErrOutcomeUnknown)
					return
				}
			}
			// Withhold terminal evidence until clean EOF: later known events invalidate the execution.
			if contracts.IsTerminalEvent(event) {
				copyEvent := event
				terminal = &copyEvent
				continue
			}
			select {
			case events <- event:
			case <-ctx.Done():
				fail(ErrOutcomeUnknown)
				return
			}
		}
		if scanner.Err() == nil && terminal != nil {
			select {
			case events <- *terminal:
			case <-ctx.Done():
				fail(ErrOutcomeUnknown)
			}
			return
		}
		// EOF, malformed stream and unknown future events are not proof of completion.
		fail(ErrOutcomeUnknown)
	}()
	return events, errs
}

func (h *HTTPExecutor) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return nil, ErrInvalid
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		if method == http.MethodGet || method == http.MethodHead {
			return nil, ErrUnavailable
		}
		return nil, ErrOutcomeUnknown
	}
	return response, nil
}

func responseError(operation string, id RunID, response *http.Response) error {
	kind := ErrInvalid
	switch {
	case response.StatusCode == 404:
		kind = ErrNotFound
	case response.StatusCode == 409:
		kind = ErrConflict
	case response.StatusCode >= 500 || response.StatusCode == 429:
		kind = ErrUnavailable
	}
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body)
	// Keep recognized machine codes only, never arbitrary upstream text.
	code := ""
	switch body.Code {
	case "already_running", "awaiting_finalize", "invalid_request", "unavailable", "conflict", "not_found":
		code = body.Code
	}
	return &RuntimeError{Operation: operation, RunID: id, StatusCode: response.StatusCode, Code: code, Kind: kind}
}

func (h *HTTPExecutor) operation(ctx context.Context, method, path, operation string, id RunID, body any, absentOK bool) error {
	response, err := h.do(ctx, method, path, body)
	if err != nil {
		return &RuntimeError{Operation: operation, RunID: id, Kind: err}
	}
	defer response.Body.Close()
	if (response.StatusCode >= 200 && response.StatusCode < 300) || (absentOK && response.StatusCode == 404) {
		return nil
	}
	return responseError(operation, id, response)
}
func (h *HTTPExecutor) Cancel(ctx context.Context, id RunID) error {
	return h.operation(ctx, "POST", "/v1/runs/"+id.String()+"/cancel", "cancel", id, nil, false)
}

// Finalize is transport-idempotent: callers may repeat the same decision after lost acknowledgement.
func (h *HTTPExecutor) Finalize(ctx context.Context, id RunID, decision Decision) error {
	if decision != Commit && decision != Abort {
		return ErrInvalid
	}
	return h.operation(ctx, "POST", "/v1/runs/"+id.String()+"/finalize", "finalize", id, struct {
		Decision Decision `json:"decision"`
	}{decision}, false)
}
func (h *HTTPExecutor) DeleteExecution(ctx context.Context, id RunID) error {
	return h.operation(ctx, "DELETE", "/v1/executions/"+id.String(), "delete_execution", id, nil, true)
}
func (h *HTTPExecutor) DeleteSession(ctx context.Context, id SessionID) error {
	return h.operation(ctx, "DELETE", "/v1/sessions/"+url.PathEscape(string(id)), "delete_session", RunID{}, nil, true)
}
func (h *HTTPExecutor) SessionExists(ctx context.Context, id SessionID) (bool, error) {
	err := h.operation(ctx, "HEAD", "/v1/sessions/"+url.PathEscape(string(id)), "session_exists", RunID{}, nil, false)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
func (h *HTTPExecutor) ListExecutions(ctx context.Context) ([]Execution, error) {
	response, err := h.do(ctx, "GET", "/v1/executions", nil)
	if err != nil {
		return nil, &RuntimeError{Operation: "list_executions", Kind: err}
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, responseError("list_executions", RunID{}, response)
	}
	var result []Execution
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(&result); err != nil || result == nil {
		return nil, &RuntimeError{Operation: "list_executions", Kind: ErrUnavailable}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, &RuntimeError{Operation: "list_executions", Kind: ErrUnavailable}
	}
	seen := map[RunID]bool{}
	for _, execution := range result {
		if execution.RunID == (RunID{}) || seen[execution.RunID] || (execution.Lifecycle != Starting && execution.Lifecycle != Running && execution.Lifecycle != AwaitingFinalize) {
			return nil, &RuntimeError{Operation: "list_executions", Kind: ErrUnavailable}
		}
		seen[execution.RunID] = true
	}
	return result, nil
}
