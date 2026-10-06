package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Cleanup expires temporary content, not unresolved execution facts.
func (s *Server) cleanup(ctx context.Context) {
	s.Store.Pool.Exec(ctx, `DELETE FROM sessions WHERE expires<now()`)
	s.Store.Pool.Exec(ctx, `DELETE FROM analyses WHERE created_at<now()-interval '30 days'`)
	s.Store.Pool.Exec(ctx, `DELETE FROM audit WHERE at<now()-interval '90 days'`)
	s.Store.Pool.Exec(ctx, `DELETE FROM tasks WHERE state IN ('succeeded','failed') AND updated_at<now()-interval '90 days'`)
	entries, err := os.ReadDir(s.Config.TransferDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err == nil && time.Since(info.ModTime()) > time.Hour {
			os.Remove(filepath.Join(s.Config.TransferDir, entry.Name()))
		}
	}
}
func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	s.mu.Lock()
	connections, queries, terminals := len(s.peers), len(s.pending), len(s.terminals)
	samples := 0
	for _, h := range s.history {
		samples += len(h)
	}
	s.mu.Unlock()
	db := s.Store.Pool.Stat()
	reply(w, 200, map[string]any{"agent_connections": connections, "active_queries": queries, "terminal_sessions": terminals, "window_samples": samples, "goroutines": runtime.NumGoroutine(), "heap_bytes": mem.HeapAlloc, "db_connections": db.TotalConns(), "db_acquired": db.AcquiredConns(), "limits": map[string]int{"queries": 64, "terminal_sessions": 8, "history_per_host": 360, "transfer_bytes": MaxTransfer}})
}
