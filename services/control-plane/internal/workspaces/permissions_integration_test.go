//go:build integration

package workspaces

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This opt-in test inspects the actual running Compose containers; unit tests
// checking mode bits alone cannot prove the two service identities agree.
func TestComposeWorkspacePermissions(t *testing.T) {
	if os.Getenv("HF_PERMISSIONS_INTEGRATION") != "1" {
		t.Skip("set HF_PERMISSIONS_INTEGRATION=1 to verify actual Compose containers")
	}
	project := os.Getenv("HF_COMPOSE_PROJECT")
	if project == "" {
		t.Fatal("HF_COMPOSE_PROJECT is required")
	}
	compose, err := filepath.Abs("../../../../docker-compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	run := func(service, script string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", "compose", "--env-file", "/dev/null", "-p", project, "-f", compose, "exec", "-T", service, "sh", "-ec", script)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s permission proof: %v\n%s", service, err, output)
		}
	}
	for _, service := range []string{"control-plane", "agent-runtime"} {
		run(service, `test "$(id -u):$(id -g)" = '10001:10001'`)
	}
	name := "permission-proof-" + uuid.NewString()
	root := "/workspaces/" + name
	run("control-plane", `mkdir -m 0770 `+root+`; mkdir -m 0770 `+root+`/inputs `+root+`/workspace `+root+`/outputs; echo input > `+root+`/inputs/data; chmod 0440 `+root+`/inputs/data; chmod 0550 `+root+`/inputs; echo control > `+root+`/workspace/control; echo control > `+root+`/outputs/control`)
	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose", "--env-file", "/dev/null", "-p", project, "-f", compose, "exec", "-T", "control-plane", "sh", "-ec", `chmod 0770 `+root+`/inputs; rm -r `+root)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("remove permission fixture: %v %s", err, out)
		}
	})
	run("agent-runtime", `test "$(cat `+root+`/inputs/data)" = input; test "$(cat `+root+`/workspace/control)" = control; echo runtime > `+root+`/workspace/runtime; echo runtime > `+root+`/outputs/runtime; if (echo forbidden > `+root+`/inputs/new) 2>/dev/null; then exit 1; fi; if (echo forbidden >> `+root+`/inputs/data) 2>/dev/null; then exit 1; fi; touch /sessions/executions/`+name+` /sessions/claude/`+name+`; rm /sessions/executions/`+name+` /sessions/claude/`+name)
	run("control-plane", `test "$(cat `+root+`/inputs/data)" = input; test "$(cat `+root+`/workspace/runtime)" = runtime; test "$(cat `+root+`/outputs/runtime)" = runtime`)
}
