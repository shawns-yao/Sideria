package main

import (
	"context"
	"encoding/json"
	"github.com/shawns-yao/Sideria/internal/agent"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	state := os.Getenv("SIDERIA_AGENT_STATE")
	if state == "" {
		state = ".run/agent"
	}
	if os.MkdirAll(state, 0700) != nil {
		slog.Error("state directory unavailable")
		os.Exit(1)
	}
	credPath := filepath.Join(state, "identity.json")
	var cred struct {
		HostID string `json:"host_id"`
		Token  string `json:"token"`
	}
	b, err := os.ReadFile(credPath)
	if err == nil {
		if _, e := os.Stat(filepath.Join(state, "journal.db")); e != nil {
			slog.Error("execution journal missing; stop for explicit recovery, do not replay tasks")
			os.Exit(1)
		}
		if json.Unmarshal(b, &cred) != nil {
			slog.Error("identity corrupt; explicit re-enrollment required")
			os.Exit(1)
		}
	} else if os.IsNotExist(err) {
		token := os.Getenv("SIDERIA_ENROLLMENT_TOKEN")
		if token == "" {
			slog.Error("enrollment token required for new identity")
			os.Exit(1)
		}
		cred.HostID, cred.Token, err = agent.Enroll(ctx, os.Getenv("SIDERIA_SERVER_URL"), token, os.Getenv("SIDERIA_DEV") == "1")
		if err != nil {
			slog.Error("enrollment failed")
			os.Exit(1)
		}
		b, _ = json.Marshal(cred)
		if os.WriteFile(credPath, b, 0600) != nil {
			slog.Error("identity persistence failed")
			os.Exit(1)
		}
	} else {
		slog.Error("identity unavailable")
		os.Exit(1)
	}
	interval := 5 * time.Second
	if v := os.Getenv("SIDERIA_SAMPLE_INTERVAL"); v != "" {
		interval, err = time.ParseDuration(v)
		if err != nil || interval < time.Second || interval > time.Minute {
			slog.Error("sample interval must be 1s to 1m")
			os.Exit(1)
		}
	}
	a, err := agent.New(agent.Config{URL: os.Getenv("SIDERIA_SERVER_URL"), HostID: cred.HostID, Token: cred.Token, Root: os.Getenv("SIDERIA_FILE_ROOT"), JournalPath: filepath.Join(state, "journal.db"), DockerSocket: os.Getenv("SIDERIA_DOCKER_SOCKET"), Terminal: os.Getenv("SIDERIA_ALLOW_TERMINAL") == "1", Dev: os.Getenv("SIDERIA_DEV") == "1", Interval: interval})
	if err != nil {
		slog.Error("agent initialization failed", "error", err)
		os.Exit(1)
	}
	defer a.Close()
	slog.Info("agent starting", "host_id", cred.HostID)
	a.Run(ctx)
}
