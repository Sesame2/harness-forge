package contracts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../.."))
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), "tests/fixtures/contracts", name))
	require.NoError(t, err)
	return data
}

func contractPath(t *testing.T, parts ...string) string {
	t.Helper()
	return filepath.Join(append([]string{repositoryRoot(t), "contracts"}, parts...)...)
}

func compileSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	schema, err := compiler.Compile(path)
	require.NoError(t, err)
	return schema
}

func decodeJSON(t *testing.T, data []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	require.NoError(t, decoder.Decode(&value))
	return value
}

func validateJSON(t *testing.T, schema *jsonschema.Schema, data []byte) {
	t.Helper()
	require.NoError(t, schema.Validate(decodeJSON(t, data)))
}

func loadRuntimeEvents(t *testing.T) []RuntimeEvent {
	t.Helper()
	schema := compileSchema(t, contractPath(t, "runtime/v1/runtime-event.schema.json"))
	scanner := bufio.NewScanner(bytes.NewReader(fixture(t, "runtime-events.ndjson")))
	var events []RuntimeEvent
	for scanner.Scan() {
		line := bytes.Clone(scanner.Bytes())
		validateJSON(t, schema, line)
		event, err := ParseRuntimeEvent(line)
		require.NoError(t, err)
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	return events
}

func TestRunRequestFixture(t *testing.T) {
	schema := compileSchema(t, contractPath(t, "runtime/v1/run-request.schema.json"))
	validateJSON(t, schema, fixture(t, "run-request.json"))
}

func TestRunRequestSchemaRejectsInvalidDocuments(t *testing.T) {
	schema := compileSchema(t, contractPath(t, "runtime/v1/run-request.schema.json"))
	valid := decodeJSON(t, fixture(t, "run-request.json")).(map[string]any)

	tests := map[string]func(map[string]any){
		"unknown top-level field": func(value map[string]any) { value["unexpected"] = true },
		"invalid run id":          func(value map[string]any) { value["run_id"] = "not-a-uuid" },
		"empty prompt":            func(value map[string]any) { value["prompt"] = "" },
		"relative container path": func(value map[string]any) {
			value["paths"].(map[string]any)["workspace"] = "workspace"
		},
		"missing limits": func(value map[string]any) { delete(value, "limits") },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			copyBytes, err := json.Marshal(valid)
			require.NoError(t, err)
			candidate := decodeJSON(t, copyBytes).(map[string]any)
			mutate(candidate)
			require.Error(t, schema.Validate(candidate))
		})
	}
}

func TestRuntimeEventFixture(t *testing.T) {
	events := loadRuntimeEvents(t)
	require.Len(t, events, 8)
	require.Equal(t, "1", events[0].Version)
	require.Equal(t, "phase.changed", events[0].Type)
	require.Equal(t, "agent.completed", events[len(events)-1].Type)

	expectedPayloadTypes := []any{
		PhaseChangedPayload{}, AssistantDeltaPayload{}, AssistantMessagePayload{},
		ToolStartedPayload{}, ToolCompletedPayload{}, ArtifactCandidatePayload{},
		AgentFailedPayload{}, AgentCompletedPayload{},
	}
	for index, expected := range expectedPayloadTypes {
		require.Equal(t, reflect.TypeOf(expected), reflect.TypeOf(events[index].Payload), events[index].Type)
	}
	require.NoError(t, ValidateRuntimeEventSequence(events))
}

func TestRuntimeEventUnknownTypeIsForwardCompatibleAndNonTerminal(t *testing.T) {
	unknown := []byte(`{"version":"1","run_id":"00000000-0000-0000-0000-000000000001","sequence":9,"type":"agent.paused","occurred_at":"2026-07-19T00:00:08Z","payload":{"reason":"approval"}}`)
	schema := compileSchema(t, contractPath(t, "runtime/v1/runtime-event.schema.json"))
	validateJSON(t, schema, unknown)
	event, err := ParseRuntimeEvent(unknown)
	require.NoError(t, err)
	require.IsType(t, map[string]any{}, event.Payload)
	require.False(t, IsTerminalEvent(event))

	events := loadRuntimeEvents(t)
	require.True(t, IsTerminalEvent(events[6]))
	require.True(t, IsTerminalEvent(events[7]))
}

