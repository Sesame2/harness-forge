package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ArtifactCandidate struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	Entry   string `json:"entry"`
	Primary bool   `json:"primary"`
}

func (artifact *ArtifactCandidate) UnmarshalJSON(data []byte) error {
	if _, err := requireObjectFields(data, "name", "title", "type", "entry", "primary"); err != nil {
		return err
	}
	type artifactCandidate ArtifactCandidate
	var decoded artifactCandidate
	if err := decodeStrict(data, &decoded); err != nil {
		return err
	}
	*artifact = ArtifactCandidate(decoded)
	return nil
}

type PhaseChangedPayload struct {
	Phase string `json:"phase"`
}

type AssistantDeltaPayload struct {
	Text string `json:"text"`
}

type AssistantMessagePayload struct {
	Text string `json:"text"`
}

type ToolStartedPayload struct {
	ToolCallID string         `json:"tool_call_id"`
	Name       string         `json:"name"`
	Input      map[string]any `json:"input"`
}

type ToolCompletedPayload struct {
	ToolCallID string  `json:"tool_call_id"`
	Name       string  `json:"name"`
	Outcome    string  `json:"outcome"`
	Output     *string `json:"output,omitempty"`
	Error      *string `json:"error,omitempty"`
}

type ArtifactCandidatePayload struct {
	Artifacts []ArtifactCandidate `json:"artifacts"`
}

type AgentCompletedPayload struct {
	CandidateSDKSessionID string              `json:"candidate_sdk_session_id"`
	Artifacts             []ArtifactCandidate `json:"artifacts"`
}

type AgentFailedPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type RuntimeEvent struct {
	Version    string    `json:"version"`
	RunID      string    `json:"run_id"`
	Sequence   uint64    `json:"sequence"`
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
	Payload    any       `json:"payload"`
}

