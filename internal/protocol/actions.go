package protocol

import (
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

type Args struct {
	ContentSHA256 string `json:"content_sha256,omitempty"`
	Path          string `json:"path,omitempty"`
	Container     string `json:"container,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	Offset        int    `json:"offset,omitempty"`
}
type Action struct {
	Capability string
	Task       bool
	Write      bool
	AI         bool
}

var Actions = map[string]Action{
	"file.list": {Capability: "files"}, "file.read": {Capability: "files"},
	"file.upload": {Capability: "files", Task: true, Write: true}, "file.download": {Capability: "files", Task: true},
	"git.status": {Capability: "git", AI: true}, "git.diff": {Capability: "git"},
	"docker.list": {Capability: "docker", AI: true}, "docker.inspect": {Capability: "docker", AI: true},
	"docker.stats": {Capability: "docker", AI: true}, "docker.logs": {Capability: "docker", AI: true},
	"docker.start": {Capability: "docker", Task: true, Write: true}, "docker.stop": {Capability: "docker", Task: true, Write: true}, "docker.restart": {Capability: "docker", Task: true, Write: true},
}
var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Validate(action string, raw json.RawMessage) (Args, error) {
	var a Args
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return a, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return a, errors.New("trailing action arguments")
	}
	if len(a.Path) > 4096 {
		return a, errors.New("path too long")
	}
	if _, ok := Actions[action]; !ok {
		return a, errors.New("action unavailable")
	}
	if strings.HasPrefix(action, "file.") || strings.HasPrefix(action, "git.") {
		if a.Path == "" || filepath.Clean(a.Path) != a.Path || filepath.IsAbs(a.Path) || !filepath.IsLocal(a.Path) || strings.ContainsRune(a.Path, 0) {
			return a, errors.New("path must be relative to authorized root")
		}
	}
	if strings.HasPrefix(action, "docker.") && action != "docker.list" && !containerID.MatchString(a.Container) {
		return a, errors.New("full 64-character container ID required")
	}
	if a.Offset < 0 || a.Offset > 100000 {
		return a, errors.New("invalid offset")
	}
	return a, nil
}
