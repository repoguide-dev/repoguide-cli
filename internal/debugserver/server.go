// Package debugserver serves a local HTTP API for inspecting local-mode state.
// Start with --debug :addr on `repoguide mcp serve`.
package debugserver

import (
	"encoding/json"
	"net/http"

	"github.com/repoguide/repoguide-cli/internal/services"
)

// Server wraps local services behind a thin HTTP layer.
type Server struct{ svc *services.Services }

// New creates a Server backed by the given Services.
func New(svc *services.Services) *Server { return &Server{svc} }

// Start registers routes and blocks serving on addr.
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/repos", s.listRepos)
	mux.HandleFunc("GET /api/repos/{id}/topics", s.listTopics)
	mux.HandleFunc("POST /api/repos/{id}/analyze", s.analyze)
	mux.HandleFunc("GET /api/repos/{id}/mcp/topics", s.mcpTopics)
	mux.HandleFunc("GET /api/repos/{id}/mcp/files", s.mcpFiles)
	mux.HandleFunc("GET /api/repos/{id}/mcp/search", s.mcpSearch)
	return http.ListenAndServe(addr, mux)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.svc.Repos.List(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"repos": repos})
}

func (s *Server) listTopics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	topics, err := s.svc.Topics.GetTopics(r.Context(), id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"topics": topics})
}

func (s *Server) analyze(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.svc.Topics.BuildTopics(r.Context(), id); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) mcpTopics(w http.ResponseWriter, r *http.Request) {
	s.listTopics(w, r)
}

func (s *Server) mcpFiles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	path := r.URL.Query().Get("path")
	if path == "" {
		writeJSON(w, 400, map[string]string{"error": "path query param required"})
		return
	}
	f, err := s.svc.Topics.GetFile(r.Context(), id, path)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if f == nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, 200, f)
}

func (s *Server) mcpSearch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := s.svc.Topics.GetSearchData(r.Context(), id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if d == nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, 200, d)
}
