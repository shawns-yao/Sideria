package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shawns-yao/Sideria/internal/protocol"
	"golang.org/x/time/rate"
)

func TestTransferBackpressureKeepsAttemptAndBytes(t *testing.T) {
	a := testAgent(t)
	content := bytes.Repeat([]byte("bounded-transfer"), 1000)
	if err := os.WriteFile(filepath.Join(a.Config.Root, "sample.bin"), content, 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Attempt-ID") != "same-attempt" {
			t.Error("attempt changed")
		}
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		got, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(got, content) {
			t.Error("retry corrupted file bytes", err)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	a.Config.URL = server.URL
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.transfer(ctx, protocol.Message{TaskID: "task", AttemptID: "same-attempt", Action: "file.download", Params: protocol.Raw(protocol.Args{Path: "sample.bin"})})
	if err != nil || calls.Load() != 2 {
		t.Fatal("admission retry failed", err, calls.Load())
	}
}

func TestTransferFailureDoesNotPublishPartialOrOverwrite(t *testing.T) {
	for _, failure := range []string{"digest", "size", "interrupted", "exists", "redirect"} {
		t.Run(failure, func(t *testing.T) {
			a := testAgent(t)
			content := []byte("complete transfer")
			digest := sha256.Sum256(content)
			if failure == "exists" {
				os.WriteFile(filepath.Join(a.Config.Root, "target"), []byte("original"), 0600)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch failure {
				case "redirect":
					w.Header().Set("Location", "http://127.0.0.1:1/secret")
					w.WriteHeader(307)
					return
				case "interrupted":
					w.Header().Set("Content-Length", "1000")
				}
				w.Write(content)
			}))
			defer server.Close()
			a.Config.URL = server.URL
			if failure == "size" {
				a.Config.TransferMaxBytes = 3
			}
			dig := hex.EncodeToString(digest[:])
			if failure == "digest" {
				dig = "wrong"
			}
			_, err := a.transfer(context.Background(), protocol.Message{TaskID: "task", AttemptID: "attempt", Action: "file.upload", Params: protocol.Raw(protocol.Args{Path: "target", ContentSHA256: dig})})
			if err == nil {
				t.Fatal("invalid upload accepted")
			}
			got, e := os.ReadFile(filepath.Join(a.Config.Root, "target"))
			if failure == "exists" {
				if e != nil || string(got) != "original" {
					t.Fatal("existing file replaced")
				}
			} else if !os.IsNotExist(e) {
				t.Fatal("partial final file published")
			}
			entries, _ := os.ReadDir(a.Config.Root)
			for _, entry := range entries {
				if entry.Name() != "target" {
					t.Fatal("temporary content retained", entry.Name())
				}
			}
		})
	}
}

func TestSharedTransferRateAndCancellation(t *testing.T) {
	// A shared exhausted token bucket must constrain both streams; waiting is cancellable.
	limiter := rate.NewLimiter(64<<10, 32<<10)
	if !limiter.AllowN(time.Now(), 32<<10) {
		t.Fatal("initial burst missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	for i := 0; i < 2; i++ {
		reader := &pacedReader{ctx: ctx, reader: bytes.NewReader(make([]byte, 32<<10)), limiter: limiter}
		if n, err := reader.Read(make([]byte, 32<<10)); err == nil || n != 0 {
			t.Fatal("budget bypassed", n, err)
		}
	}
}

func TestOversizedDownloadNeverStartsNetworkTransfer(t *testing.T) {
	a := testAgent(t)
	f, err := os.Create(filepath.Join(a.Config.Root, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(maxTransfer + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer s.Close()
	a.Config.URL = s.URL
	_, err = a.transfer(context.Background(), protocol.Message{Action: "file.download", Params: protocol.Raw(protocol.Args{Path: "large.bin"})})
	if err == nil || calls.Load() != 0 {
		t.Fatal("oversized file transferred", err, calls.Load())
	}
}
