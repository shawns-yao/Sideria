package agent

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// A small Engine adapter uses documented endpoints and negotiates the daemon API version.
// It does not invoke a shell or forward daemon authentication to the browser.
func (a *Agent) docker(ctx context.Context, action, id string) (any, error) {
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", a.Config.DockerSocket)
	}}}
	defer client.CloseIdleConnections()
	call := func(method, path string) ([]byte, error) {
		req, e := http.NewRequestWithContext(ctx, method, "http://docker"+path, nil)
		if e != nil {
			return nil, e
		}
		res, e := client.Do(req)
		if e != nil {
			return nil, errors.New("Docker socket unavailable")
		}
		defer res.Body.Close()
		b, e := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if e != nil {
			return nil, e
		}
		if len(b) >= 1<<20 {
			return nil, errors.New("Docker response budget exceeded")
		}
		if res.StatusCode < 200 || res.StatusCode > 299 {
			return nil, errors.New("Docker operation rejected: " + res.Status)
		}
		return b, nil
	}
	b, err := call("GET", "/version")
	if err != nil {
		return nil, err
	}
	var ver struct {
		APIVersion string `json:"ApiVersion"`
	}
	if json.Unmarshal(b, &ver) != nil || !strings.HasPrefix(ver.APIVersion, "1.") {
		return nil, errors.New("unsupported Docker API")
	}
	prefix := "/v" + ver.APIVersion
	// Reject orchestrated resources rather than manipulating Kubernetes-owned containers.
	if id != "" {
		b, err = call("GET", prefix+"/containers/"+id+"/json")
		if err != nil {
			return nil, err
		}
		var x struct {
			Config struct{ Labels map[string]string }
		}
		json.Unmarshal(b, &x)
		for k := range x.Config.Labels {
			if strings.HasPrefix(k, "io.kubernetes.") {
				return nil, errors.New("Kubernetes-managed resource denied")
			}
		}
	}
	path := prefix + "/containers/" + id
	method := "GET"
	switch action {
	case "docker.list":
		path = prefix + "/containers/json?all=1"
	case "docker.inspect":
		path += "/json"
	case "docker.stats":
		path += "/stats?stream=false&one-shot=true"
	case "docker.logs":
		path += "/logs?stdout=true&stderr=true&tail=100&timestamps=true"
	case "docker.start", "docker.stop", "docker.restart":
		method = "POST"
		path += "/" + strings.TrimPrefix(action, "docker.") + "?t=10"
	default:
		return nil, errors.New("unsupported Docker action")
	}
	b, err = call(method, path)
	if err != nil {
		if method == "POST" {
			return nil, fmt.Errorf("%w: %v", ErrUncertain, err)
		}
		return nil, err
	}
	if action == "docker.logs" {
		var out []byte
		for len(b) >= 8 && (b[0] == 0 || b[0] == 1 || b[0] == 2) && b[1] == 0 && b[2] == 0 && b[3] == 0 {
			n := int(binary.BigEndian.Uint32(b[4:8]))
			if n > len(b)-8 {
				break
			}
			out = append(out, b[8:8+n]...)
			b = b[8+n:]
		}
		out = append(out, b...)
		truncated := len(out) > PreviewLimit
		if truncated {
			out = out[:PreviewLimit]
		}
		return map[string]any{"logs": string(out), "truncated": truncated}, nil
	}
	if method == "POST" {
		after, e := call("GET", prefix+"/containers/"+id+"/json")
		if e != nil {
			return nil, fmt.Errorf("%w: %v", ErrUncertain, e)
		}
		var state map[string]json.RawMessage
		json.Unmarshal(after, &state)
		return map[string]any{"state": state["State"], "business_health": "not verified"}, nil
	}
	// Inspect allowlist deliberately omits environment variables and registry metadata.
	if action == "docker.inspect" {
		var x map[string]json.RawMessage
		if err = json.Unmarshal(b, &x); err != nil {
			return nil, err
		}
		return map[string]any{"Id": x["Id"], "Name": x["Name"], "State": x["State"], "Mounts": x["Mounts"], "NetworkSettings": x["NetworkSettings"]}, nil
	}
	var result any
	err = json.Unmarshal(b, &result)
	return result, err
}
