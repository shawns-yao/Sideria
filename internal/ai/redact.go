package ai

import (
	"encoding/json"
	"regexp"
	"strings"
)

var secretField = regexp.MustCompile(`(?i)(password|passwd|secret|token|authorization|api.?key|private.?key|credential)`)
var pem = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
var keyPattern = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9]{20,})\b`)

// Sanitize retains JSON validity and redacts keys as well as text. It is defense in
// depth, not permission to export arbitrary files: the model tool allowlist excludes them.
func Sanitize(raw json.RawMessage) json.RawMessage {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return json.RawMessage(`{"error":"invalid structured tool result"}`)
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if secretField.MatchString(k) {
					x[k] = "[REDACTED]"
				} else {
					x[k] = walk(v)
				}
			}
			return x
		case []any:
			for i, v := range x {
				x[i] = walk(v)
			}
			return x
		case string:
			return Redact(x)
		default:
			return v
		}
	}
	b, _ := json.Marshal(walk(value))
	return b
}
func redactJSONText(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil && (strings.HasPrefix(strings.TrimSpace(s), "{") || strings.HasPrefix(strings.TrimSpace(s), "[")) {
		return string(Sanitize(json.RawMessage(s)))
	}
	return s
}
