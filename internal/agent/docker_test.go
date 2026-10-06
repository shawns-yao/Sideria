package agent

import (
	"context"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDockerRealLifecycle(t *testing.T) {
	socket := os.Getenv("SIDERIA_TEST_DOCKER_SOCKET")
	if socket == "" {
		t.Skip("isolated Docker socket not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "-d", "--name", "sideria-test-"+protocol.ID()[:12], "alpine:3.22", "sh", "-c", "echo SIDERIA_CONTAINER_PROBE; sleep 120")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	id := strings.TrimSpace(string(out))
	defer exec.Command("docker", "rm", "-f", id).Run()
	a := testAgent(t)
	a.Config.DockerSocket = socket
	for _, action := range []string{"docker.list", "docker.inspect", "docker.logs", "docker.stats", "docker.stop", "docker.start", "docker.restart"} {
		args := protocol.Args{Container: id}
		if action == "docker.list" {
			args.Container = ""
		}
		v, e := a.Execute(ctx, action, protocol.Raw(args))
		if e != nil {
			t.Fatalf("%s: %v", action, e)
		}
		if action == "docker.logs" && !strings.Contains(v.(map[string]any)["logs"].(string), "SIDERIA_CONTAINER_PROBE") {
			t.Fatal(v)
		}
	}
	out, err = exec.Command("docker", "inspect", "-f", "{{.State.Running}}", id).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Fatal("restart not reflected in actual Docker state", string(out), err)
	}
}
