package api

import (
	"encoding/json"
	"errors"
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
	s.mux.HandleFunc("GET /api/memory/conflicts", s.handleMemoryConflicts)
	s.mux.HandleFunc("POST /api/memory/resolve", s.handleMemoryResolve)
	s.mux.HandleFunc("POST /api/memory/unsupersede", s.handleMemoryUnsupersede)
}

// memoryReadLimiter and memoryWriteLimiter throttle the contradiction endpoints
// (§3.7): reads at 60/min, writes at 10/min with a small burst. They are shared
// package-level buckets so all requests for an endpoint share the budget.
var (
	memoryReadLimiter  = newTokenBucket(60, 10)
	memoryWriteLimiter = newTokenBucket(10, 5)
)

// actorEnvelope is the shared JSON shape carrying the operator identity on the
// contradiction write endpoints.
type actorEnvelope struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// requireHumanActor resolves the operator identity from the request body's actor
// field (preferred) or the X-Actor header (fallback), enforcing a named human
// actor for auditability. It mirrors the phase-7 promotion contract.
func requireHumanActor(r *http.Request, body actorEnvelope) (memory.PromotionActor, error) {
	kind, name := body.Kind, strings.TrimSpace(body.Name)
	if name == "" {
		if h := strings.TrimSpace(r.Header.Get("X-Actor")); h != "" {
			kind, name = "human", h
		}
	}
	if kind != "human" || name == "" {
		return memory.PromotionActor{}, errHumanActorRequired
	}
	return memory.PromotionActor{Kind: "human", Name: name}, nil
}

var errHumanActorRequired = errors.New("human actor required")

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

// checkRateLimit enforces the token bucket unless disabled via env, writing a
// 429 with a Retry-After hint on rejection. It returns false when the request
// was already answered (rate limited).
func checkRateLimit(w http.ResponseWriter, b *tokenBucket) bool {
	if ratelimitDisabled() {
		return true
	}
	if ok, retry := b.allow(); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":         "rate limit exceeded",
			"retry_after_s": retry,
		})
		return false
	}
	return true
}

func (s *Server) handleMemoryConflicts(w http.ResponseWriter, r *http.Request) {
	if !checkRateLimit(w, memoryReadLimiter) {
		return
	}
	q := r.URL.Query()
	minConf, _ := strconv.ParseFloat(q.Get("min_confidence"), 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, err := memory.ListConflicts(memory.ListConflictFilter{
		Project:       q.Get("project"),
		Source:        q.Get("source"),
		MinConfidence: minConf,
		Action:        q.Get("action"),
		Limit:         limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []memory.ResolutionResult{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(items), "items": items})
}

type resolveReq struct {
	Pair     []string      `json:"pair"`
	All      bool          `json:"all"`
	Project  string        `json:"project"`
	Evict    bool          `json:"evict"`
	Apply    bool          `json:"apply"`
	MaxPairs int           `json:"max_pairs"`
	IKnow    bool          `json:"i_know"`
	Actor    actorEnvelope `json:"actor"`
	Source   string        `json:"source_filter"`
}

func (s *Server) handleMemoryResolve(w http.ResponseWriter, r *http.Request) {
	if !checkRateLimit(w, memoryWriteLimiter) {
		return
	}
	var req resolveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	spec := memory.ResolveSpec{
		All:          req.All,
		Project:      req.Project,
		Evict:        req.Evict,
		Apply:        req.Apply,
		MaxPairs:     req.MaxPairs,
		IKnow:        req.IKnow,
		SourceFilter: req.Source,
	}
	if len(req.Pair) == 2 {
		spec.Pair = [2]string{req.Pair[0], req.Pair[1]}
	}
	// A write apply must be attributed to a human actor.
	if req.Apply {
		actor, err := requireHumanActor(r, req.Actor)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		spec.Actor = actor
	}
	summary, err := memory.RunResolve(spec)
	if err != nil {
		writeError(w, resolveErrStatus(err), err.Error())
		return
	}
	mode := "apply"
	if summary.DryRun {
		mode = "dry-run"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":    mode,
		"results": summary.Results,
		"summary": map[string]int{
			"superseded": summary.Superseded,
			"flagged":    summary.Flagged,
			"skipped":    summary.Skipped,
		},
	})
}

type unsupersedeReq struct {
	ID     string        `json:"id"`
	Apply  bool          `json:"apply"`
	Reason string        `json:"reason"`
	Actor  actorEnvelope `json:"actor"`
}

func (s *Server) handleMemoryUnsupersede(w http.ResponseWriter, r *http.Request) {
	if !checkRateLimit(w, memoryWriteLimiter) {
		return
	}
	var req unsupersedeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if !req.Apply {
		writeJSON(w, http.StatusOK, map[string]any{
			"id":      req.ID,
			"dry_run": true,
		})
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusBadRequest, "unsupersede apply requires reason")
		return
	}
	actor, err := requireHumanActor(r, req.Actor)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	restored, err := memory.Unsupersede(req.ID, req.Reason, actor)
	if err != nil {
		writeError(w, resolveErrStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         restored.ID,
		"from_state": memory.PromotionSuperseded,
		"to_state":   restored.PromotionState,
		"dry_run":    false,
	})
}

// resolveErrStatus maps a memory-layer error to an HTTP status per §3.5.
func resolveErrStatus(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case strings.Contains(msg, "not active"):
		return http.StatusNotFound
	case strings.Contains(msg, "is not allowed"):
		return http.StatusConflict
	case strings.Contains(msg, "--max-pairs") || strings.Contains(msg, "refused:"):
		return http.StatusUnprocessableEntity
	case strings.Contains(msg, "requires") || strings.Contains(msg, "mutually exclusive"):
		return http.StatusBadRequest
	case strings.Contains(msg, "l1 memory must be curated"):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
