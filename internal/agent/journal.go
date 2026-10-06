package agent

import (
	"encoding/json"
	"errors"
	"github.com/shawns-yao/Sideria/internal/protocol"
	bolt "go.etcd.io/bbolt"
	"time"
)

type Journal struct{ db *bolt.DB }

func OpenJournal(path string) (*Journal, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	j := &Journal{db}
	err = db.Update(func(tx *bolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists([]byte("attempts"))
		if e != nil {
			return e
		}
		return b.ForEach(func(k, v []byte) error {
			var m protocol.Message
			if e := json.Unmarshal(v, &m); e != nil {
				return e
			}
			if m.State == "running" {
				m.State = "uncertain"
				m.Error = "agent restarted during execution; no automatic replay"
				return b.Put(k, protocol.Raw(m))
			}
			return nil
		})
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return j, nil
}
func (j *Journal) Close() error { return j.db.Close() }
func (j *Journal) Accept(m protocol.Message) (protocol.Message, bool, error) {
	var old protocol.Message
	created := false
	err := j.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("attempts"))
		key := []byte(m.AttemptID)
		if v := b.Get(key); v != nil {
			if e := json.Unmarshal(v, &old); e != nil {
				return e
			}
			if old.TaskID != m.TaskID || old.Action != m.Action || string(old.Params) != string(m.Params) {
				return errors.New("attempt content mismatch")
			}
			return nil
		}
		if tx.Size() > 64<<20 || b.Stats().KeyN >= 100000 {
			return errors.New("execution journal capacity reached; manual maintenance required")
		}
		if time.Now().After(m.Deadline) {
			return errors.New("execution authorization expired")
		}
		old = m
		old.Type = "task_result"
		old.State = "running"
		created = true
		return b.Put(key, protocol.Raw(old))
	})
	return old, created, err
}
func (j *Journal) Save(m protocol.Message) error {
	return j.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("attempts")).Put([]byte(m.AttemptID), protocol.Raw(m))
	})
}

// Conflicts includes uncertain attempts: lease expiry does not prove that an
// external side effect stopped. Operators must reconcile uncertainty before a new write.
func (j *Journal) Conflicts(m protocol.Message) (bool, error) {
	var incoming protocol.Args
	if err := json.Unmarshal(m.Params, &incoming); err != nil {
		return false, err
	}
	conflict := false
	err := j.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("attempts")).ForEach(func(_, v []byte) error {
			var old protocol.Message
			if err := json.Unmarshal(v, &old); err != nil {
				return err
			}
			if old.AttemptID == m.AttemptID || (old.State != "running" && old.State != "uncertain") {
				return nil
			}
			var args protocol.Args
			if err := json.Unmarshal(old.Params, &args); err != nil {
				return err
			}
			if args.Path == incoming.Path && args.Container == incoming.Container {
				conflict = true
			}
			return nil
		})
	})
	return conflict, err
}
