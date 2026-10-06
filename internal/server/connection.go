package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shawns-yao/Sideria/internal/protocol"
)

var upgrade = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	id, err := s.Store.HostToken(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil {
		fail(w, 401, "agent authentication failed")
		return
	}
	s.mu.Lock()
	if s.peers[id] != nil {
		s.mu.Unlock()
		fail(w, 409, "identity already connected")
		return
	}
	ws, err := upgrade.Upgrade(w, r, nil)
	if err != nil {
		s.mu.Unlock()
		return
	}
	p := &peer{ws: ws}
	s.peers[id] = p
	s.mu.Unlock()
	defer func() {
		ws.Close()
		s.mu.Lock()
		if s.peers[id] == p {
			delete(s.peers, id)
		}
		for _, t := range s.terminals {
			if t.host == id {
				t.browser.Close()
			}
		}
		s.mu.Unlock()
	}()
	ws.SetReadLimit(protocol.MaxMessage)
	var lastSample, lastHeartbeat time.Time
	for {
		ws.SetReadDeadline(time.Now().Add(20 * time.Second))
		var m protocol.Message
		if ws.ReadJSON(&m) != nil {
			return
		}
		if m.Version != protocol.Version || m.HostID != id {
			return
		}
		switch m.Type {
		case "hello":
			var caps map[string]string
			if json.Unmarshal(m.Data, &caps) != nil || len(caps) > 16 {
				return
			}
			if _, err = s.Store.Pool.Exec(r.Context(), `UPDATE hosts SET capabilities=$2,last_seen=now() WHERE id=$1 AND NOT revoked`, id, m.Data); err != nil {
				return
			}
		case "snapshot":
			if time.Since(lastSample) < 900*time.Millisecond {
				continue
			}
			lastSample = time.Now()
			var snap protocol.Snapshot
			if json.Unmarshal(m.Data, &snap) != nil {
				return
			}
			if len(snap.Disks) > 64 || len(snap.Networks) > 64 || len(snap.Errors) > 64 || len(snap.OS) > 256 || len(snap.Hostname) > 256 || (snap.CPU != nil && (*snap.CPU < 0 || *snap.CPU > 100)) || snap.MemoryAvailable > snap.MemoryTotal {
				return
			}
			snap.ReceivedAt = time.Now().UTC()
			if snap.SampledAt.After(time.Now().Add(time.Minute)) || snap.SampledAt.Before(time.Now().Add(-time.Hour)) {
				continue
			}
			if _, err = s.Store.Pool.Exec(r.Context(), `UPDATE hosts SET snapshot=$2,last_seen=now() WHERE id=$1 AND NOT revoked`, id, protocol.Raw(snap)); err != nil {
				return
			}
			s.mu.Lock()
			h := append(s.history[id], snap)
			if len(h) > 360 {
				h = h[len(h)-360:]
			}
			s.history[id] = h
			s.mu.Unlock()
		case "heartbeat":
			if time.Since(lastHeartbeat) < time.Second {
				continue
			}
			lastHeartbeat = time.Now()
			if _, err = s.Store.Pool.Exec(r.Context(), `UPDATE hosts SET last_seen=now() WHERE id=$1 AND NOT revoked`, id); err != nil {
				return
			}
		case "result":
			s.mu.Lock()
			ch := s.pending[m.ID]
			s.mu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
		case "task_result":
			if s.Store.UpdateTask(r.Context(), id, m) == nil {
				if m.State == "succeeded" || m.State == "failed" {
					if task, err := s.Store.Task(r.Context(), m.TaskID); err == nil {
						var args protocol.Args
						json.Unmarshal(task.Params, &args)
						resource := task.HostID + ":" + args.Path + ":" + args.Container
						s.Redis.Eval(r.Context(), `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end; return 0`, []string{"lease:" + resource}, task.AttemptID)
					}
				}
				p.Send(protocol.Message{Version: protocol.Version, Type: "ack", HostID: id, TaskID: m.TaskID, AttemptID: m.AttemptID})
			}
		case "terminal_output", "terminal_closed":
			if len(m.Data) > 16<<10 {
				return
			}
			s.mu.Lock()
			t := s.terminals[m.ID]
			s.mu.Unlock()
			if t != nil && t.host == id {
				t.send(m)
			}
		default:
			return
		}
	}
}

// Run dispatches durable records, not a volatile queue. Each delivery preserves attempt identity.
func (s *Server) Run(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	maintenance := time.NewTicker(time.Minute)
	defer maintenance.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			for _, p := range s.peers {
				p.ws.Close()
			}
			s.mu.Unlock()
			return
		case <-maintenance.C:
			s.cleanup(ctx)
		case <-tick.C:
			s.mu.Lock()
			connected := make([]string, 0, len(s.peers))
			for id := range s.peers {
				connected = append(connected, id)
			}
			s.mu.Unlock()
			ts := []protocol.Task{}
			for _, id := range connected {
				batch, err := s.Store.Tasks(ctx, id, true)
				if err == nil {
					ts = append(ts, batch...)
				}
			}
			for _, t := range ts {
				s.mu.Lock()
				p := s.peers[t.HostID]
				s.mu.Unlock()
				if p == nil {
					continue
				}
				if time.Now().After(t.Deadline) && t.State == "pending" {
					s.Store.UpdateTask(ctx, t.HostID, protocol.Message{TaskID: t.ID, AttemptID: t.AttemptID, State: "uncertain", Error: "deadline elapsed; execution must be reconciled"})
					t.State = "uncertain"
				}
				if t.Action == "file.upload" && t.State == "pending" {
					if _, err := os.Stat(s.transferPath(t.ID)); err != nil {
						continue
					}
				}
				if t.State == "pending" {
					var a protocol.Args
					json.Unmarshal(t.Params, &a)
					resource := t.HostID + ":" + a.Path + ":" + a.Container
					ok, err := s.Redis.Eval(ctx, `local v=redis.call('GET',KEYS[1]); if not v or v==ARGV[1] then redis.call('SET',KEYS[1],ARGV[1],'EX',30);return 1 end;return 0`, []string{"lease:" + resource}, t.AttemptID).Int()
					if err != nil || ok == 0 {
						continue
					}
				}
				p.Send(t.Message())
			}
		}
	}
}
