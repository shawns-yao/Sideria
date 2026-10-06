package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/shawns-yao/Sideria/internal/protocol"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const maxTransfer = 32 << 20

func (a *Agent) transfer(ctx context.Context, m protocol.Message) (any, error) {
	args, err := protocol.Validate(m.Action, m.Params)
	if err != nil {
		return nil, err
	}
	url := a.Config.URL + "/agent/transfer/" + m.TaskID
	client := &http.Client{Timeout: 4 * time.Minute}
	var req *http.Request
	var file *os.File
	if m.Action == "file.upload" {
		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
	} else {
		file, err = a.root.OpenFile(args.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		st, e := file.Stat()
		if e != nil || !st.Mode().IsRegular() || st.Size() > maxTransfer {
			return nil, errors.New("download requires a regular file no larger than 32 MiB")
		}
		req, err = http.NewRequestWithContext(ctx, "PUT", url, io.LimitReader(file, maxTransfer+1))
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Config.Token)
	req.Header.Set("X-Attempt-ID", m.AttemptID)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
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
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, maxTransfer+1))
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil {
		return nil, err
	}
	if n > maxTransfer {
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
