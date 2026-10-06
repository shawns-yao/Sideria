package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const PreviewLimit = 64 << 10

// cappedWriter stops commands which exceed the output budget without retaining it in memory.
type cappedWriter struct {
	b   []byte
	max int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if len(w.b)+len(p) > w.max {
		return 0, errors.New("output budget exceeded")
	}
	w.b = append(w.b, p...)
	return len(p), nil
}
func (a *Agent) Execute(ctx context.Context, action string, raw json.RawMessage) (any, error) {
	args, err := protocol.Validate(action, raw)
	if err != nil {
		return nil, err
	}
	def := protocol.Actions[action]
	if a.Capabilities()[def.Capability] != "available" {
		return nil, errors.New("capability denied or unavailable")
	}
	switch action {
	case "file.list":
		return a.list(args)
	case "file.read":
		f, err := a.root.OpenFile(args.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			return nil, errors.New("regular file required")
		}
		b, err := io.ReadAll(io.LimitReader(f, PreviewLimit+1))
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
			return nil, errors.New("binary preview unavailable")
		}
		truncated := len(b) > PreviewLimit
		if truncated {
			b = b[:PreviewLimit]
		}
		return map[string]any{"text": string(b), "truncated": truncated, "size": st.Size()}, nil
	case "git.status", "git.diff":
		return a.git(ctx, args.Path, action == "git.diff")
	default:
		if strings.HasPrefix(action, "docker.") {
			return a.docker(ctx, action, args.Container)
		}
		return nil, errors.New("unsupported action")
	}
}
func (a *Agent) list(args protocol.Args) (any, error) {
	f, err := a.root.OpenFile(args.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.IsDir() {
		return nil, errors.New("directory required")
	}
	if args.Offset > 0 {
		if _, err = f.ReadDir(args.Offset); err != nil && err != io.EOF {
			return nil, err
		}
	}
	entries, err := f.ReadDir(201)
	if err != nil && err != io.EOF {
		return nil, err
	}
	more := len(entries) > 200
	if more {
		entries = entries[:200]
	}
	out := []map[string]any{}
	for _, e := range entries {
		st, err := e.Info()
		if err != nil {
			continue
		}
		uid, gid := uint32(0), uint32(0)
		if x, ok := st.Sys().(*syscall.Stat_t); ok {
			uid = x.Uid
			gid = x.Gid
		}
		link := ""
		if st.Mode()&os.ModeSymlink != 0 {
			link, _ = a.root.Readlink(filepath.Join(args.Path, e.Name()))
		}
		out = append(out, map[string]any{"name": e.Name(), "directory": e.IsDir(), "size": st.Size(), "mode": st.Mode().String(), "modified": st.ModTime(), "uid": uid, "gid": gid, "symlink": link})
	}
	return map[string]any{"entries": out, "more": more, "offset": args.Offset}, nil
}
func (a *Agent) git(ctx context.Context, path string, diff bool) (any, error) {
	// Holding the directory descriptor avoids a path swap between validation and exec.
	dir, err := a.root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	st, err := dir.Stat()
	if err != nil || !st.IsDir() {
		return nil, errors.New("repository directory required")
	}
	run := func(args ...string) (string, error) {
		base := []string{"--no-pager", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "core.quotePath=true"}
		cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
		cmd.Dir = fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), dir.Fd())
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
		cmd.WaitDelay = time.Second
		out := &cappedWriter{max: PreviewLimit}
		cmd.Stdout = out
		diagnostic := &cappedWriter{max: 4096}
		cmd.Stderr = diagnostic
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git query failed: %w (%s)", err, diagnostic.b)
		}
		return strings.TrimSpace(string(out.b)), nil
	}
	prefix, err := run("rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	if prefix != "" {
		return nil, errors.New("register the repository root explicitly; ancestor repository is outside selected directory")
	}
	if diff {
		v, e := run("diff", "--no-ext-diff", "--no-textconv", "--", ".")
		return map[string]any{"diff": v, "limit": PreviewLimit}, e
	}
	status, err := run("status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	refs, _ := run("for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads", "refs/remotes")
	log, _ := run("log", "-5", "--format=%h %s")
	remotes, _ := run("config", "--get-regexp", `^remote\..*\.url$`)
	head, _ := run("rev-parse", "--verify", "HEAD")
	return map[string]any{"status": status, "head": head, "branches": refs, "recent_commits": log, "remotes": remoteSummary(remotes), "summary": gitSummary(status), "observed_at": time.Now().UTC(), "remote_freshness": "unknown: local references only; no fetch performed", "running_version": "not verified"}, nil
}
func safeError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return fmt.Sprintf("%s", s)
}
