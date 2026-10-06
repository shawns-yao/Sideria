package agent

import (
	"fmt"
	"net/url"
	"strings"
)

func gitSummary(status string) map[string]any {
	out := map[string]any{"branch": "", "head": "", "upstream": "", "ahead": nil, "behind": nil, "detached": false, "empty": false}
	staged, unstaged, untracked, conflicts := 0, 0, 0, 0
	for _, line := range strings.Split(status, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			branch := strings.TrimPrefix(line, "# branch.head ")
			out["branch"] = branch
			out["detached"] = branch == "(detached)"
		case strings.HasPrefix(line, "# branch.oid "):
			head := strings.TrimPrefix(line, "# branch.oid ")
			out["head"] = head
			out["empty"] = head == "(initial)"
		case strings.HasPrefix(line, "# branch.upstream "):
			out["upstream"] = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			var ahead, behind int
			if _, err := fmt.Sscanf(line, "# branch.ab +%d -%d", &ahead, &behind); err == nil {
				out["ahead"] = ahead
				out["behind"] = behind
			}
		case strings.HasPrefix(line, "? "):
			untracked++
		case strings.HasPrefix(line, "u "):
			conflicts++
		case strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 "):
			if len(line) >= 4 {
				if line[2] != '.' {
					staged++
				}
				if line[3] != '.' {
					unstaged++
				}
			}
		}
	}
	out["staged"] = staged
	out["unstaged"] = unstaged
	out["untracked"] = untracked
	out["conflicts"] = conflicts
	out["dirty"] = staged+unstaged+untracked+conflicts > 0
	return out
}
func remoteSummary(config string) []map[string]string {
	out := []map[string]string{}
	for _, line := range strings.Split(config, "\n") {
		fields := strings.SplitN(line, " ", 2)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(fields[0], "remote."), ".url")
		raw := fields[1]
		safe := "[local or non-URL remote]"
		if u, e := url.Parse(raw); e == nil && u.Host != "" {
			u.User = nil
			u.RawQuery = ""
			u.Fragment = ""
			safe = u.String()
		} else if strings.Contains(raw, "@") && strings.Contains(raw, ":") {
			hostpath := strings.SplitN(raw, "@", 2)[1]
			if !strings.ContainsAny(hostpath, "?# ") {
				safe = hostpath
			}
		}
		out = append(out, map[string]string{"name": name, "location": safe})
	}
	return out
}
