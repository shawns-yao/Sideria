package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shawns-yao/Sideria/internal/protocol"
)

// Each run creates and drops its own databases. No production URL is accepted.
func TestPostgresBackupRestore(t *testing.T) {
	databaseURL := os.Getenv("SIDERIA_TEST_DATABASE")
	container := os.Getenv("SIDERIA_TEST_POSTGRES_CONTAINER")
	if databaseURL == "" || container == "" {
		t.Skip("isolated PostgreSQL container required for official pg_dump/restore clients")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil || (cfg.Host != "127.0.0.1" && cfg.Host != "localhost") {
		t.Fatal("backup test requires loopback database")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	names := []string{"sideria_source_" + protocol.ID()[:12], "sideria_restore_" + protocol.ID()[:12], "sideria_corrupt_" + protocol.ID()[:12]}
	for _, name := range names {
		if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		defer admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	}
	sourceURL, _ := url.Parse(databaseURL)
	sourceURL.Path = "/" + names[0]
	source, err := OpenStore(ctx, sourceURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer source.Pool.Close()
	_, err = source.Pool.Exec(ctx, `INSERT INTO hosts(id,name,token_hash,enrollment_hash,enrollment_expires) VALUES('backup-host','备份测试','test-credential-hash','test-enrollment-hash',now()+interval '1 hour'); INSERT INTO sessions VALUES('test-session-hash',now()+interval '1 hour'); INSERT INTO projects VALUES('project','backup-host','.','fixture'); INSERT INTO analyses VALUES('analysis',now(),'{"status":"incomplete","evidence":[]}')`)
	if err != nil {
		t.Fatal(err)
	}
	states := []string{"pending", "running", "uncertain", "succeeded", "failed"}
	original := map[string]protocol.Task{}
	for i := 0; i < 80; i++ {
		task, _, e := source.CreateTask(ctx, "backup-host", fmt.Sprintf("backup-key-%d", i), "file.download", protocol.Raw(protocol.Args{Path: fmt.Sprintf("fixture-%d.txt", i)}))
		if e != nil {
			t.Fatal(e)
		}
		state := states[i%len(states)]
		if state != "pending" {
			e = source.UpdateTask(ctx, "backup-host", protocol.Message{TaskID: task.ID, AttemptID: task.AttemptID, State: state, Data: protocol.Raw(map[string]int{"sample": i})})
			if e != nil {
				t.Fatal(e)
			}
		}
		task, _ = source.Task(ctx, task.ID)
		original[task.ID] = task
	}
	var originalAudit string
	if err = source.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(a) ORDER BY id)::text FROM audit a`).Scan(&originalAudit); err != nil {
		t.Fatal(err)
	}
	var maxAudit int64
	source.Pool.QueryRow(ctx, `SELECT max(id) FROM audit`).Scan(&maxAudit)
	bundle := filepath.Join(t.TempDir(), "backup")
	script, _ := filepath.Abs("../../scripts/database-backup.py")
	run := func(database, operation string, wantSuccess bool) {
		t.Helper()
		args := []string{script, operation, bundle, "--container", container}
		if operation == "restore" {
			args = append(args, "--target", database)
		}
		cmd := exec.CommandContext(ctx, "python3", args...)
		// Ignore ambient libpq configuration; test container is explicitly authorized.
		for _, env := range os.Environ() {
			if !strings.HasPrefix(env, "PG") {
				cmd.Env = append(cmd.Env, env)
			}
		}
		cmd.Env = append(cmd.Env, "PGDATABASE="+database, "PGUSER=postgres", "PGCONNECT_TIMEOUT=5")
		out, e := cmd.CombinedOutput()
		if (e == nil) != wantSuccess {
			t.Fatalf("%s success=%v: %v %s", operation, wantSuccess, e, out)
		}
	}
	started := time.Now()
	run(names[0], "backup", true)
	backupDuration := time.Since(started)
	for _, path := range []string{bundle, filepath.Join(bundle, "database.dump"), filepath.Join(bundle, "manifest.json")} {
		info, e := os.Stat(path)
		if e != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("backup permissions", path, e)
		}
	}
	run(names[0], "backup", false) // Never replace a completed bundle.
	started = time.Now()
	run(names[1], "restore", true)
	restoreDuration := time.Since(started)
	targetURL, _ := url.Parse(databaseURL)
	targetURL.Path = "/" + names[1]
	target, err := OpenStore(ctx, targetURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Pool.Close()
	for id, old := range original {
		got, e := target.Task(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if old.State == "pending" || old.State == "running" || old.State == "uncertain" {
			if got.State != "uncertain" || !strings.Contains(got.Error, "database restored") {
				t.Fatal("active attempt not fenced")
			}
			old.State = got.State
			old.Error = got.Error
		}
		// jsonb may reorder embedded result keys; compare semantic JSON below.
		var a, b any
		json.Unmarshal(protocol.Raw(old), &a)
		json.Unmarshal(protocol.Raw(got), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("task identity/result changed: %s", id)
		}
	}
	var restoredAudit string
	err = target.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(a) ORDER BY id)::text FROM audit a WHERE id <= $1`, maxAudit).Scan(&restoredAudit)
	if err != nil || restoredAudit != originalAudit {
		t.Fatal("original audit not exactly preserved", err)
	}
	var activeCredentials, activeSessions, projects, analyses, activeDispatch int
	target.Pool.QueryRow(ctx, `SELECT count(*) FROM hosts WHERE NOT revoked OR token_hash IS NOT NULL OR enrollment_hash IS NOT NULL`).Scan(&activeCredentials)
	target.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&activeSessions)
	target.Pool.QueryRow(ctx, `SELECT count(*) FROM projects`).Scan(&projects)
	target.Pool.QueryRow(ctx, `SELECT count(*) FROM analyses`).Scan(&analyses)
	target.Pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE state IN ('pending','running')`).Scan(&activeDispatch)
	if activeCredentials+activeSessions+activeDispatch != 0 || projects != 1 || analyses != 1 {
		t.Fatal("restore scope/fencing incomplete")
	}
	if _, _, e := target.CreateTask(ctx, "backup-host", "new-key", "file.download", protocol.Raw(protocol.Args{Path: "hello"})); e == nil {
		t.Fatal("restored host accepted new task without reauthorization")
	}
	// A non-empty target and an altered archive must be rejected without changing facts.
	run(names[1], "restore", false)
	archive := filepath.Join(bundle, "database.dump")
	f, _ := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("corruption")
	f.Close()
	run(names[2], "restore", false)
	corruptCfg := cfg.Copy()
	corruptCfg.Database = names[2]
	clean, e := pgx.ConnectConfig(ctx, corruptCfg)
	if e != nil {
		t.Fatal(e)
	}
	defer clean.Close(ctx)
	var count int
	clean.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(&count)
	if count != 0 {
		t.Fatal("failed restore changed empty destination")
	}
	// A checksum-consistent but truncated archive exercises rollback after restore begins.
	data, _ := os.ReadFile(archive)
	data = data[:len(data)/2]
	os.WriteFile(archive, data, 0600)
	manifestPath := filepath.Join(bundle, "manifest.json")
	manifestData, _ := os.ReadFile(manifestPath)
	var manifest map[string]any
	json.Unmarshal(manifestData, &manifest)
	digest := sha256.Sum256(data)
	manifest["sha256"] = hex.EncodeToString(digest[:])
	manifest["bytes"] = len(data)
	os.WriteFile(manifestPath, protocol.Raw(manifest), 0600)
	run(names[2], "restore", false)
	clean.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(&count)
	if count != 0 {
		t.Fatal("interrupted restore did not roll back schema and rows")
	}
	t.Logf("backup/restore: 80 tasks, %d original audit rows exact; active attempts quarantined, credentials revoked; archive=%d bytes backup=%s restore=%s", maxAudit, int64(len(data)*2)-10, backupDuration, restoreDuration)
}
