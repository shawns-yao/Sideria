package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shawns-yao/Sideria/internal/protocol"
)

//go:embed schema.sql
var schema string
var ErrConflict = errors.New("idempotency key conflicts with an existing request")

type Store struct{ Pool *pgxpool.Pool }

func OpenStore(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if _, err = p.Exec(ctx, schema); err != nil {
		p.Close()
		return nil, err
	}
	return &Store{p}, nil
}
func (s *Store) Audit(ctx context.Context, source, host, action, id, state string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO audit(source,host_id,action,request_id,state) VALUES($1,$2,$3,$4,$5)`, source, host, action, id, state)
	return err
}
func (s *Store) CreateTask(ctx context.Context, host, key, action string, params json.RawMessage) (protocol.Task, bool, error) {
	var normalized any
	if err := json.Unmarshal(params, &normalized); err != nil {
		return protocol.Task{}, false, err
	}
	params = protocol.Raw(normalized)
	digest := protocol.Hash(action + string(params))
	t := protocol.Task{ID: protocol.ID(), HostID: host, StepID: protocol.ID(), ExecutionID: protocol.ID(), AttemptID: protocol.ID(), Action: action, Params: params, State: "pending", CreatedAt: time.Now().UTC(), Deadline: time.Now().Add(5 * time.Minute).UTC()}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return t, false, err
	}
	defer tx.Rollback(ctx)
	var revoked bool
	var identityGeneration int
	if err = tx.QueryRow(ctx, `SELECT revoked,identity_generation FROM hosts WHERE id=$1 FOR UPDATE`, host).Scan(&revoked, &identityGeneration); err != nil || revoked {
		return t, false, errors.New("host authorization unavailable or revoked")
	}
	tag, err := tx.Exec(ctx, `INSERT INTO tasks(id,host_id,key,digest,body,state,identity_generation) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(host_id,key) DO NOTHING`, t.ID, host, key, digest, protocol.Raw(t), t.State, identityGeneration)
	if err != nil {
		return t, false, err
	}
	created := tag.RowsAffected() == 1
	if !created {
		var b []byte
		var old string
		err = tx.QueryRow(ctx, `SELECT digest,body FROM tasks WHERE host_id=$1 AND key=$2`, host, key).Scan(&old, &b)
		if err != nil {
			return t, false, err
		}
		if old != digest {
			return t, false, ErrConflict
		}
		if err = json.Unmarshal(b, &t); err != nil {
			return t, false, err
		}
	}
	if created {
		var queued int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE host_id=$1 AND identity_generation=$2 AND state IN ('pending','running','uncertain')`, host, identityGeneration).Scan(&queued); err != nil {
			return t, false, err
		}
		if queued > 100 {
			return t, false, errors.New("host queue capacity reached")
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit(source,host_id,action,request_id,state) VALUES('ui',$1,$2,$3,'accepted')`, host, action, t.ID)
		if err != nil {
			return t, false, err
		}
	}
	return t, created, tx.Commit(ctx)
}
func (s *Store) Task(ctx context.Context, id string) (protocol.Task, error) {
	var b []byte
	var t protocol.Task
	err := s.Pool.QueryRow(ctx, `SELECT body FROM tasks WHERE id=$1`, id).Scan(&b)
	if err != nil {
		return t, err
	}
	err = json.Unmarshal(b, &t)
	return t, err
}
func (s *Store) Tasks(ctx context.Context, host string, active bool) ([]protocol.Task, error) {
	q := `SELECT body FROM tasks WHERE ($1='' OR host_id=$1)`
	if active {
		q += ` AND state IN ('pending','running','uncertain') AND identity_generation=(SELECT identity_generation FROM hosts WHERE id=tasks.host_id AND NOT revoked)`
	}
	if active {
		q += ` ORDER BY created_at ASC LIMIT 200`
	} else {
		q += ` ORDER BY created_at DESC LIMIT 200`
	}
	rows, err := s.Pool.Query(ctx, q, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Task{}
	for rows.Next() {
		var b []byte
		var t protocol.Task
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) UpdateTask(ctx context.Context, host string, m protocol.Message) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var b []byte
	err = tx.QueryRow(ctx, `SELECT body FROM tasks WHERE id=$1 AND host_id=$2 FOR UPDATE`, m.TaskID, host).Scan(&b)
	if err != nil {
		return err
	}
	var t protocol.Task
	if err = json.Unmarshal(b, &t); err != nil {
		return err
	}
	if t.AttemptID != m.AttemptID {
		return errors.New("attempt mismatch")
	}
	if t.State == "succeeded" || t.State == "failed" {
		return nil
	}
	switch m.State {
	case "running", "succeeded", "failed", "uncertain":
	default:
		return errors.New("invalid task state")
	}
	if t.State == m.State && t.Error == m.Error && string(t.Result) == string(m.Data) {
		return nil
	}
	t.State = m.State
	t.Result = m.Data
	t.Error = m.Error
	_, err = tx.Exec(ctx, `UPDATE tasks SET body=$2,state=$3,updated_at=now() WHERE id=$1`, t.ID, protocol.Raw(t), t.State)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit(source,host_id,action,request_id,state) VALUES('agent',$1,$2,$3,$4)`, host, t.Action, t.ID, t.State)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) HostToken(ctx context.Context, token string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM hosts WHERE token_hash=$1 AND NOT revoked`, protocol.Hash(token)).Scan(&id)
	return id, err
}
func (s *Store) Enroll(ctx context.Context, token string) (string, string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM hosts WHERE enrollment_hash=$1 AND enrollment_expires>now() AND NOT revoked FOR UPDATE`, protocol.Hash(token)).Scan(&id)
	if err != nil {
		return "", "", err
	}
	credential := protocol.ID()
	_, err = tx.Exec(ctx, `UPDATE hosts SET token_hash=$2,enrollment_hash=NULL,enrollment_expires=NULL WHERE id=$1`, id, protocol.Hash(credential))
	if err != nil {
		return "", "", err
	}
	return id, credential, tx.Commit(ctx)
}
func isMissing(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
