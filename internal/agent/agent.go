package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"golang.org/x/time/rate"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Config struct {
	URL, HostID, Token, Root, JournalPath, DockerSocket string
	Terminal                                            bool
	Dev                                                 bool
	Interval                                            time.Duration
	TransferBytesPerSecond                              int64
	TransferMaxBytes                                    int64
	TransferConcurrency                                 int
}
type Agent struct {
	Config        Config
	root          *os.Root
	journal       *Journal
	mu            sync.Mutex
	conn          *websocket.Conn
	write         sync.Mutex
	resources     map[string]bool
	slots         chan struct{}
	taskSlots     chan struct{}
	terminals     map[string]*ptySession
	workers       sync.WaitGroup
	transferSlots chan struct{}
	transferRate  *rate.Limiter
}

func New(c Config) (*Agent, error) {
	if e := ValidateURL(c.URL, c.Dev); e != nil {
		return nil, e
	}
	if c.Interval < time.Second {
		c.Interval = 5 * time.Second
	}
	if c.TransferBytesPerSecond == 0 {
		c.TransferBytesPerSecond = 1 << 20
	}
	if c.TransferMaxBytes == 0 {
		c.TransferMaxBytes = maxTransfer
	}
	if c.TransferConcurrency == 0 {
		c.TransferConcurrency = 2
	}
	if c.TransferBytesPerSecond < 64<<10 || c.TransferBytesPerSecond > 1<<30 || c.TransferMaxBytes < 1 || c.TransferMaxBytes > maxTransfer || c.TransferConcurrency < 1 || c.TransferConcurrency > 4 {
		return nil, errors.New("transfer limits outside supported range")
	}
	root, e := os.OpenRoot(c.Root)
	if e != nil {
		return nil, e
	}
	j, e := OpenJournal(c.JournalPath)
	if e != nil {
		root.Close()
		return nil, e
	}
	return &Agent{Config: c, root: root, journal: j, resources: map[string]bool{}, slots: make(chan struct{}, 8), taskSlots: make(chan struct{}, 4), terminals: map[string]*ptySession{}, transferSlots: make(chan struct{}, c.TransferConcurrency), transferRate: rate.NewLimiter(rate.Limit(c.TransferBytesPerSecond), 32<<10)}, nil
}
func (a *Agent) Close() {
	a.mu.Lock()
	if a.conn != nil {
		a.conn.Close()
	}
	a.mu.Unlock()
	a.root.Close()
	a.journal.Close()
}
func Enroll(ctx context.Context, endpoint, token string, dev bool) (string, string, error) {
	if err := ValidateURL(endpoint, dev); err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/agent/enroll", bytes.NewReader(protocol.Raw(map[string]string{"token": token})))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("enrollment redirects denied") }}
	res, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", "", errors.New("enrollment rejected")
	}
	var b struct {
		HostID string `json:"host_id"`
		Token  string `json:"token"`
	}
	if err = json.NewDecoder(res.Body).Decode(&b); err != nil {
		return "", "", err
	}
	return b.HostID, b.Token, nil
}
func (a *Agent) Capabilities() map[string]string {
	c := map[string]string{"files": "available", "git": "unavailable: git not installed", "docker": "disabled: socket not authorized", "terminal": "disabled: explicit agent permission required"}
	if _, e := exec.LookPath("git"); e == nil {
		c["git"] = "available"
	}
	if a.Config.DockerSocket != "" {
		c["docker"] = "available"
	}
	if a.Config.Terminal {
		c["terminal"] = "available"
	}
	return c
}
func (a *Agent) send(m protocol.Message) error {
	m.Version = protocol.Version
	m.HostID = a.Config.HostID
	a.write.Lock()
	defer a.write.Unlock()
	a.mu.Lock()
	ws := a.conn
	a.mu.Unlock()
	if ws == nil {
		return errors.New("disconnected")
	}
	ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return ws.WriteJSON(m)
}
func (a *Agent) Run(ctx context.Context) {
	defer a.workers.Wait()
	backoff := time.Second
	for ctx.Err() == nil {
		a.session(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff + time.Duration(rand.IntN(500))*time.Millisecond):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
func (a *Agent) session(ctx context.Context) {
	u := strings.Replace(a.Config.URL, "http", "ws", 1) + "/agent/connect"
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, u, http.Header{"Authorization": []string{"Bearer " + a.Config.Token}})
	if err != nil {
		return
	}
	a.mu.Lock()
	a.conn = ws
	a.mu.Unlock()
	defer func() {
		ws.Close()
		a.mu.Lock()
		a.conn = nil
		for _, t := range a.terminals {
			t.close()
		}
		a.mu.Unlock()
	}()
	ws.SetReadLimit(protocol.MaxMessage)
	if a.send(protocol.Message{Type: "hello", Data: protocol.Raw(a.Capabilities())}) != nil {
		return
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	var local sync.WaitGroup
	local.Add(2)
	defer func() { cancel(); local.Wait() }()
	go func() { <-sessionCtx.Done(); ws.Close() }()
	go func() {
		defer local.Done()
		sampler := &Sampler{}
		tick := time.NewTicker(a.Config.Interval)
		defer tick.Stop()
		for {
			snap := sampler.Sample(sessionCtx)
			snap.IntervalSeconds = a.Config.Interval.Seconds()
			if a.send(protocol.Message{Type: "snapshot", Data: protocol.Raw(snap)}) != nil {
				return
			}
			select {
			case <-sessionCtx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	go func() {
		defer local.Done()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-tick.C:
				if a.send(protocol.Message{Type: "heartbeat"}) != nil {
					return
				}
			}
		}
	}()
	for {
		var m protocol.Message
		if ws.ReadJSON(&m) != nil {
			return
		}
		if m.Version != protocol.Version || m.HostID != a.Config.HostID {
			return
		}
		switch m.Type {
		case "query", "task":
			slots := a.slots
			if m.Type == "task" {
				slots = a.taskSlots
			}
			select {
			case slots <- struct{}{}:
				a.workers.Add(1)
				go func() { defer a.workers.Done(); defer func() { <-slots }(); a.handle(ctx, m) }()
			default:
				// Pending tasks stay durable at the center and are redelivered with the same Attempt.
				if m.Type == "query" {
					a.send(protocol.Message{Type: "result", ID: m.ID, State: "failed", Error: "agent query capacity reached"})
				}
			}
		case "ack":
		case "terminal_open", "terminal_input", "terminal_resize", "terminal_close":
			a.terminal(m)
		default:
			return
		}
	}
}
func (a *Agent) handle(parent context.Context, m protocol.Message) {
	ctx, cancel := context.WithDeadline(parent, m.Deadline)
	defer cancel()
	if m.Type == "query" {
		if d, ok := protocol.Actions[m.Action]; !ok || d.Task {
			a.send(protocol.Message{Type: "result", ID: m.ID, State: "failed", Error: "query action denied"})
			return
		}
		v, err := a.Execute(ctx, m.Action, m.Params)
		state := "succeeded"
		if err != nil {
			state = "failed"
		}
		a.send(protocol.Message{Type: "result", ID: m.ID, State: state, Data: protocol.Raw(v), Error: safeError(err)})
		return
	}
	a.mu.Lock()
	conflict, conflictErr := a.journal.Conflicts(m)
	if conflict || conflictErr != nil {
		a.mu.Unlock()
		m.Type = "task_result"
		m.State = "failed"
		m.Error = "resource conflict or unresolved prior execution"
		a.send(m)
		return
	}
	old, created, err := a.journal.Accept(m)
	a.mu.Unlock()
	if err != nil {
		m.Type = "task_result"
		m.State = "uncertain"
		m.Error = safeError(err)
		a.send(m)
		return
	}
	if !created {
		a.send(old)
		return
	}
	a.send(old)
	var args protocol.Args
	json.Unmarshal(m.Params, &args)
	resource := args.Path + ":" + args.Container
	a.mu.Lock()
	busy := a.resources[resource]
	if !busy {
		a.resources[resource] = true
	}
	a.mu.Unlock()
	var v any
	if busy {
		err = errors.New("resource conflict: another attempt is executing")
	} else {
		defer func() { a.mu.Lock(); delete(a.resources, resource); a.mu.Unlock() }()
		if m.Action == "file.upload" || m.Action == "file.download" {
			v, err = a.transfer(ctx, m)
		} else {
			v, err = a.Execute(ctx, m.Action, m.Params)
		}
	}
	old.State = "succeeded"
	if err != nil {
		old.State = "failed"
		if ctx.Err() != nil || errors.Is(err, ErrUncertain) {
			old.State = "uncertain"
		}
	}
	old.Error = safeError(err)
	old.Data = protocol.Raw(v)
	if a.journal.Save(old) != nil {
		old.State = "uncertain"
		old.Error = "result persistence failed"
	}
	a.send(old)
}

func ValidateURL(endpoint string, dev bool) error {
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("center URL must contain only HTTPS origin")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if !dev || u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
		return errors.New("HTTPS required except explicit loopback development")
	}
	return nil
}
