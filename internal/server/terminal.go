package server

import (
	"github.com/gorilla/websocket"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"net/http"
	"sync"
	"time"
)

type terminal struct {
	host    string
	browser *websocket.Conn
	mu      sync.Mutex
}

func (t *terminal) send(m protocol.Message) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.browser.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if t.browser.WriteJSON(m) != nil {
		t.browser.Close()
	}
}
func (s *Server) openTerminal(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !s.limited(w, r, "terminal:"+host, 10) {
		return
	}
	if r.URL.Query().Get("confirm") != "shell" {
		fail(w, 400, "explicit shell access confirmation required")
		return
	}
	s.mu.Lock()
	p := s.peers[host]
	count := len(s.terminals)
	s.mu.Unlock()
	if p == nil || count >= 8 {
		fail(w, 409, "host offline or terminal limit reached")
		return
	}
	id := protocol.ID()
	if s.Store.Audit(r.Context(), "ui", host, "terminal.open", id, "authorized") != nil {
		fail(w, 503, "audit unavailable")
		return
	}
	ws, err := upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	t := &terminal{host: host, browser: ws}
	s.mu.Lock()
	s.terminals[id] = t
	s.mu.Unlock()
	defer func() {
		ws.Close()
		s.mu.Lock()
		delete(s.terminals, id)
		s.mu.Unlock()
		p.Send(protocol.Message{Version: protocol.Version, Type: "terminal_close", HostID: host, ID: id})
		s.Store.Audit(r.Context(), "ui", host, "terminal.close", id, "closed")
	}()
	ws.SetReadLimit(32 << 10)
	if p.Send(protocol.Message{Version: protocol.Version, Type: "terminal_open", HostID: host, ID: id, Deadline: time.Now().Add(time.Hour)}) != nil {
		return
	}
	for {
		ws.SetReadDeadline(time.Now().Add(15 * time.Minute))
		var m protocol.Message
		if ws.ReadJSON(&m) != nil {
			return
		}
		if m.Type != "terminal_input" && m.Type != "terminal_resize" {
			return
		}
		m.ID = id
		m.HostID = host
		m.Version = protocol.Version
		if p.Send(m) != nil {
			return
		}
	}
}
