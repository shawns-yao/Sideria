package server

import (
	"github.com/shawns-yao/Sideria/internal/protocol"
	"net/http"
	"strings"
)

func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT id,host_id,path,name FROM projects ORDER BY name LIMIT 500`)
	if err != nil {
		fail(w, 503, "database unavailable")
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, host, path, name string
		if rows.Scan(&id, &host, &path, &name) != nil {
			fail(w, 500, "project read failed")
			return
		}
		out = append(out, map[string]string{"id": id, "host_id": host, "path": path, "name": name})
	}
	reply(w, 200, out)
}
func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if !s.limited(w, r, "project", 30) {
		return
	}
	var b struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if !decode(w, r, &b) {
		return
	}
	if strings.TrimSpace(b.Name) == "" || len(b.Name) > 120 {
		fail(w, 400, "invalid project name")
		return
	}
	host := r.PathValue("host")
	m, err := s.request(r.Context(), host, "git.status", protocol.Raw(protocol.Args{Path: b.Path}))
	if err != nil || m.Error != "" {
		fail(w, 400, "repository could not be verified within authorized root")
		return
	}
	id := protocol.ID()
	err = s.Store.Pool.QueryRow(r.Context(), `INSERT INTO projects(id,host_id,path,name) VALUES($1,$2,$3,$4) ON CONFLICT(host_id,path) DO UPDATE SET name=excluded.name RETURNING id`, id, host, b.Path, b.Name).Scan(&id)
	if err != nil {
		fail(w, 503, "database unavailable")
		return
	}
	reply(w, 201, map[string]string{"id": id})
}
