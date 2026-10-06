package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/shawns-yao/Sideria/internal/protocol"
)

type Config struct {
	AdminToken            string
	Dev                   bool
	TransferDir           string
	AIURL, AIKey, AIModel string
	AIAllow               bool
}
type Server struct {
	Store         *Store
	Redis         *redis.Client
	Config        Config
	mu            sync.Mutex
	peers         map[string]*peer
	history       map[string][]protocol.Snapshot
	pending       map[string]pendingRequest
	terminals     map[string]*terminal
	analysisSlots chan struct{}
	transferSlots chan struct{}
}
type pendingRequest struct {
	host   string
	result chan protocol.Message
}

type peer struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (p *peer) Send(m protocol.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return p.ws.WriteJSON(m)
}
func New(s *Store, r *redis.Client, c Config) *Server {
	return &Server{Store: s, Redis: r, Config: c, peers: map[string]*peer{}, history: map[string][]protocol.Snapshot{}, pending: map[string]pendingRequest{}, terminals: map[string]*terminal{}, analysisSlots: make(chan struct{}, 2), transferSlots: make(chan struct{}, 2)}
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, err string) {
	reply(w, status, map[string]string{"error": err})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, protocol.MaxMessage)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, "invalid request body")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(w, 400, "trailing JSON is not allowed")
		return false
	}
	return true
}
func (s *Server) limited(w http.ResponseWriter, r *http.Request, key string, max int) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	n, err := s.Redis.Eval(ctx, `local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],60) end; return n`, []string{"rate:" + key}).Int()
	if err != nil {
		fail(w, 503, "coordination unavailable; request not accepted")
		return false
	}
	if n > max {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "rate limit exceeded")
		return false
	}
	return true
}
func (s *Server) Handler(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, c := context.WithTimeout(r.Context(), 2*time.Second)
		defer c()
		if s.Store.Pool.Ping(ctx) != nil || s.Redis.Ping(ctx).Err() != nil {
			fail(w, 503, "dependency unavailable")
			return
		}
		reply(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /agent/enroll", s.enroll)
	mux.HandleFunc("GET /agent/connect", s.connect)
	mux.HandleFunc("/agent/transfer/{id}", s.agentTransfer)
	api := http.NewServeMux()
	api.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie("sideria")
		s.Store.Pool.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, protocol.Hash(c.Value))
		s.mu.Lock()
		for _, t := range s.terminals {
			if t.sessionHash == protocol.Hash(c.Value) {
				t.browser.Close()
			}
		}
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "sideria", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: !s.Config.Dev})
		reply(w, 200, map[string]bool{"ok": true})
	})
	api.HandleFunc("GET /api/diagnostics", s.diagnostics)
	api.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"ai_enabled": s.Config.AIAllow && s.Config.AIKey != "", "version": protocol.Version})
	})
	api.HandleFunc("GET /api/hosts", s.hosts)
	api.HandleFunc("POST /api/hosts", s.createHost)
	api.HandleFunc("POST /api/hosts/{host}/revoke", s.revoke)
	api.HandleFunc("POST /api/hosts/{host}/reenroll", s.reenroll)
	api.HandleFunc("POST /api/hosts/{host}/query", s.query)
	api.HandleFunc("POST /api/hosts/{host}/tasks", s.createTask)
	api.HandleFunc("GET /api/tasks", s.tasks)
	api.HandleFunc("GET /api/tasks/{id}", s.task)
	api.HandleFunc("GET /api/audit", s.audit)
	api.HandleFunc("GET /api/projects", s.projects)
	api.HandleFunc("POST /api/hosts/{host}/projects", s.createProject)
	api.HandleFunc("POST /api/hosts/{host}/uploads", s.upload)
	api.HandleFunc("GET /api/tasks/{id}/download", s.download)
	api.HandleFunc("GET /api/hosts/{host}/terminal", s.openTerminal)
	api.HandleFunc("POST /api/analyses", s.analyze)
	api.HandleFunc("GET /api/analyses", s.analyses)
	mux.Handle("/api/", s.auth(api))
	if assets != nil {
		mux.Handle("/", http.FileServerFS(assets))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		if s.Config.Dev {
			h, _, _ := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(h)
			if ip == nil || !ip.IsLoopback() {
				fail(w, 403, "development mode is loopback only")
				return
			}
		} else if r.TLS == nil {
			fail(w, 426, "TLS required")
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Host != r.Host {
				fail(w, 403, "origin mismatch")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("sideria")
		if err != nil {
			fail(w, 401, "sign in required")
			return
		}
		var ok bool
		err = s.Store.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash=$1 AND expires>now())`, protocol.Hash(c.Value)).Scan(&ok)
		if err != nil {
			fail(w, 503, "database unavailable")
			return
		}
		if !ok {
			fail(w, 401, "session expired")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.limited(w, r, "login:"+ip, 10) {
		return
	}
	var b struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &b) {
		return
	}
	if len(s.Config.AdminToken) < 32 || subtle.ConstantTimeCompare([]byte(b.Token), []byte(s.Config.AdminToken)) != 1 {
		fail(w, 401, "invalid credentials")
		return
	}
	token := protocol.ID()
	_, err := s.Store.Pool.Exec(r.Context(), `INSERT INTO sessions(token_hash,expires) VALUES($1,$2)`, protocol.Hash(token), time.Now().Add(8*time.Hour))
	if err != nil {
		fail(w, 503, "database unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "sideria", Value: token, Path: "/", HttpOnly: true, Secure: !s.Config.Dev, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
	reply(w, 200, map[string]bool{"ok": true})
}
func (s *Server) createHost(w http.ResponseWriter, r *http.Request) {
	if !s.limited(w, r, "create-host", 10) {
		return
	}
	var b struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &b) {
		return
	}
	b.Name = strings.TrimSpace(b.Name)
	if len(b.Name) == 0 || len(b.Name) > 120 {
		fail(w, 400, "name required (max 120 bytes)")
		return
	}
	id, token := protocol.ID(), protocol.ID()
	_, err := s.Store.Pool.Exec(r.Context(), `INSERT INTO hosts(id,name,enrollment_hash,enrollment_expires) VALUES($1,$2,$3,$4)`, id, b.Name, protocol.Hash(token), time.Now().Add(10*time.Minute))
	if err != nil {
		fail(w, 503, "database unavailable")
		return
	}
	reply(w, 201, map[string]string{"id": id, "enrollment_token": token})
}
func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.limited(w, r, "enroll:"+ip, 20) {
		return
	}
	var b struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &b) {
		return
	}
	id, token, err := s.Store.Enroll(r.Context(), b.Token)
	if err != nil {
		fail(w, 401, "invalid or expired enrollment")
		return
	}
	reply(w, 200, map[string]string{"host_id": id, "token": token})
}
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("host")
	tag, err := s.Store.Pool.Exec(r.Context(), `UPDATE hosts SET revoked=true,token_hash=NULL,enrollment_hash=NULL WHERE id=$1`, id)
	if err != nil {
		fail(w, 503, "database unavailable")
		return
	}
	if tag.RowsAffected() != 1 {
		fail(w, 404, "host missing")
		return
	}
	s.mu.Lock()
	if p := s.peers[id]; p != nil {
		p.ws.Close()
	}
	s.mu.Unlock()
	s.Store.Audit(r.Context(), "ui", id, "host.revoke", protocol.ID(), "revoked")
	reply(w, 200, map[string]bool{"ok": true})
}
func (s *Server) hosts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT id,name,last_seen,capabilities,snapshot FROM hosts WHERE NOT revoked ORDER BY name,id LIMIT 200`)
	if err != nil {
		fail(w, 503, "database unavailable")
		return
	}
	defer rows.Close()
	out := []protocol.Host{}
	for rows.Next() {
		var h protocol.Host
		var caps, snap []byte
		if err = rows.Scan(&h.ID, &h.Name, &h.LastSeen, &caps, &snap); err != nil {
			fail(w, 500, "host read failed")
			return
		}
		json.Unmarshal(caps, &h.Capabilities)
		if len(snap) > 0 {
			json.Unmarshal(snap, &h.Snapshot)
		}
		s.mu.Lock()
		h.Online = s.peers[h.ID] != nil && h.LastSeen != nil && time.Since(*h.LastSeen) < 20*time.Second
		if r.URL.Query().Get("history") == h.ID {
			h.History = append([]protocol.Snapshot{}, s.history[h.ID]...)
		}
		s.mu.Unlock()
		out = append(out, h)
	}
	reply(w, 200, out)
}
func (s *Server) request(ctx context.Context, host, action string, params json.RawMessage) (protocol.Message, error) {
	if _, err := protocol.Validate(action, params); err != nil {
		return protocol.Message{}, err
	}
	id := protocol.ID()
	ch := make(chan protocol.Message, 1)
	s.mu.Lock()
	p := s.peers[host]
	if p == nil {
		s.mu.Unlock()
		return protocol.Message{}, errors.New("host offline")
	}
	if len(s.pending) >= 64 {
		s.mu.Unlock()
		return protocol.Message{}, errors.New("query capacity reached")
	}
	s.pending[id] = pendingRequest{host: host, result: ch}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	if err := s.Store.Audit(ctx, "tool", host, action, id, "requested"); err != nil {
		return protocol.Message{}, err
	}
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := p.Send(protocol.Message{Version: protocol.Version, Type: "query", ID: id, HostID: host, Action: action, Params: params, Deadline: deadline}); err != nil {
		return protocol.Message{}, err
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case m := <-ch:
		s.Store.Audit(ctx, "tool", host, action, id, m.State)
		return m, nil
	case <-ctx.Done():
		return protocol.Message{}, ctx.Err()
	case <-timer.C:
		return protocol.Message{}, errors.New("query timed out")
	}
}
func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !s.limited(w, r, "query:"+host, 120) {
		return
	}
	var b struct {
		Action string          `json:"action"`
		Params json.RawMessage `json:"params"`
	}
	if !decode(w, r, &b) {
		return
	}
	a, ok := protocol.Actions[b.Action]
	if !ok || a.Task {
		fail(w, 403, "query action denied")
		return
	}
	if _, err := protocol.Validate(b.Action, b.Params); err != nil {
		fail(w, 400, err.Error())
		return
	}
	m, err := s.request(r.Context(), host, b.Action, b.Params)
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	reply(w, 200, m)
}
func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !s.limited(w, r, "task:"+host, 30) {
		return
	}
	var b struct {
		Action  string          `json:"action"`
		Params  json.RawMessage `json:"params"`
		Confirm bool            `json:"confirm"`
	}
	if !decode(w, r, &b) {
		return
	}
	a, ok := protocol.Actions[b.Action]
	if !ok || !a.Task || b.Action == "file.upload" {
		fail(w, 403, "task action denied")
		return
	}
	if !b.Confirm {
		fail(w, 400, "explicit target and impact confirmation required")
		return
	}
	if _, err := protocol.Validate(b.Action, b.Params); err != nil {
		fail(w, 400, err.Error())
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		fail(w, 400, "idempotency key required (8-128 bytes)")
		return
	}
	t, _, err := s.Store.CreateTask(r.Context(), host, key, b.Action, b.Params)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			fail(w, 409, err.Error())
		} else {
			fail(w, 503, "task persistence failed")
		}
		return
	}
	reply(w, 202, t)
}
func (s *Server) tasks(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Store.Tasks(r.Context(), r.URL.Query().Get("host"), false)
	if err != nil {
		fail(w, 503, "database unavailable")
		return
	}
	reply(w, 200, ts)
}
func (s *Server) task(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.Task(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, 404, "task missing")
		return
	}
	reply(w, 200, t)
}
func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT at,source,host_id,action,request_id,state FROM audit ORDER BY id DESC LIMIT 200`)
	if err != nil {
		fail(w, 503, "database unavailable")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var at time.Time
		var source, host, action, id, state string
		if rows.Scan(&at, &source, &host, &action, &id, &state) != nil {
			fail(w, 500, "audit read failed")
			return
		}
		out = append(out, map[string]any{"at": at, "source": source, "host_id": host, "action": action, "request_id": id, "state": state})
	}
	reply(w, 200, out)
}