type runtimeEventEnvelope struct {
	Version    string          `json:"version"`
	RunID      string          `json:"run_id"`
	Sequence   uint64          `json:"sequence"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

func ParseRuntimeEvent(data []byte) (RuntimeEvent, error) {
	if _, err := requireObjectFields(data, "version", "run_id", "sequence", "type", "occurred_at", "payload"); err != nil {
		return RuntimeEvent{}, fmt.Errorf("decode runtime event envelope: %w", err)
	}
	var envelope runtimeEventEnvelope
	if err := decodeStrict(data, &envelope); err != nil {
		return RuntimeEvent{}, fmt.Errorf("decode runtime event envelope: %w", err)
	}
	if envelope.Version != "1" {
		return RuntimeEvent{}, fmt.Errorf("unsupported runtime event version %q", envelope.Version)
	}
	if _, err := uuid.Parse(envelope.RunID); err != nil {
		return RuntimeEvent{}, fmt.Errorf("invalid run_id: %w", err)
	}
	if strings.TrimSpace(envelope.Type) == "" {
		return RuntimeEvent{}, errors.New("runtime event type must not be empty")
	}
	if envelope.OccurredAt.IsZero() {
		return RuntimeEvent{}, errors.New("occurred_at must be a date-time")
	}

	payload, err := parseEventPayload(envelope.Type, envelope.Payload)
	if err != nil {
		return RuntimeEvent{}, fmt.Errorf("invalid %s payload: %w", envelope.Type, err)
	}
	return RuntimeEvent{
		Version:    envelope.Version,
		RunID:      envelope.RunID,
		Sequence:   envelope.Sequence,
		Type:       envelope.Type,
		OccurredAt: envelope.OccurredAt,
		Payload:    payload,
	}, nil
}

func parseEventPayload(eventType string, data []byte) (any, error) {
	requiredFields := map[string][]string{
		"phase.changed":      {"phase"},
		"assistant.delta":    {"text"},
		"assistant.message":  {"text"},
		"tool.started":       {"tool_call_id", "name", "input"},
		"tool.completed":     {"tool_call_id", "name", "outcome"},
		"artifact.candidate": {"artifacts"},
		"agent.completed":    {"candidate_sdk_session_id", "artifacts"},
		"agent.failed":       {"code", "message", "retryable"},
	}
	if required, known := requiredFields[eventType]; known {
		fields, err := requireObjectFields(data, required...)
		if err != nil {
			return nil, err
		}
		if eventType == "tool.completed" {
			for _, optional := range []string{"output", "error"} {
				if raw, exists := fields[optional]; exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					return nil, fmt.Errorf("%s must not be null", optional)
				}
			}
		}
	}

	var payload any
	switch eventType {
	case "phase.changed":
		payload = &PhaseChangedPayload{}
	case "assistant.delta":
		payload = &AssistantDeltaPayload{}
	case "assistant.message":
		payload = &AssistantMessagePayload{}
	case "tool.started":
		payload = &ToolStartedPayload{}
	case "tool.completed":
		payload = &ToolCompletedPayload{}
	case "artifact.candidate":
		payload = &ArtifactCandidatePayload{}
	case "agent.completed":
		payload = &AgentCompletedPayload{}
	case "agent.failed":
		payload = &AgentFailedPayload{}
	default:
		var generic map[string]any
		if err := decodeStrict(data, &generic); err != nil {
			return nil, err
		}
		if generic == nil {
			return nil, errors.New("payload must be an object")
		}
		return generic, nil
	}

	if err := decodeStrict(data, payload); err != nil {
		return nil, err
	}
	switch value := payload.(type) {
	case *PhaseChangedPayload:
		if value.Phase != "preparing" && value.Phase != "agent" {
			return nil, errors.New("phase must be preparing or agent")
		}
		return *value, nil
	case *AssistantDeltaPayload:
		return *value, nil
	case *AssistantMessagePayload:
		return *value, nil
	case *ToolStartedPayload:
		if value.Input == nil {
			return nil, errors.New("input is required")
		}
		return *value, nil
	case *ToolCompletedPayload:
		if value.Outcome != "succeeded" && value.Outcome != "failed" {
			return nil, errors.New("outcome must be succeeded or failed")
		}
		if value.Outcome == "failed" && value.Error == nil {
			return nil, errors.New("error is required for failed outcome")
		}
		return *value, nil
	case *ArtifactCandidatePayload:
		if value.Artifacts == nil {
			return nil, errors.New("artifacts is required")
		}
		if err := validateArtifacts(value.Artifacts); err != nil {
			return nil, err
		}
		return *value, nil
	case *AgentCompletedPayload:
		if strings.TrimSpace(value.CandidateSDKSessionID) == "" {
			return nil, errors.New("candidate_sdk_session_id must not be empty")
		}
		if value.Artifacts == nil {
			return nil, errors.New("artifacts is required")
		}
		if err := validateArtifacts(value.Artifacts); err != nil {
			return nil, err
		}
		return *value, nil
	case *AgentFailedPayload:
		if strings.TrimSpace(value.Code) == "" || strings.TrimSpace(value.Message) == "" {
			return nil, errors.New("code and message must not be empty")
		}
		return *value, nil
	default:
		return nil, errors.New("unhandled known event payload")
	}
}

func IsTerminalEvent(event RuntimeEvent) bool {
	switch event.Type {
	case "agent.completed":
		_, ok := event.Payload.(AgentCompletedPayload)
		return ok
	case "agent.failed":
		_, ok := event.Payload.(AgentFailedPayload)
		return ok
	default:
		return false
	}
}

func ValidateRuntimeEventSequence(events []RuntimeEvent) error {
	var lastCandidate []ArtifactCandidate
	sawCandidate := false
	for _, event := range events {
		switch payload := event.Payload.(type) {
		case ArtifactCandidatePayload:
			lastCandidate = payload.Artifacts
			sawCandidate = true
		case AgentCompletedPayload:
			if len(payload.Artifacts) == 0 && !sawCandidate {
				continue
			}
			if !sawCandidate || !reflect.DeepEqual(lastCandidate, payload.Artifacts) {
				return errors.New("agent.completed artifacts must match the last artifact.candidate")
			}
		}
	}
	return nil
}

type ArtifactManifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Artifacts     []ArtifactCandidate `json:"artifacts"`
}

func ParseArtifactManifest(data []byte) (ArtifactManifest, error) {
	var manifest ArtifactManifest
	if err := decodeStrict(data, &manifest); err != nil {
		return ArtifactManifest{}, fmt.Errorf("decode artifact manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 {
		return ArtifactManifest{}, fmt.Errorf("unsupported artifact manifest schema_version %d", manifest.SchemaVersion)
	}
	if manifest.Artifacts == nil {
		return ArtifactManifest{}, errors.New("artifacts is required")
	}
	if err := validateArtifacts(manifest.Artifacts); err != nil {
		return ArtifactManifest{}, err
	}
	names := make(map[string]struct{}, len(manifest.Artifacts))
	primaryCount := 0
	for _, artifact := range manifest.Artifacts {
		if _, exists := names[artifact.Name]; exists {
			return ArtifactManifest{}, fmt.Errorf("artifact name %q is not unique", artifact.Name)
		}
		names[artifact.Name] = struct{}{}
		if artifact.Primary {
			primaryCount++
		}
	}
	if primaryCount > 1 {
		return ArtifactManifest{}, errors.New("artifact manifest may have at most one primary artifact")
	}
	return manifest, nil
}

func validateArtifacts(artifacts []ArtifactCandidate) error {
	for index, artifact := range artifacts {
		if strings.TrimSpace(artifact.Name) == "" || strings.TrimSpace(artifact.Title) == "" {
			return fmt.Errorf("artifact %d name and title must not be empty", index)
		}
		switch artifact.Type {
		case "html", "markdown", "image", "data":
		default:
			return fmt.Errorf("artifact %d has unsupported type %q", index, artifact.Type)
		}
		if !isSafeRelativePath(artifact.Entry) {
			return fmt.Errorf("artifact %d entry must be a safe relative path", index)
		}
	}
	return nil
}

func isSafeRelativePath(value string) bool {
	if value == "" || path.IsAbs(value) || value == "." {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func requireObjectFields(data []byte, required ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("value must be an object")
	}
	for _, name := range required {
		raw, exists := fields[name]
		if !exists {
			return nil, fmt.Errorf("%s is required", name)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("%s must not be null", name)
		}
	}
	return fields, nil
}
