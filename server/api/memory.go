package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/ming-agents/server/memory"
)

func (s *Server) registerMemoryRoutes() {
	s.mux.HandleFunc("POST /api/memory/ingest", s.handleMemoryIngest)
	s.mux.HandleFunc("GET /api/memory/recall", s.handleMemoryRecall)
	s.mux.HandleFunc("GET /api/memory/brief", s.handleMemoryBrief)
	s.mux.HandleFunc("POST /api/memory/feedback", s.handleMemoryFeedback)
	s.mux.HandleFunc("POST /api/memory/implicit", s.handleMemoryImplicit)
	s.mux.HandleFunc("POST /api/memory/cleanup", s.handleMemoryCleanup)
	s.mux.HandleFunc("GET /api/memory/stats", s.handleMemoryStats)
}

type ingestReq struct {
	Content string   `json:"content"`
	Type    string   `json:"type"`
	Project string   `json:"project"`
	Tags    []string `json:"tags"`
	Source  string   `json:"source"`
	Title   string   `json:"title"`
}

func (s *Server) handleMemoryIngest(w http.ResponseWriter, r *http.Request) {
	var req ingestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	res, err := memory.Ingest(req.Content, req.Type, req.Project, req.Tags, req.Source, req.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) handleMemoryRecall(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	minScore, _ := strconv.ParseFloat(q.Get("min_score"), 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit == 0 {
		limit = 10
	}
	results, total, err := memory.Recall(
		q.Get("query"), q.Get("project"), q.Get("type"),
		splitCSV(q.Get("tags")), minScore, q.Get("status"), limit,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if results == nil {
		results = []memory.Memory{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "total": total})
}

func (s *Server) handleMemoryBrief(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	maxTokens, _ := strconv.Atoi(q.Get("max_tokens"))
	block, audit, err := memory.Brief(q.Get("project"), q.Get("query"), memory.Budget{MaxTokens: maxTokens})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"block": block, "audit": audit})
}

type feedbackReq struct {
	ID      string `json:"id"`
	Used    bool   `json:"used"`
	Helpful bool   `json:"helpful"`
}

func (s *Server) handleMemoryFeedback(w http.ResponseWriter, r *http.Request) {
	var req feedbackReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	res, err := memory.Feedback(req.ID, req.Used, req.Helpful)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type implicitReq struct {
	IDs []string `json:"ids"`
	Log string   `json:"log"`
}

func (s *Server) handleMemoryImplicit(w http.ResponseWriter, r *http.Request) {
	var req implicitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := memory.ImplicitFeedback(req.IDs, req.Log)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleMemoryCleanup(w http.ResponseWriter, r *http.Request) {
	res, err := memory.Cleanup()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleMemoryStats(w http.ResponseWriter, r *http.Request) {
	total, active, archived, superseded, byType, err := memory.Stats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":      total,
		"active":     active,
		"archived":   archived,
		"superseded": superseded,
		"by_type":    byType,
	})
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}