func TestRuntimeEventSchemaAndParserRejectsInvalidPayloads(t *testing.T) {
	schema := compileSchema(t, contractPath(t, "runtime/v1/runtime-event.schema.json"))
	tests := map[string]string{
		"missing required":         `{"version":"1","run_id":"00000000-0000-0000-0000-000000000001","sequence":1,"type":"phase.changed","occurred_at":"2026-07-19T00:00:00Z","payload":{}}`,
		"wrong type":               `{"version":"1","run_id":"00000000-0000-0000-0000-000000000001","sequence":1,"type":"assistant.delta","occurred_at":"2026-07-19T00:00:00Z","payload":{"text":42}}`,
		"null required string":     `{"version":"1","run_id":"00000000-0000-0000-0000-000000000001","sequence":1,"type":"assistant.delta","occurred_at":"2026-07-19T00:00:00Z","payload":{"text":null}}`,
		"missing required bool":    `{"version":"1","run_id":"00000000-0000-0000-0000-000000000001","sequence":1,"type":"agent.failed","occurred_at":"2026-07-19T00:00:00Z","payload":{"code":"failed","message":"failed"}}`,
		"unknown artifact type":    `{"version":"1","run_id":"00000000-0000-0000-0000-000000000001","sequence":1,"type":"artifact.candidate","occurred_at":"2026-07-19T00:00:00Z","payload":{"artifacts":[{"name":"map","title":"Map","type":"video","entry":"map.mp4","primary":true}]}}`,
		"missing artifact primary": `{"version":"1","run_id":"00000000-0000-0000-0000-000000000001","sequence":1,"type":"artifact.candidate","occurred_at":"2026-07-19T00:00:00Z","payload":{"artifacts":[{"name":"map","title":"Map","type":"html","entry":"map.html"}]}}`,
		"failed without error":     `{"version":"1","run_id":"00000000-0000-0000-0000-000000000001","sequence":1,"type":"tool.completed","occurred_at":"2026-07-19T00:00:00Z","payload":{"tool_call_id":"call-1","name":"write","outcome":"failed"}}`,
	}

	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			data := []byte(document)
			require.Error(t, schema.Validate(decodeJSON(t, data)))
			_, err := ParseRuntimeEvent(data)
			require.Error(t, err)
		})
	}
}

func TestRuntimeEventSequenceRejectsInvalidArtifactFinalization(t *testing.T) {
	events := loadRuntimeEvents(t)
	completed := events[len(events)-1].Payload.(AgentCompletedPayload)
	completed.Artifacts[0].Entry = "different.html"
	events[len(events)-1].Payload = completed
	require.Error(t, ValidateRuntimeEventSequence(events))
}

func TestRuntimeEventSequenceRejectsCompletedWithoutArtifactCandidate(t *testing.T) {
	events := []RuntimeEvent{
		{
			Type: "agent.completed",
			Payload: AgentCompletedPayload{
				CandidateSDKSessionID: "sdk-session-2",
				Artifacts:             []ArtifactCandidate{},
			},
		},
	}
	require.Error(t, ValidateRuntimeEventSequence(events))
}

func TestArtifactManifestFixture(t *testing.T) {
	data := fixture(t, "artifact-manifest.json")
	schema := compileSchema(t, contractPath(t, "artifacts/v1/artifact-manifest.schema.json"))
	validateJSON(t, schema, data)
	manifest, err := ParseArtifactManifest(data)
	require.NoError(t, err)
	require.Len(t, manifest.Artifacts, 2)
	require.Equal(t, "report", manifest.Artifacts[0].Name)
}

