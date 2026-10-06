package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/shawns-yao/Sideria/internal/ai"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"net/http"
	"time"
)

func (s *Server) analyze(w http.ResponseWriter, r *http.Request) {
	if !s.Config.AIAllow || s.Config.AIKey == "" {
		fail(w, 503, "AI 未配置：需要提供方、模型凭据与数据外发授权；分析未执行")
		return
	}
	if !s.limited(w, r, "ai", 10) {
		return
	}
	select {
	case s.analysisSlots <- struct{}{}:
		defer func() { <-s.analysisSlots }()
	default:
		fail(w, 429, "analysis concurrency limit reached")
		return
	}
	var b struct {
		Prompt string   `json:"prompt"`
		Hosts  []string `json:"hosts"`
		Mode   string   `json:"mode"`
	}
	if !decode(w, r, &b) {
		return
	}
	if len(b.Prompt) == 0 || len(b.Prompt) > 4000 || len(b.Hosts) == 0 || len(b.Hosts) > 10 || (b.Mode != "Observe" && b.Mode != "Suggest") {
		fail(w, 400, "invalid analysis scope or mode")
		return
	}
	scope := map[string]bool{}
	for _, h := range b.Hosts {
		var ok bool
		if s.Store.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM hosts WHERE id=$1 AND NOT revoked)`, h).Scan(&ok) != nil || !ok {
			fail(w, 403, "host outside authorized scope")
			return
		}
		scope[h] = true
	}
	id := protocol.ID()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	save := func(result ai.Result) error {
		_, err := s.Store.Pool.Exec(ctx, `INSERT INTO analyses(id,body) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET body=excluded.body`, id, protocol.Raw(map[string]any{"id": id, "hosts": b.Hosts, "mode": b.Mode, "goal": ai.Redact(b.Prompt), "result": result}))
		return err
	}
	if save(ai.Result{State: "running", Model: s.Config.AIModel}) != nil {
		fail(w, 503, "analysis persistence failed")
		return
	}
	observe := func(ctx context.Context, host, action, resource string) (json.RawMessage, error) {
		if !scope[host] {
			return nil, errors.New("target outside analysis scope")
		}
		var allowed bool
		if s.Store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hosts WHERE id=$1 AND NOT revoked)`, host).Scan(&allowed) != nil || !allowed {
			return nil, errors.New("host authorization revoked")
		}
		if action == "project.list" {
			rows, err := s.Store.Pool.Query(ctx, `SELECT id,name FROM projects WHERE host_id=$1 ORDER BY name LIMIT 100`, host)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			projects := []map[string]string{}
			for rows.Next() {
				var id, name string
				if err = rows.Scan(&id, &name); err != nil {
					return nil, err
				}
				projects = append(projects, map[string]string{"id": id, "name": name})
			}
			return protocol.Raw(projects), rows.Err()
		}
		if action == "host.metrics" {
			var snap []byte
			var last *time.Time
			if err := s.Store.Pool.QueryRow(ctx, `SELECT snapshot,last_seen FROM hosts WHERE id=$1`, host).Scan(&snap, &last); err != nil {
				return nil, err
			}
			s.mu.Lock()
			online := s.peers[host] != nil
			history := append([]protocol.Snapshot{}, s.history[host]...)
			s.mu.Unlock()
			total := len(history)
			if total > 12 {
				history = history[total-12:]
			}
			return protocol.Raw(map[string]any{"window_sample_count": total, "host_id": host, "snapshot": json.RawMessage(snap), "last_seen": last, "online": online, "history": history}), nil
		}
		def, ok := protocol.Actions[action]
		if !ok || !def.AI || def.Write {
			return nil, errors.New("AI read action denied")
		}
		args := protocol.Args{}
		if action == "git.status" {
			if err := s.Store.Pool.QueryRow(ctx, `SELECT path FROM projects WHERE id=$1 AND host_id=$2`, resource, host).Scan(&args.Path); err != nil {
				return nil, errors.New("registered project required")
			}
		} else {
			args.Container = resource
		}
		m, err := s.request(ctx, host, action, protocol.Raw(args))
		if err != nil {
			return nil, err
		}
		if m.Error != "" {
			return nil, errors.New(m.Error)
		}
		return protocol.Raw(map[string]any{"request_id": m.ID, "host_id": host, "observed_at": time.Now().UTC(), "result": m.Data}), nil
	}
	client := ai.Client{URL: s.Config.AIURL, Key: s.Config.AIKey, Model: s.Config.AIModel}
	prompt := b.Prompt + "\nMode: " + b.Mode + "\nAuthorized hosts: " + string(protocol.Raw(b.Hosts)) + "\nAvailable reads: host.metrics, project.list, docker.list, docker.inspect, docker.stats, docker.logs, git.status (resource is registered project ID)."
	result := client.Analyze(ctx, prompt, observe, save)
	// Persist a canceled/failed analysis using an independent bounded context.
	finalCtx, done := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer done()
	_, err := s.Store.Pool.Exec(finalCtx, `UPDATE analyses SET body=$2 WHERE id=$1`, id, protocol.Raw(map[string]any{"id": id, "hosts": b.Hosts, "mode": b.Mode, "goal": ai.Redact(b.Prompt), "result": result}))
	if err != nil {
		fail(w, 503, "final analysis persistence failed")
		return
	}
	reply(w, 200, map[string]any{"id": id, "hosts": b.Hosts, "mode": b.Mode, "goal": ai.Redact(b.Prompt), "result": result})
}
func (s *Server) analyses(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT body FROM analyses ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		fail(w, 503, "database unavailable")
		return
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if rows.Scan(&b) != nil {
			fail(w, 500, "analysis read failed")
			return
		}
		out = append(out, b)
	}
	reply(w, 200, out)
}
