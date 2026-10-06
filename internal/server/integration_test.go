package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/shawns-yao/Sideria/internal/agent"
	"github.com/shawns-yao/Sideria/internal/protocol"
)

func TestIsolatedWorkflow(t *testing.T) {
	database := os.Getenv("SIDERIA_TEST_DATABASE")
	redisURL := os.Getenv("SIDERIA_TEST_REDIS")
	if database == "" || redisURL == "" {
		t.Skip("isolated PostgreSQL and Redis URLs required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := OpenStore(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Pool.Close()
	if _, err = store.Pool.Exec(ctx, `TRUNCATE hosts,tasks,projects,audit,sessions,analyses RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	opts, _ := redis.ParseURL(redisURL)
	redisClient := redis.NewClient(opts)
	defer redisClient.Close()
	if err = redisClient.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	admin := strings.Repeat("a", 48)
	s := New(store, redisClient, Config{AdminToken: admin, Dev: true, TransferDir: t.TempDir()})
	httpServer := httptest.NewServer(s.Handler(nil))
	defer httpServer.Close()
	go s.Run(ctx)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 25 * time.Second}
	call := func(method, path string, body any, key string) (int, []byte) {
		t.Helper()
		var reader io.Reader
		if b, ok := body.([]byte); ok {
			reader = bytes.NewReader(b)
		} else if body != nil {
			reader = bytes.NewReader(protocol.Raw(body))
		}
		req, _ := http.NewRequest(method, httpServer.URL+path, reader)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	if status, _ := call("GET", "/api/hosts", nil, ""); status != 401 {
		t.Fatal("unauthenticated API", status)
	}
	if status, b := call("POST", "/api/login", map[string]string{"token": admin}, ""); status != 200 {
		t.Fatalf("login %d %s", status, b)
	}
	type node struct {
		id, token, root string
		a               *agent.Agent
		stop            context.CancelFunc
		done            chan struct{}
	}
	nodes := []node{}
	for _, name := range []string{"test-alpha", "test-beta"} {
		status, b := call("POST", "/api/hosts", map[string]string{"name": name}, "")
		if status != 201 {
			t.Fatalf("host %d %s", status, b)
		}
		var registration map[string]string
		json.Unmarshal(b, &registration)
		id, token, e := agent.Enroll(ctx, httpServer.URL, registration["enrollment_token"], true)
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = agent.Enroll(ctx, httpServer.URL, registration["enrollment_token"], true); e == nil {
			t.Fatal("enrollment token reused")
		}
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "hello.txt"), []byte(name), 0600)
		a, e := agent.New(agent.Config{URL: httpServer.URL, HostID: id, Token: token, Dev: true, Root: root, JournalPath: filepath.Join(t.TempDir(), "journal.db"), Terminal: true, Interval: time.Second, DockerSocket: os.Getenv("SIDERIA_TEST_DOCKER_SOCKET")})
		if e != nil {
			t.Fatal(e)
		}
		ac, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); a.Run(ac) }()
		nodes = append(nodes, node{id, token, root, a, stop, done})
	}
	defer func() {
		for _, n := range nodes {
			n.stop()
			<-n.done
			n.a.Close()
		}
	}()
	wait := func(name string, fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			if fn() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("timeout:", name)
	}
	wait("two online agents", func() bool {
		_, b := call("GET", "/api/hosts", nil, "")
		var hosts []protocol.Host
		json.Unmarshal(b, &hosts)
		return len(hosts) == 2 && hosts[0].Online && hosts[1].Online && hosts[0].Snapshot != nil && hosts[1].Snapshot != nil
	})
	t.Run("identity_and_query", func(t *testing.T) {
		n := nodes[0]
		ws, resp, e := websocket.DefaultDialer.Dial(strings.Replace(httpServer.URL, "http", "ws", 1)+"/agent/connect", http.Header{"Authorization": []string{"Bearer " + n.token}})
		if ws != nil {
			ws.Close()
		}
		if e == nil || resp.StatusCode != 409 {
			t.Fatal("duplicate identity accepted")
		}
		for _, n := range nodes {
			status, b := call("POST", "/api/hosts/"+n.id+"/query", map[string]any{"action": "file.read", "params": protocol.Args{Path: "hello.txt"}}, "")
			if status != 200 || !bytes.Contains(b, []byte(filepath.Base(n.root))) && !bytes.Contains(b, []byte("test-")) {
				t.Fatalf("read %d %s", status, b)
			}
		}
		status, _ := call("POST", "/api/hosts/"+n.id+"/query", map[string]any{"action": "file.read", "params": protocol.Args{Path: "../secret"}}, "")
		if status != 400 {
			t.Fatal("path escape", status)
		}
		status, _ = call("POST", "/api/hosts/"+n.id+"/query", map[string]any{"action": "docker.restart", "params": protocol.Args{Container: "abcdefabcdef"}}, "")
		if status != 403 {
			t.Fatal("query write", status)
		}
	})
	t.Run("download_idempotency_and_recovery", func(t *testing.T) {
		n := nodes[0]
		body := map[string]any{"action": "file.download", "params": protocol.Args{Path: "hello.txt"}, "confirm": true}
		status, b := call("POST", "/api/hosts/"+n.id+"/tasks", body, "download-key-1")
		if status != 202 {
			t.Fatalf("task %d %s", status, b)
		}
		var task protocol.Task
		json.Unmarshal(b, &task)
		_, b = call("POST", "/api/hosts/"+n.id+"/tasks", body, "download-key-1")
		var repeat protocol.Task
		json.Unmarshal(b, &repeat)
		if repeat.ID != task.ID || repeat.AttemptID != task.AttemptID {
			t.Fatal("duplicate acceptance")
		}
		body["params"] = protocol.Args{Path: "other.txt"}
		status, _ = call("POST", "/api/hosts/"+n.id+"/tasks", body, "download-key-1")
		if status != 409 {
			t.Fatal("idempotency conflict", status)
		}
		wait("download", func() bool { current, _ := store.Task(ctx, task.ID); return current.State == "succeeded" })
		status, b = call("GET", "/api/tasks/"+task.ID+"/download", nil, "")
		if status != 200 || string(b) != "test-alpha" {
			t.Fatalf("download %d %s", status, b)
		}
		// Replay an already completed attempt as if its acknowledgment was lost.
		s.mu.Lock()
		p := s.peers[n.id]
		s.mu.Unlock()
		if e := p.Send(task.Message()); e != nil {
			t.Fatal(e)
		}
		time.Sleep(100 * time.Millisecond)
		current, _ := store.Task(ctx, task.ID)
		if current.State != "succeeded" || current.AttemptID != task.AttemptID {
			t.Fatal("replay lost durable result")
		}
		// Replace the in-memory connection registry, simulating a center restart with durable DB facts.
		s.mu.Lock()
		p.ws.Close()
		s.mu.Unlock()
		wait("agent reconnect", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.peers[n.id] != nil && s.peers[n.id] != p })
		current, _ = store.Task(ctx, task.ID)
		if current.State != "succeeded" {
			t.Fatal("reconnect lost task")
		}
	})
	t.Run("upload_no_overwrite", func(t *testing.T) {
		n := nodes[0]
		req, _ := http.NewRequest("POST", httpServer.URL+"/api/hosts/"+n.id+"/uploads?path=new.txt", strings.NewReader("content transfer"))
		req.Header.Set("Idempotency-Key", "upload-key-1")
		req.Header.Set("X-Confirm-Target", n.id)
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		var task protocol.Task
		json.NewDecoder(res.Body).Decode(&task)
		res.Body.Close()
		if res.StatusCode != 202 {
			t.Fatal("upload", res.StatusCode)
		}
		wait("upload", func() bool {
			current, _ := store.Task(ctx, task.ID)
			if current.State == "failed" {
				t.Fatalf("upload failed: %s", current.Error)
			}
			return current.State == "succeeded"
		})
		b, e := os.ReadFile(filepath.Join(n.root, "new.txt"))
		if e != nil || string(b) != "content transfer" {
			t.Fatal("upload content", string(b), e)
		}
	})
	t.Run("pty_target_and_input", func(t *testing.T) {
		n := nodes[0]
		req, _ := http.NewRequest("GET", httpServer.URL, nil)
		cookies := ""
		for _, c := range client.Jar.Cookies(req.URL) {
			cookies += c.Name + "=" + c.Value + "; "
		}
		ws, _, e := websocket.DefaultDialer.Dial(strings.Replace(httpServer.URL, "http", "ws", 1)+"/api/hosts/"+n.id+"/terminal?confirm=shell", http.Header{"Cookie": []string{cookies}})
		if e != nil {
			t.Fatal(e)
		}
		defer ws.Close()
		ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		ws.WriteJSON(protocol.Message{Type: "terminal_resize", Data: protocol.Raw(map[string]int{"cols": 100, "rows": 30})})
		ws.WriteJSON(protocol.Message{Type: "terminal_input", Data: protocol.Raw(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte("printf '%s%s\\n' PTY_EXEC_ CONFIRMED\n"))})})
		var output string
		for !strings.Contains(output, "PTY_EXEC_CONFIRMED") {
			var m protocol.Message
			if e = ws.ReadJSON(&m); e != nil {
				t.Fatal(e, output)
			}
			var data map[string]string
			json.Unmarshal(m.Data, &data)
			b, _ := base64.StdEncoding.DecodeString(data["bytes"])
			output += string(b)
		}
		t.Log("PTY returned expected bytes")
	})
	t.Run("git_project", func(t *testing.T) {
		n := nodes[0]
		cmd := exec.Command("git", "init", "-q", "-b", "main", n.root)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatal(string(out), e)
		}
		status, b := call("POST", "/api/hosts/"+n.id+"/projects", map[string]string{"name": "fixture", "path": "."}, "")
		if status != 201 {
			t.Fatalf("project %d %s", status, b)
		}
	})
	t.Run("ai_disabled", func(t *testing.T) {
		status, _ := call("POST", "/api/analyses", map[string]any{"hosts": []string{nodes[0].id}, "prompt": "analyze", "mode": "Suggest"}, "")
		if status != 503 {
			t.Fatal("AI falsely available")
		}
	})
	t.Run("redis_failure_closed", func(t *testing.T) {
		bad := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
		old := s.Redis
		s.Redis = bad
		status, _ := call("POST", "/api/hosts", map[string]string{"name": "denied"}, "")
		s.Redis = old
		bad.Close()
		if status != 503 {
			t.Fatal("Redis failure accepted registration", status)
		}
	})

	t.Run("identity_rebind_fences_old_dispatch", func(t *testing.T) {
		n := nodes[1]
		n.stop()
		<-n.done
		wait("old agent stopped", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.peers[n.id] == nil })
		old, _, err := store.CreateTask(ctx, n.id, "before-rebind-key", "file.download", protocol.Raw(protocol.Args{Path: "hello.txt"}))
		if err != nil {
			t.Fatal(err)
		}
		status, b := call("POST", "/api/hosts/"+n.id+"/reenroll", map[string]bool{"confirm": true}, "")
		if status != 201 {
			t.Fatalf("rebind %d %s", status, b)
		}
		var registration map[string]string
		json.Unmarshal(b, &registration)
		id, token, err := agent.Enroll(ctx, httpServer.URL, registration["enrollment_token"], true)
		if err != nil || id != n.id {
			t.Fatal("stable identity not preserved", err)
		}
		if _, err = store.HostToken(ctx, n.token); err == nil {
			t.Fatal("replaced credential valid")
		}
		old, _ = store.Task(ctx, old.ID)
		if old.State != "uncertain" {
			t.Fatal("old task falsely resolved", old.State)
		}
		active, err := store.Tasks(ctx, n.id, true)
		if err != nil || len(active) != 0 {
			t.Fatal("old generation remained dispatchable", active, err)
		}
		a, err := agent.New(agent.Config{URL: httpServer.URL, HostID: id, Token: token, Dev: true, Root: n.root, JournalPath: filepath.Join(t.TempDir(), "fresh-journal"), Interval: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		ac, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); a.Run(ac) }()
		nodes = append(nodes, node{id, token, n.root, a, stop, done})
		wait("replacement connected", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.peers[id] != nil })
		fresh, _, err := store.CreateTask(ctx, id, "after-rebind-key", "file.download", protocol.Raw(protocol.Args{Path: "hello.txt"}))
		if err != nil {
			t.Fatal(err)
		}
		wait("new identity task", func() bool { task, _ := store.Task(ctx, fresh.ID); return task.State == "succeeded" })
	})
	t.Run("revoke_disconnect", func(t *testing.T) {
		n := nodes[1]
		status, _ := call("POST", "/api/hosts/"+n.id+"/revoke", map[string]any{}, "")
		if status != 200 {
			t.Fatal(status)
		}
		wait("revoked connection closed", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.peers[n.id] == nil })
		if _, e := store.HostToken(ctx, n.token); e == nil {
			t.Fatal("revoked credential still accepted")
		}
	})
}
