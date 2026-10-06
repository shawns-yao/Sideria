package server

import (
	"github.com/shawns-yao/Sideria/internal/protocol"
	"net/http"
	"time"
)

// Re-enrollment preserves Host identity/history but revokes the old credential and
// fences every previously accepted task. A fresh journal must never replay them.
func (s *Server) reenroll(w http.ResponseWriter, r *http.Request) {
	if !s.limited(w, r, "reenroll", 10) {
		return
	}
	var b struct {
		Confirm bool `json:"confirm"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !b.Confirm {
		fail(w, 400, "identity replacement confirmation required")
		return
	}
	id := r.PathValue("host")
	token := protocol.ID()
	tx, err := s.Store.Pool.Begin(r.Context())
	if err != nil {
		fail(w, 503, "database unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `UPDATE hosts SET token_hash=NULL,enrollment_hash=$2,enrollment_expires=$3,revoked=false,identity_generation=identity_generation+1,snapshot=NULL,last_seen=NULL,capabilities='{}' WHERE id=$1`, id, protocol.Hash(token), time.Now().Add(10*time.Minute))
	if err != nil || tag.RowsAffected() != 1 {
		fail(w, 404, "host unavailable")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE tasks SET state='uncertain',body=jsonb_set(jsonb_set(body,'{state}','"uncertain"'::jsonb),'{error}','"identity replaced; prior attempt requires manual reconciliation"'::jsonb),updated_at=now() WHERE host_id=$1 AND state IN ('pending','running','uncertain')`, id)
	if err != nil {
		fail(w, 503, "task fencing failed")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO audit(source,host_id,action,request_id,state) VALUES('ui',$1,'host.reenroll',$2,'old identity revoked; old tasks fenced')`, id, protocol.ID())
	if err != nil || tx.Commit(r.Context()) != nil {
		fail(w, 503, "identity persistence failed")
		return
	}
	s.mu.Lock()
	delete(s.history, id)
	if p := s.peers[id]; p != nil {
		p.ws.Close()
	}
	s.mu.Unlock()
	reply(w, 201, map[string]string{"id": id, "enrollment_token": token})
}
