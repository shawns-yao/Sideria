package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MaxTransfer = 32 << 20

func (s *Server) transferPath(id string) string { return filepath.Join(s.Config.TransferDir, id) }
func saveStream(dir string, r io.Reader) (string, string, int64, error) {
	f, e := os.CreateTemp(dir, "transfer-")
	if e != nil {
		return "", "", 0, e
	}
	name := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(name)
		}
	}()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, MaxTransfer+1))
	if e != nil {
		return "", "", n, e
	}
	if n > MaxTransfer {
		return "", "", n, errors.New("transfer exceeds 32 MiB limit")
	}
	if e = f.Sync(); e != nil {
		return "", "", n, e
	}
	ok = true
	return name, hex.EncodeToString(h.Sum(nil)), n, nil
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if !s.beginTransfer(w) {
		return
	}
	defer func() { <-s.transferSlots }()
	host := r.PathValue("host")
	if !s.limited(w, r, "upload:"+host, 10) {
		return
	}
	path := r.URL.Query().Get("path")
	params := protocol.Raw(protocol.Args{Path: path})
	if _, err := protocol.Validate("file.upload", params); err != nil {
		fail(w, 400, err.Error())
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		fail(w, 400, "idempotency key required")
		return
	}
	if r.Header.Get("X-Confirm-Target") != host {
		fail(w, 400, "target confirmation required")
		return
	}
	name, digest, _, err := saveStream(s.Config.TransferDir, r.Body)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	defer os.Remove(name)
	// Include content digest in request identity so same key with different bytes conflicts.
	t, created, err := s.Store.CreateTask(r.Context(), host, key, "file.upload", protocol.Raw(map[string]any{"path": path, "content_sha256": digest}))
	if err != nil {
		if errors.Is(err, ErrConflict) {
			fail(w, 409, err.Error())
		} else {
			fail(w, 503, "transfer persistence failed")
		}
		return
	}
	if _, statErr := os.Stat(s.transferPath(t.ID)); created || (os.IsNotExist(statErr) && t.State == "pending") {
		if err = os.Rename(name, s.transferPath(t.ID)); err != nil {
			fail(w, 503, "transfer spool unavailable")
			return
		}
	}
	reply(w, 202, t)
}
func (s *Server) agentTransfer(w http.ResponseWriter, r *http.Request) {
	host, err := s.Store.HostToken(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil {
		fail(w, 401, "agent authentication failed")
		return
	}
	t, err := s.Store.Task(r.Context(), r.PathValue("id"))
	if err != nil || t.HostID != host || time.Now().After(t.Deadline) || r.Header.Get("X-Attempt-ID") != t.AttemptID {
		fail(w, 403, "transfer authorization denied")
		return
	}
	var currentIdentity bool
	err = s.Store.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM tasks t JOIN hosts h ON h.id=t.host_id WHERE t.id=$1 AND t.identity_generation=h.identity_generation AND NOT h.revoked AND t.state IN ('pending','running'))`, t.ID).Scan(&currentIdentity)
	if err != nil || !currentIdentity {
		fail(w, 403, "transfer attempt is no longer authorized")
		return
	}
	if t.Action == "file.upload" && r.Method == "GET" {
		f, err := os.Open(s.transferPath(t.ID))
		if err != nil {
			fail(w, 409, "transfer content not ready")
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		io.Copy(w, f)
		return
	}
	if t.Action == "file.download" && r.Method == "PUT" {
		if !s.beginTransfer(w) {
			return
		}
		defer func() { <-s.transferSlots }()
		name, _, _, err := saveStream(s.Config.TransferDir, r.Body)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if err = os.Rename(name, s.transferPath(t.ID)); err != nil {
			os.Remove(name)
			fail(w, 503, "spool write failed")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	fail(w, 405, "transfer direction mismatch")
}
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.Task(r.Context(), r.PathValue("id"))
	if err != nil || t.Action != "file.download" || t.State != "succeeded" {
		fail(w, 409, "download not ready")
		return
	}
	f, err := os.Open(s.transferPath(t.ID))
	if err != nil {
		fail(w, 410, "transfer expired")
		return
	}
	defer f.Close()
	var args protocol.Args
	json.Unmarshal(t.Params, &args)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=download")
	io.Copy(w, f)
}

func (s *Server) beginTransfer(w http.ResponseWriter) bool {
	select {
	case s.transferSlots <- struct{}{}:
	default:
		w.Header().Set("Retry-After", "1")
		fail(w, 429, "transfer concurrency limit reached")
		return false
	}
	entries, err := os.ReadDir(s.Config.TransferDir)
	var size int64
	if err == nil {
		for _, e := range entries {
			if info, e := e.Info(); e == nil {
				size += info.Size()
			}
		}
	}
	if err != nil || size >= (512<<20)-MaxTransfer {
		<-s.transferSlots
		fail(w, 507, "transfer spool capacity unavailable")
		return false
	}
	return true
}