func TestArtifactManifestRejectsInvalidDocuments(t *testing.T) {
	schema := compileSchema(t, contractPath(t, "artifacts/v1/artifact-manifest.schema.json"))
	schemaInvalid := []string{
		`{"schema_version":1,"artifacts":[{"name":"","title":"Map","type":"html","entry":"map.html","primary":true}]}`,
		`{"schema_version":1,"artifacts":[{"name":"map","title":"Map","type":"video","entry":"map.mp4","primary":true}]}`,
		`{"schema_version":1,"artifacts":[{"name":"map","title":"Map","type":"html","entry":"/map.html","primary":true}]}`,
		`{"schema_version":1,"artifacts":[{"name":"map","title":"Map","type":"html","entry":"../map.html","primary":true}]}`,
	}
	for _, document := range schemaInvalid {
		require.Error(t, schema.Validate(decodeJSON(t, []byte(document))))
	}

	semanticInvalid := []string{
		`{"schema_version":1,"artifacts":[{"name":"map","title":"Map","type":"html","entry":"map.html","primary":true},{"name":"map","title":"Data","type":"data","entry":"map.json","primary":false}]}`,
		`{"schema_version":1,"artifacts":[{"name":"map","title":"Map","type":"html","entry":"map.html","primary":true},{"name":"data","title":"Data","type":"data","entry":"map.json","primary":true}]}`,
	}
	for _, document := range semanticInvalid {
		_, err := ParseArtifactManifest([]byte(document))
		require.Error(t, err)
	}
}

type operationExpectation struct {
	method      string
	path        string
	statuses    []string
	successType string
	successRef  string
	requestRef  string
}

