package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/codegraph"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/engine"
	"github.com/ming-agents/server/eval"
	"github.com/ming-agents/server/store"
)

// Server is the HTTP API server for run orchestration.
// Epic 4.4: Run编排API — HTTP 创建/启动/取消/查询 Run.
type Server struct {
	store      *store.Store
	engine     *engine.Engine
	driver     RunDriver
	pm         *engine.PersistenceManager
	adapterReg *adapter.Registry
	evalReg    *eval.Registry
	mux        *http.ServeMux
	graph      *codegraph.RepoGraph
	pgxPool    *pgxpool.Pool
}

// RunDriver is the run lifecycle dependency used by HTTP handlers.
type RunDriver interface {
	Launch(runID uuid.UUID) error
	ResumeRun(runID uuid.UUID) (*engine.RecoveryResult, error)
}

// Option configures optional API modules.
type Option func(*Server)

// WithCodeGraph registers CodeGraph routes backed by the given graph and pool.
func WithCodeGraph(graph *codegraph.RepoGraph, pool *pgxpool.Pool) Option {
	return func(s *Server) {
		s.graph = graph
		s.pgxPool = pool
	}
}

// WithRunDriver injects the run lifecycle driver used by start/resume routes.
func WithRunDriver(driver RunDriver) Option {
	return func(s *Server) {
		s.driver = driver
	}
}

// NewServer creates a new API server.
func NewServer(s *store.Store, eng *engine.Engine, ar *adapter.Registry, er *eval.Registry, opts ...Option) *Server {
	srv := &Server{
		store:      s,
		engine:     eng,
		pm:         engine.NewPersistenceManager(s),
		adapterReg: ar,
		evalReg:    er,
		mux:        http.NewServeMux(),
	}
	for _, opt := range opts {
		opt(srv)
	}
	if srv.driver == nil {
		srv.driver = engine.NewRunDriver(s, ar, eng)
	}
	srv.routes()
	return srv
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /runs", s.handleCreateRun)
	s.mux.HandleFunc("GET /runs", s.handleListRuns)
	s.mux.HandleFunc("GET /runs/{id}", s.handleGetRun)
	s.mux.HandleFunc("POST /runs/{id}/start", s.handleStartRun)
	s.mux.HandleFunc("POST /runs/{id}/pause", s.handlePauseRun)
	s.mux.HandleFunc("POST /runs/{id}/cancel", s.handleCancelRun)
	s.mux.HandleFunc("POST /runs/{id}/resume", s.handleResumeRun)
	s.mux.HandleFunc("GET /runs/{id}/steps", s.handleListSteps)
	s.mux.HandleFunc("GET /runs/{id}/tasks", s.handleListTasks)
	s.mux.HandleFunc("GET /runs/{id}/timeline", s.handleTimeline)
	s.mux.HandleFunc("GET /runs/{id}/snapshot", s.handleGetSnapshot)
	s.mux.HandleFunc("POST /admin/cleanup", s.handleCleanup)
	s.registerMemoryRoutes()
	if s.graph != nil && s.pgxPool != nil {
		NewGraphHandler(s.pgxPool, s.graph).RegisterRoutes(s.mux)
	}
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ─── Request/Response types ────────────────────────────────────────────────────

type createRunReq struct {
	Name        string `json:"name"`
	WDLSource   string `json:"wdl_source"`
	MaxParallel int    `json:"max_parallel"`
}

type runResp struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	MaxParallel int    `json:"max_parallel"`
	CreatedAt   string `json:"created_at"`
}

