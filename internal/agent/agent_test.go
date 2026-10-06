package agent

import (
	"context"
	"encoding/json"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRateReset(t *testing.T) {
	if Rate(5, 10, time.Second) != nil {
		t.Fatal("counter rollback created a rate")
	}
	if Rate(1, 0, 0) != nil {
		t.Fatal("zero interval")
	}
	if v := Rate(200, 100, 2*time.Second); v == nil || *v != 50 {
		t.Fatal(v)
	}
}
func TestJournalReplayAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	j, e := OpenJournal(path)
	if e != nil {
		t.Fatal(e)
	}
	m := protocol.Message{Type: "task", TaskID: "t", AttemptID: "a", Action: "docker.restart", Params: protocol.Raw(protocol.Args{Container: "abcdefabcdef"}), Deadline: time.Now().Add(time.Minute)}
	old, created, e := j.Accept(m)
	if e != nil || !created || old.State != "running" {
		t.Fatal(old, created, e)
	}
	_, created, e = j.Accept(m)
	if e != nil || created {
		t.Fatal("replay started execution")
	}
	m.Action = "docker.stop"
	if _, _, e = j.Accept(m); e == nil {
		t.Fatal("attempt mutation accepted")
	}
	m.Action = "docker.restart"
	j.Close()
	j, e = OpenJournal(path)
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	old, created, e = j.Accept(m)
	if e != nil || created || old.State != "uncertain" {
		t.Fatal(old, created, e)
	}
}
func testAgent(t *testing.T) *Agent {
	t.Helper()
	root := t.TempDir()
	a, e := New(Config{URL: "http://127.0.0.1:1", Dev: true, Root: root, JournalPath: filepath.Join(t.TempDir(), "journal")})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(a.Close)
	return a
}
func TestFileRootAndPreview(t *testing.T) {
	a := testAgent(t)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("outside"), 0600)
	os.Symlink(outside, filepath.Join(a.Config.Root, "link"))
	if _, e := a.Execute(context.Background(), "file.read", protocol.Raw(protocol.Args{Path: "link"})); e == nil {
		t.Fatal("symlink escaped root")
	}
	os.WriteFile(filepath.Join(a.Config.Root, "hello space.txt"), []byte("hello"), 0600)
	v, e := a.Execute(context.Background(), "file.read", protocol.Raw(protocol.Args{Path: "hello space.txt"}))
	if e != nil {
		t.Fatal(e)
	}
	if v.(map[string]any)["text"] != "hello" {
		t.Fatal(v)
	}
	if _, e = a.Execute(context.Background(), "file.read", protocol.Raw(protocol.Args{Path: "."})); e == nil {
		t.Fatal("directory preview")
	}
}
func TestGitStates(t *testing.T) {
	a := testAgent(t)
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = a.Config.Root
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		if out, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git: %s %v", out, e)
		}
	}
	run("init", "-q", "-b", "main")
	v, e := a.git(context.Background(), ".", false)
	if e != nil {
		t.Fatal(e)
	}
	if v.(map[string]any)["head"] != "" {
		t.Fatal("empty repository fabricated head")
	}
	os.WriteFile(filepath.Join(a.Config.Root, "hello"), []byte("hello"), 0600)
	run("add", "hello")
	run("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "initial")
	run("checkout", "--detach")
	os.WriteFile(filepath.Join(a.Config.Root, "hello"), []byte("changed"), 0600)
	v, e = a.git(context.Background(), ".", false)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(v)
	t.Log(string(b))
	os.Mkdir(filepath.Join(a.Config.Root, "sub"), 0700)
	if _, e = a.git(context.Background(), "sub", false); e == nil {
		t.Fatal("ancestor repository silently accepted")
	}
}
func BenchmarkRate(b *testing.B) {
	for b.Loop() {
		Rate(1000, 500, 5*time.Second)
	}
}
func TestEnrollmentTransportBoundary(t *testing.T) {
	for _, u := range []string{"http://example.com", "http://127.0.0.1", "https://user:password@example.com", "https://example.com?token=x"} {
		if ValidateURL(u, false) == nil {
			t.Fatal("accepted unsafe enrollment URL", u)
		}
	}
	if ValidateURL("http://127.0.0.1:8080", true) != nil {
		t.Fatal("loopback development rejected")
	}
}
func TestGitSummaryAndRemotePrivacy(t *testing.T) {
	s := gitSummary("# branch.head main\n# branch.oid abc\n# branch.upstream origin/main\n# branch.ab +2 -3\n1 .M x\n? new\nu UU conflict")
	if s["ahead"] != 2 || s["behind"] != 3 || s["unstaged"] != 1 || s["untracked"] != 1 || s["conflicts"] != 1 || s["dirty"] != true {
		t.Fatal(s)
	}
	r := remoteSummary("remote.origin.url https://user:SECRET@example.com/repo?token=SECRET\nremote.backup.url git@example.com:path/repo")
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "user") {
		t.Fatal(string(b))
	}
}
func BenchmarkSampler(b *testing.B) {
	s := &Sampler{}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		s.Sample(ctx)
	}
}
func TestUncertainResourceConflict(t *testing.T) {
	j, e := OpenJournal(filepath.Join(t.TempDir(), "journal"))
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	m := protocol.Message{TaskID: "task", AttemptID: "attempt", Action: "docker.restart", Params: protocol.Raw(protocol.Args{Container: "abcdefabcdef"}), Deadline: time.Now().Add(time.Minute)}
	old, _, e := j.Accept(m)
	if e != nil {
		t.Fatal(e)
	}
	old.State = "uncertain"
	j.Save(old)
	next := m
	next.AttemptID = "next"
	next.TaskID = "next-task"
	if conflict, e := j.Conflicts(next); e != nil || !conflict {
		t.Fatal("uncertain resource was considered free", e)
	}
}
func TestJournalCorruptionFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	if e := os.WriteFile(path, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if j, e := OpenJournal(path); e == nil {
		j.Close()
		t.Fatal("corrupt journal silently recreated")
	}
}
func TestSpecialFileDoesNotBlock(t *testing.T) {
	a := testAgent(t)
	if e := syscall.Mkfifo(filepath.Join(a.Config.Root, "pipe"), 0600); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, e := a.Execute(context.Background(), "file.read", protocol.Raw(protocol.Args{Path: "pipe"}))
		done <- e
	}()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO open blocked")
	}
}
