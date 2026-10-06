package protocol

import (
	"testing"
)

func TestActionBoundaries(t *testing.T) {
	for _, p := range []string{"../secret", "/etc/passwd", "a/../../b", ""} {
		if _, e := Validate("file.read", Raw(Args{Path: p})); e == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	if _, e := Validate("docker.restart", Raw(Args{Container: "x;rm -rf /"})); e == nil {
		t.Fatal("accepted container command")
	}
	if _, e := Validate("shell.exec", Raw(Args{})); e == nil {
		t.Fatal("accepted shell")
	}
	if _, e := Validate("file.list", Raw(Args{Path: "."})); e != nil {
		t.Fatal(e)
	}
}
