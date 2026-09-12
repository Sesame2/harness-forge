package runs

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTerminalEventRequiresMatchingFinalizedRun(t *testing.T) {
	now := time.Now()
	for _, status := range []Status{Succeeded, Failed, Cancelled, Interrupted} {
		event := Event{Type: "run." + string(status), Payload: json.RawMessage(`{}`), OccurredAt: now}
		if err := validateEvent(Run{Status: status}, event); err == nil {
			t.Fatalf("accepted unfinalized %s", status)
		}
		if err := validateEvent(Run{Status: status, FinalizedAt: &now}, event); err != nil {
			t.Fatal(err)
		}
		if err := validateEvent(Run{Status: Queued, FinalizedAt: &now}, event); err == nil {
			t.Fatal("accepted wrong terminal status")
		}
	}
	if err := validateEvent(Run{Status: Running}, Event{Type: "assistant.delta", Payload: json.RawMessage(`{"text":"a"}`), OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []Event{
		{Type: "", Payload: json.RawMessage(`{}`), OccurredAt: now},
		{Type: "assistant.delta", Payload: json.RawMessage(`invalid`), OccurredAt: now},
		{Type: "assistant.delta", Payload: json.RawMessage(`null`), OccurredAt: now},
		{Type: "assistant.delta", Payload: json.RawMessage(`[]`), OccurredAt: now},
		{Type: "assistant.delta", Payload: json.RawMessage(`1`), OccurredAt: now},
		{Type: "assistant.delta", Payload: json.RawMessage(`{}`)},
	} {
		if err := validateEvent(Run{}, event); err == nil {
			t.Fatal("accepted invalid event")
		}
	}
}

func TestRunJSONHidesSandboxIdentity(t *testing.T) {
	provider, ref := "provider-secret", "ref-secret"
	encoded, err := json.Marshal(Run{SandboxProvider: &provider, SandboxRef: &ref})
	if err != nil || strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "sandbox") {
		t.Fatalf("JSON = %s, %v", encoded, err)
	}
}