type stepResp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"step_type"`
	Status    string `json:"status"`
	Iteration int    `json:"iteration"`
	Attempt   int    `json:"attempt"`
}

type taskResp struct {
	ID      string `json:"id"`
	StepID  string `json:"step_id"`
	Status  string `json:"status"`
	Summary string `json:"result_summary,omitempty"`
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req createRunReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		req.Name = "run-" + uuid.New().String()[:8]
	}
	if req.MaxParallel <= 0 {
		req.MaxParallel = 4
	}

	if req.WDLSource == "" {
		writeError(w, http.StatusBadRequest, "wdl_source is required")
		return
	}

	// Compile WDL into a run plan.
	compileRes, err := s.engine.Compile(req.WDLSource)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("compile WDL: %v", err))
		return
	}

	// Override name and parallelism.
	compileRes.Run.Name = req.Name
	compileRes.Run.MaxParallel = req.MaxParallel

	writeJSON(w, http.StatusCreated, runResp{
		ID:          compileRes.Run.ID.String(),
		Name:        compileRes.Run.Name,
		Status:      string(compileRes.Run.Status),
		MaxParallel: compileRes.Run.MaxParallel,
		CreatedAt:   compileRes.Run.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 50
	}

	runs, err := s.store.ListRuns(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]runResp, len(runs))
	for i, run := range runs {
		out[i] = runResp{
			ID:          run.ID.String(),
			Name:        run.Name,
			Status:      string(run.Status),
			MaxParallel: run.MaxParallel,
			CreatedAt:   run.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(r, "id")
	run, err := s.store.GetRun(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(r, "id")
	if err := s.driver.Launch(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "running", "run_id": id.String()})
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(r, "id")
	run, err := s.store.GetRun(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	run.Status = domain.RunStatusCancelled
	if err := s.store.UpdateRun(run); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Server) handlePauseRun(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(r, "id")
	timeout := 30 * time.Second
	if v := r.URL.Query().Get("timeout_ms"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms <= 0 {
			writeError(w, http.StatusBadRequest, "timeout_ms must be a positive integer")
			return
		}
		timeout = time.Duration(ms) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	if err := s.driver.Pause(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (s *Server) handleListSteps(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(r, "id")
	steps, err := s.store.GetStepsByRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]stepResp, len(steps))
	for i, st := range steps {
		out[i] = stepResp{
			ID:        st.ID.String(),
			Name:      st.Name,
			Type:      string(st.StepType),
			Status:    string(st.Status),
			Iteration: st.Iteration,
			Attempt:   st.Attempt,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(r, "id")
	tasks, err := s.store.GetTasksByRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]taskResp, len(tasks))
	for i, t := range tasks {
		out[i] = taskResp{
			ID:      t.ID.String(),
			StepID:  t.StepID.String(),
			Status:  string(t.Status),
			Summary: t.ResultSummaryStr,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(r, "id")

	steps, err := s.store.GetStepsByRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tasks, err := s.store.GetTasksByRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type timelineEvent struct {
		Type    string `json:"type"`
		StepID  string `json:"step_id,omitempty"`
		TaskID  string `json:"task_id,omitempty"`
		Time    string `json:"time"`
		Status  string `json:"status"`
		Details string `json:"details,omitempty"`
	}

	var timeline []timelineEvent
	for _, st := range steps {
		timeline = append(timeline, timelineEvent{
			Type:   "step",
			StepID: st.ID.String(),
			Time:   st.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			Status: string(st.Status),
		})
	}
	for _, t := range tasks {
		e := timelineEvent{
			Type:   "task",
			StepID: t.StepID.String(),
			TaskID: t.ID.String(),
			Status: string(t.Status),
		}
		if t.ClaimedAt.Valid {
			e.Time = t.ClaimedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		}
		if t.CompletedAt.Valid {
			e.Time = t.CompletedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		}
		timeline = append(timeline, e)
	}

	writeJSON(w, http.StatusOK, timeline)
}

func (s *Server) handleResumeRun(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(r, "id")
	result, err := s.driver.ResumeRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(r, "id")
	result, err := s.pm.RecoverRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleCleanup manually triggers retention cleanup of agent_task_queue and
// loop_iterations for terminal runs older than the retention period. The period
// defaults to 7 days and can be overridden with ?retention_days=N.
func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	cfg := store.DefaultCleanupConfig()
	if v := r.URL.Query().Get("retention_days"); v != "" {
		days, err := strconv.Atoi(v)
		if err != nil || days < 0 {
			writeError(w, http.StatusBadRequest, "retention_days must be a non-negative integer")
			return
		}
		cfg.Retention = time.Duration(days) * 24 * time.Hour
	}

	res, err := s.store.CleanupExpired(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────────

func parseUUID(r *http.Request, key string) uuid.UUID {
	id, err := uuid.Parse(r.PathValue(key))
	if err != nil {
		return uuid.Nil
	}
	return id
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// Listen starts the HTTP server.
func (s *Server) Listen(addr string) error {
	return http.ListenAndServe(addr, s.mux)
}
