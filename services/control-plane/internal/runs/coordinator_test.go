package runs

import (
	"testing"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/agentexec"
	"harness-forge.local/control-plane/internal/profiles"
)

func TestBuildRequestUsesLeasePathsAndFullImmutablePolicy(t *testing.T) {
	source := "current"
	r := Run{ID: uuid.New(), ConversationID: uuid.New(), SourceSDKSessionID: &source}
	paths := agentexec.Paths{Inputs: "/remote/i", Workspace: "/remote/w", Outputs: "/remote/o"}
	snapshot := profiles.Snapshot{ID: "geo", Version: "1", Digest: "digest", SystemPrompt: "system", AllowedTools: []string{"Read"}, DisallowedTools: []string{"WebSearch"}, PermissionMode: "acceptEdits", Agent: profiles.AgentPolicy{MaxTurns: 5, MaxBudgetUSD: 2}}
	request := buildRequest(r, RunContext{ProjectID: uuid.New(), Prompt: "trigger"}, snapshot, paths)
	if request.Paths != paths || request.Prompt != "trigger" || request.SourceSDKSessionID == nil || string(*request.SourceSDKSessionID) != source {
		t.Fatal(request)
	}
	tools := request.Profile.Config["tools"].(map[string]any)
	tools["disallowed"].([]string)[0] = "mutated"
	if snapshot.DisallowedTools[0] != "WebSearch" {
		t.Fatal("mutable policy alias")
	}
	if request.Limits.MaxTurns != 5 || request.Profile.Config["system_prompt"] != "system" {
		t.Fatal(request)
	}
}
