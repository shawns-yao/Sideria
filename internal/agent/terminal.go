package agent

import (
	"encoding/base64"
	"encoding/json"
	"github.com/creack/pty"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"os"
	"os/exec"
	"os/user"
	"sync"
	"syscall"
	"time"
)

type ptySession struct {
	f    *os.File
	cmd  *exec.Cmd
	once sync.Once
}

func (t *ptySession) close() {
	t.once.Do(func() {
		t.f.Close()
		if t.cmd.Process != nil {
			syscall.Kill(-t.cmd.Process.Pid, syscall.SIGHUP)
		}
	})
}
func (a *Agent) terminal(m protocol.Message) {
	a.mu.Lock()
	t := a.terminals[m.ID]
	a.mu.Unlock()
	if m.Type == "terminal_open" {
		if !a.Config.Terminal || time.Now().After(m.Deadline) {
			a.send(protocol.Message{Type: "terminal_closed", ID: m.ID, Error: "terminal permission denied"})
			return
		}
		a.mu.Lock()
		count := len(a.terminals)
		a.mu.Unlock()
		if count >= 8 || t != nil {
			return
		}
		shell := "/bin/sh"
		if _, e := os.Stat("/bin/bash"); e == nil {
			shell = "/bin/bash"
		}
		cmd := exec.Command(shell)
		cmd.Dir = a.Config.Root
		cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "TERM=xterm-256color", "HOME=" + a.Config.Root}
		f, err := pty.Start(cmd)
		if err != nil {
			a.send(protocol.Message{Type: "terminal_closed", ID: m.ID, Error: "PTY unavailable"})
			return
		}
		t = &ptySession{f: f, cmd: cmd}
		a.mu.Lock()
		a.terminals[m.ID] = t
		a.mu.Unlock()
		u, _ := user.Current()
		name := "unknown"
		if u != nil {
			name = u.Username
		}
		a.send(protocol.Message{Type: "terminal_output", ID: m.ID, Data: protocol.Raw(map[string]string{"shell": shell, "user": name})})
		go func() {
			timer := time.AfterFunc(time.Until(m.Deadline), t.close)
			defer timer.Stop()
			defer func() {
				t.close()
				cmd.Wait()
				a.mu.Lock()
				delete(a.terminals, m.ID)
				a.mu.Unlock()
				a.send(protocol.Message{Type: "terminal_closed", ID: m.ID})
			}()
			buf := make([]byte, 8192)
			for {
				n, e := f.Read(buf)
				if n > 0 {
					if a.send(protocol.Message{Type: "terminal_output", ID: m.ID, Data: protocol.Raw(map[string]string{"bytes": base64.StdEncoding.EncodeToString(buf[:n])})}) != nil {
						return
					}
				}
				if e != nil {
					return
				}
			}
		}()
		return
	}
	if t == nil {
		return
	}
	switch m.Type {
	case "terminal_close":
		t.close()
	case "terminal_input":
		var v struct {
			Bytes string `json:"bytes"`
		}
		if json.Unmarshal(m.Data, &v) == nil {
			b, e := base64.StdEncoding.DecodeString(v.Bytes)
			if e == nil && len(b) <= 8192 {
				t.f.Write(b)
			}
		}
	case "terminal_resize":
		var v struct {
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(m.Data, &v) == nil && v.Cols > 0 && v.Cols <= 500 && v.Rows > 0 && v.Rows <= 200 {
			pty.Setsize(t.f, &pty.Winsize{Rows: v.Rows, Cols: v.Cols})
		}
	}
}