func TestControlPlaneOpenAPI(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(contractPath(t, "control-plane.openapi.yaml"))
	require.NoError(t, err)
	require.Equal(t, "3.1.0", doc.OpenAPI)
	require.NoError(t, doc.Validate(context.Background()))

	expectations := []operationExpectation{
		{http.MethodGet, "/api/v1/projects", []string{"200"}, "array", "Project", ""},
		{http.MethodPost, "/api/v1/projects", []string{"201", "400"}, "object", "Project", "CreateProject"},
		{http.MethodGet, "/api/v1/projects/{project_id}", []string{"200", "404"}, "object", "Project", ""},
		{http.MethodPatch, "/api/v1/projects/{project_id}", []string{"200", "404", "409"}, "object", "Project", "RenameProject"},
		{http.MethodDelete, "/api/v1/projects/{project_id}", []string{"204", "404", "409"}, "", "", ""},
		{http.MethodGet, "/api/v1/projects/{project_id}/inputs", []string{"200", "404"}, "array", "InputFile", ""},
		{http.MethodPost, "/api/v1/projects/{project_id}/inputs", []string{"201", "400", "404", "409", "413"}, "object", "InputFile", "multipart"},
		{http.MethodGet, "/api/v1/projects/{project_id}/conversations", []string{"200", "404"}, "array", "Conversation", ""},
		{http.MethodPost, "/api/v1/projects/{project_id}/conversations", []string{"201", "400", "404", "409"}, "object", "Conversation", "CreateConversation"},
		{http.MethodGet, "/api/v1/conversations/{conversation_id}", []string{"200", "404"}, "object", "Conversation", ""},
		{http.MethodPatch, "/api/v1/conversations/{conversation_id}", []string{"200", "404", "409"}, "object", "Conversation", "RenameConversation"},
		{http.MethodDelete, "/api/v1/conversations/{conversation_id}", []string{"204", "404", "409"}, "", "", ""},
		{http.MethodGet, "/api/v1/conversations/{conversation_id}/messages", []string{"200", "404"}, "array", "Message", ""},
		{http.MethodPost, "/api/v1/conversations/{conversation_id}/messages", []string{"201", "400", "404", "409"}, "object", "SubmitMessageResult", "SubmitMessage"},
		{http.MethodGet, "/api/v1/conversations/{conversation_id}/runs", []string{"200", "404"}, "array", "Run", ""},
		{http.MethodGet, "/api/v1/runs/{run_id}", []string{"200", "404"}, "object", "Run", ""},
		{http.MethodGet, "/api/v1/runs/{run_id}/events", []string{"200", "400", "404"}, "array", "RunEvent", ""},
		{http.MethodGet, "/api/v1/runs/{run_id}/events/stream", []string{"200", "400", "404"}, "sse", "", ""},
		{http.MethodPost, "/api/v1/runs/{run_id}/cancel", []string{"202", "404", "409"}, "object", "Run", ""},
		{http.MethodGet, "/api/v1/runs/{run_id}/artifacts", []string{"200", "404"}, "array", "Artifact", ""},
	}

	for _, expected := range expectations {
		t.Run(expected.method+" "+expected.path, func(t *testing.T) {
			item := doc.Paths.Find(expected.path)
			require.NotNil(t, item)
			operation := item.GetOperation(expected.method)
			require.NotNil(t, operation)
			var actual []string
			for status := range operation.Responses.Map() {
				actual = append(actual, status)
			}
			sort.Strings(actual)
			sort.Strings(expected.statuses)
			require.Equal(t, expected.statuses, actual)

			if expected.requestRef != "" {
				require.NotNil(t, operation.RequestBody)
				if expected.requestRef == "multipart" {
					media := operation.RequestBody.Value.Content["multipart/form-data"]
					require.NotNil(t, media)
					require.Contains(t, media.Schema.Value.Required, "file")
				} else {
					media := operation.RequestBody.Value.Content["application/json"]
					require.Equal(t, "#/components/schemas/"+expected.requestRef, media.Schema.Ref)
				}
			}

			success := expected.statuses[0]
			response := operation.Responses.Value(success)
			require.NotNil(t, response)
			if expected.successType == "" {
				require.Empty(t, response.Value.Content)
			} else if expected.successType == "sse" {
				require.Contains(t, response.Value.Content, "text/event-stream")
			} else {
				media := response.Value.Content["application/json"]
				require.NotNil(t, media)
				if expected.successType == "array" {
					require.True(t, media.Schema.Value.Type.Is("array"))
					require.Equal(t, "#/components/schemas/"+expected.successRef, media.Schema.Value.Items.Ref)
				} else {
					require.Equal(t, "#/components/schemas/"+expected.successRef, media.Schema.Ref)
				}
			}

			for _, status := range expected.statuses[1:] {
				media := operation.Responses.Value(status).Value.Content["application/json"]
				require.NotNil(t, media, status)
				require.Equal(t, "#/components/schemas/Error", media.Schema.Ref, status)
			}
		})
	}

	runs := doc.Paths.Find("/api/v1/conversations/{conversation_id}/runs").Get
	require.Contains(t, strings.ToLower(runs.Description), "created_at")
	require.Contains(t, strings.ToLower(runs.Description), "ascending")
	events := doc.Paths.Find("/api/v1/runs/{run_id}/events").Get
	assertOptionalUint64Parameter(t, findParameter(events, "query", "after_sequence"))
	sse := doc.Paths.Find("/api/v1/runs/{run_id}/events/stream").Get
	require.Contains(t, sse.Description, "Last-Event-ID")
	require.Contains(t, strings.ToLower(sse.Description), "priority")
	require.Contains(t, strings.ToLower(sse.Description), "durable sequence")
	assertOptionalUint64Parameter(t, findParameter(sse, "query", "after_sequence"))
	lastEventID := findParameter(sse, "header", "Last-Event-ID")
	assertOptionalUint64Parameter(t, lastEventID)
}

func findParameter(operation *openapi3.Operation, in, name string) *openapi3.Parameter {
	for _, ref := range operation.Parameters {
		if ref.Value != nil && ref.Value.In == in && ref.Value.Name == name {
			return ref.Value
		}
	}
	return nil
}

func assertOptionalUint64Parameter(t *testing.T, parameter *openapi3.Parameter) {
	t.Helper()
	require.NotNil(t, parameter)
	require.False(t, parameter.Required)
	require.NotNil(t, parameter.Schema)
	require.True(t, parameter.Schema.Value.Type.Is("integer"))
	require.Equal(t, "uint64", parameter.Schema.Value.Format)
}

