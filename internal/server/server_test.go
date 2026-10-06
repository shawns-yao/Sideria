package server

import (
	"github.com/shawns-yao/Sideria/internal/protocol"
	"testing"
)

func TestQueryResultCannotCrossHostScope(t *testing.T) {
	s := New(nil, nil, Config{})
	ch := make(chan protocol.Message, 1)
	s.pending["known-request-id"] = pendingRequest{host: "host-a", result: ch}
	m := protocol.Message{ID: "known-request-id", Data: protocol.Raw("forged result")}
	if s.deliverResult("host-b", m) {
		t.Fatal("another authenticated host injected a result")
	}
	if len(ch) != 0 {
		t.Fatal("foreign result delivered")
	}
	if !s.deliverResult("host-a", m) {
		t.Fatal("authorized result rejected")
	}
}
