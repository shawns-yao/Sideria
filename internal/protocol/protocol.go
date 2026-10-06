package protocol

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

const Version = 1
const MaxMessage = 1 << 20

func ID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

type Message struct {
	Version     int             `json:"version"`
	Type        string          `json:"type"`
	ID          string          `json:"id,omitempty"`
	HostID      string          `json:"host_id,omitempty"`
	TaskID      string          `json:"task_id,omitempty"`
	StepID      string          `json:"step_id,omitempty"`
	ExecutionID string          `json:"execution_id,omitempty"`
	AttemptID   string          `json:"attempt_id,omitempty"`
	Action      string          `json:"action,omitempty"`
	Deadline    time.Time       `json:"deadline,omitempty"`
	Params      json.RawMessage `json:"params,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	State       string          `json:"state,omitempty"`
	Error       string          `json:"error,omitempty"`
}

func Raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

type Snapshot struct {
	SampledAt       time.Time `json:"sampled_at"`
	ReceivedAt      time.Time `json:"received_at"`
	CPU             *float64  `json:"cpu"`
	Cores           int       `json:"cores"`
	MemoryTotal     uint64    `json:"memory_total"`
	MemoryAvailable uint64    `json:"memory_available"`
	MemoryValid     bool      `json:"memory_valid"`
	SwapTotal       uint64    `json:"swap_total"`
	SwapUsed        uint64    `json:"swap_used"`
	Load            []float64 `json:"load"`
	Uptime          uint64    `json:"uptime"`
	OS              string    `json:"os"`
	Hostname        string    `json:"hostname"`
	Disks           []Disk    `json:"disks"`
	Networks        []Network `json:"networks"`
	Errors          []string  `json:"errors"`
}
type Disk struct {
	Mount       string `json:"mount"`
	Total       uint64 `json:"total"`
	Used        uint64 `json:"used"`
	InodesTotal uint64 `json:"inodes_total"`
	InodesUsed  uint64 `json:"inodes_used"`
}
type Network struct {
	Name     string   `json:"name"`
	Received uint64   `json:"received"`
	Sent     uint64   `json:"sent"`
	RX       *float64 `json:"rx"`
	TX       *float64 `json:"tx"`
}
type Host struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Online       bool              `json:"online"`
	LastSeen     *time.Time        `json:"last_seen"`
	Capabilities map[string]string `json:"capabilities"`
	Snapshot     *Snapshot         `json:"snapshot"`
	History      []Snapshot        `json:"history,omitempty"`
}
type Task struct {
	ID          string          `json:"id"`
	HostID      string          `json:"host_id"`
	StepID      string          `json:"step_id"`
	ExecutionID string          `json:"execution_id"`
	AttemptID   string          `json:"attempt_id"`
	Action      string          `json:"action"`
	Params      json.RawMessage `json:"params"`
	State       string          `json:"state"`
	CreatedAt   time.Time       `json:"created_at"`
	Deadline    time.Time       `json:"deadline"`
	Result      json.RawMessage `json:"result"`
	Error       string          `json:"error"`
}

func (t Task) Message() Message {
	return Message{Version: Version, Type: "task", ID: t.ID, TaskID: t.ID, StepID: t.StepID, ExecutionID: t.ExecutionID, AttemptID: t.AttemptID, HostID: t.HostID, Action: t.Action, Params: t.Params, Deadline: t.Deadline}
}