func TestOpenAPIComponentsAreClosedRequiredAndNullable(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile(contractPath(t, "control-plane.openapi.yaml"))
	require.NoError(t, err)

	expectedFields := map[string][]string{
		"Project":             {"id", "name", "profile_id", "profile_version", "accepted_input_media_types", "created_at", "updated_at"},
		"InputFile":           {"id", "project_id", "display_name", "media_type", "size_bytes", "sha256_digest", "created_at"},
		"Conversation":        {"id", "project_id", "title", "active_sdk_session_id", "created_at", "updated_at"},
		"Message":             {"id", "conversation_id", "role", "content", "created_at"},
		"Run":                 {"id", "conversation_id", "trigger_message_id", "status", "phase", "error", "source_sdk_session_id", "candidate_sdk_session_id", "finalized_at", "created_at", "updated_at"},
		"RunEvent":            {"run_id", "sequence", "type", "payload", "occurred_at"},
		"Artifact":            {"id", "run_id", "title", "type", "entry_path", "is_primary", "gateway_url", "created_at"},
		"SubmitMessageResult": {"message", "run"},
		"Error":               {"code", "message", "details", "request_id"},
		"CreateProject":       {"name", "profile_id"},
		"RenameProject":       {"name"},
		"CreateConversation":  {"title"},
		"RenameConversation":  {"title"},
		"SubmitMessage":       {"content"},
	}

	for name, fields := range expectedFields {
		t.Run(name, func(t *testing.T) {
			schema := doc.Components.Schemas[name].Value
			require.NotNil(t, schema)
			require.NotNil(t, schema.AdditionalProperties.Has)
			require.False(t, *schema.AdditionalProperties.Has)
			actualProperties := make([]string, 0, len(schema.Properties))
			for property := range schema.Properties {
				actualProperties = append(actualProperties, property)
			}
			sort.Strings(actualProperties)
			sort.Strings(fields)
			require.Equal(t, fields, actualProperties)
			required := fields
			if name == "CreateConversation" {
				required = nil
			}
			require.ElementsMatch(t, required, schema.Required)
		})
	}

	stringNullable := []struct{ component, property string }{
		{"Conversation", "active_sdk_session_id"},
		{"Run", "source_sdk_session_id"},
		{"Run", "candidate_sdk_session_id"},
		{"Run", "finalized_at"},
	}
	for _, item := range stringNullable {
		types := doc.Components.Schemas[item.component].Value.Properties[item.property].Value.Type.Slice()
		require.Contains(t, types, "null", item.component+"."+item.property)
	}
	require.Contains(t, doc.Components.Schemas["Run"].Value.Properties["phase"].Value.Type.Slice(), "null")
	require.Contains(t, doc.Components.Schemas["Error"].Value.Properties["details"].Value.Type.Slice(), "null")

	runError := doc.Components.Schemas["Run"].Value.Properties["error"].Value
	require.Len(t, runError.AnyOf, 2)
	require.Equal(t, "#/components/schemas/Error", runError.AnyOf[0].Ref)
	require.NotNil(t, runError.AnyOf[1].Value)
	require.True(t, runError.AnyOf[1].Value.Type.Is("null"))

	result := doc.Components.Schemas["SubmitMessageResult"].Value
	require.Equal(t, "#/components/schemas/Message", result.Properties["message"].Ref)
	require.Equal(t, "#/components/schemas/Run", result.Properties["run"].Ref)

	for _, request := range []string{"CreateProject", "RenameProject", "RenameConversation", "SubmitMessage"} {
		for property, ref := range doc.Components.Schemas[request].Value.Properties {
			require.GreaterOrEqual(t, ref.Value.MinLength, uint64(1), request+"."+property)
			require.Equal(t, `.*\S.*`, ref.Value.Pattern, request+"."+property)
		}
	}
}
