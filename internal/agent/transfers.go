package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"golang.org/x/time/rate"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const maxTransfer = 32 << 20

func (a *Agent) transfer(ctx context.Context, m protocol.Message) (any, error) {
	select {
	case a.transferSlots <- struct{}{}:
		defer func() { <-a.transferSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	args, err := protocol.Validate(m.Action, m.Params)
	if err != nil {
		return nil, err
	}
	url := a.Config.URL + "/agent/transfer/" + m.TaskID
	client := &http.Client{Timeout: 4 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("transfer redirects denied") }}
	var file *os.File
	var size int64
	if m.Action == "file.download" {
		file, err = a.root.OpenFile(args.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		st, e := file.Stat()
		if e != nil || !st.Mode().IsRegular() || st.Size() > a.Config.TransferMaxBytes {
			return nil, errors.New("download requires a regular file within the configured transfer limit")
		}
		size = st.Size()
	}
	var res *http.Response
	for attempt := 0; ; attempt++ {
		var body io.Reader
		method := "GET"
		if file != nil {
			method = "PUT"
			// ReadAt avoids sharing a seek cursor with a transport still closing an old request.
			body = &pacedReader{ctx: ctx, reader: io.NewSectionReader(file, 0, size), limiter: a.transferRate}
		}
		req, e := http.NewRequestWithContext(ctx, method, url, body)
		if e != nil {
			return nil, e
		}
		if file != nil {
			req.ContentLength = size
		}
		req.Header.Set("Authorization", "Bearer "+a.Config.Token)
		req.Header.Set("X-Attempt-ID", m.AttemptID)
		res, err = client.Do(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusTooManyRequests || attempt >= 30 {
			break
		}
		res.Body.Close()
		// 429 means the center rejected admission before reading or publishing bytes.
		// Retry only this transport admission, never the task or filesystem side effect.
		timer := time.NewTimer(time.Second + time.Duration(rand.IntN(250))*time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("transfer rejected: " + res.Status)
	}
	if m.Action == "file.download" {
		return map[string]any{"transferred": true}, nil
	}
	// Write a private sibling first, then create the final name atomically without replacing it.
	temp := filepath.Join(filepath.Dir(args.Path), ".sideria-"+m.AttemptID)
	f, err := a.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	defer a.root.Remove(temp)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(&pacedReader{ctx: ctx, reader: res.Body, limiter: a.transferRate}, a.Config.TransferMaxBytes+1))
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil {
		return nil, err
	}
	if n > a.Config.TransferMaxBytes {
		return nil, errors.New("upload size exceeded")
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if digest != args.ContentSHA256 {
		return nil, errors.New("upload digest mismatch")
	}
	if err = a.root.Link(temp, args.Path); err != nil {
		return nil, err
	}
	dir, err := a.root.Open(filepath.Dir(args.Path))
	if err != nil {
		return nil, ErrUncertain
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return nil, ErrUncertain
	}
	return map[string]any{"bytes": n, "sha256": digest, "path": args.Path}, nil
}

// One limiter is shared by every upload and download on this Agent. Burst is
// bounded to 32 KiB, cancellation interrupts waiting, buffers stay bounded.
type pacedReader struct {
	ctx     context.Context
	reader  io.Reader
	limiter *rate.Limiter
}

func (p *pacedReader) Read(b []byte) (int, error) {
	if len(b) > p.limiter.Burst() {
		b = b[:p.limiter.Burst()]
	}
	if err := p.limiter.WaitN(p.ctx, len(b)); err != nil {
		return 0, err
	}
	return p.reader.Read(b)
}
