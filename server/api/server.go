package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	sse        *SSEManager
	feishu     feishuNotifier
}

// RunDriver is the run lifecycle dependency used by HTTP handlers.
type RunDriver interface {
	Launch(runID uuid.UUID) error
	ResumeRun(runID uuid.UUID) (*engine.RecoveryResult, error)
	Pause(ctx context.Context, runID uuid.UUID) error
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

// WithFeishuNotifier injects a notification sender for waiting-user-input alerts.
func WithFeishuNotifier(notifier feishuNotifier) Option {
	return func(s *Server) {
		s.feishu = notifier
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
		sse:        NewSSEManager(),
		feishu:     newBytedcliFeishuNotifier(),
	}
	for _, opt := range opts {
		opt(srv)
	}
	if srv.driver == nil {
		srv.driver = engine.NewRunDriver(s, ar, eng)
	}
	if srv.store != nil {
		frontendBase := firstNonEmpty(os.Getenv("FRONTEND_BASE_URL"), "http://localhost:5173")
		srv.store.SetStepStatusNotifier(&serverStepNotifier{
			sse:          srv.sse,
			feishu:       srv.feishu,
			frontendBase: frontendBase,
		})
	}
	srv.routes()
	return srv
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("POST /runs", s.handleCreateRun)
	s.mux.HandleFunc("GET /runs", s.handleListRuns)
	s.mux.HandleFunc("GET /runs/{id}", s.handleGetRun)
	s.mux.HandleFunc("POST /runs/{id}/start", s.handleStartRun)
	s.mux.HandleFunc("POST /runs/{id}/pause", s.handlePauseRun)
	s.mux.HandleFunc("POST /runs/{id}/cancel", s.handleCancelRun)
	s.mux.HandleFunc("POST /runs/{id}/resume", s.handleResumeRun)
	s.mux.HandleFunc("GET /runs/{run_id}/events", s.handleSSEEvents)
	s.mux.HandleFunc("GET /runs/{run_id}/pty-sessions", s.handlePTYSessions)
	s.mux.HandleFunc("GET /runs/{run_id}/phase-status", s.handleGetPhaseStatus)
	s.mux.HandleFunc("GET /api/runs/{run_id}/phase-status", s.handleGetPhaseStatus)
	s.mux.HandleFunc("GET /runs/{run_id}/evaluation", s.handleGetEvaluation)
	s.mux.HandleFunc("GET /api/runs/{run_id}/evaluation", s.handleGetEvaluation)
	s.mux.HandleFunc("GET /ws/pty/{session_id}", s.handlePTYWebSocket)
	s.mux.HandleFunc("GET /runs/{id}/steps", s.handleListSteps)
	s.mux.HandleFunc("GET /runs/{id}/tasks", s.handleListTasks)
	s.mux.HandleFunc("GET /runs/{id}/timeline", s.handleTimeline)
	s.mux.HandleFunc("GET /runs/{id}/snapshot", s.handleGetSnapshot)
	s.mux.HandleFunc("POST /admin/cleanup", s.handleCleanup)
	s.mux.HandleFunc("GET /runs/{id}/status", s.handleGetRunStatus)
	s.mux.HandleFunc("GET /runs/{run_id}/nodes/{node}/artifact", s.handleGetNodeArtifact)
	s.mux.HandleFunc("POST /runs/{run_id}/nodes/{node}/feedback", s.handleNodeFeedback)
	s.mux.HandleFunc("POST /runs/{run_id}/nodes/{node}/approve", s.handleNodeApprove)
	s.mux.HandleFunc("POST /runs/{run_id}/nodes/{node}/retry", s.handleNodeRetry)
	s.mux.HandleFunc("GET /runs/{run_id}/nodes/{node}/history", s.handleNodeHistory)
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

type runSummaryResp struct {
	RunID       string `json:"run_id"`
	Status      string `json:"status"`
	CurrentNode string `json:"current_node"`
	CreatedAt   string `json:"created_at"`
}

type listRunsResp struct {
	Runs []runSummaryResp `json:"runs"`
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

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGetPhaseStatus(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	statusPath := filepath.Join(".workflow", "runs", runID, "phase_status.json")
	data, err := os.ReadFile(statusPath)
	if err != nil {
		http.Error(w, "phase status not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) handleGetEvaluation(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	evalPath := filepath.Join(".workflow", "runs", runID, "evaluation.json")
	data, err := os.ReadFile(evalPath)
	if err != nil {
		http.Error(w, "evaluation not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

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
	statusFilter := r.URL.Query().Get("status")
	if limit <= 0 {
		limit = 50
	}

	runs, err := s.store.ListRuns(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]runSummaryResp, 0, len(runs))
	for _, run := range runs {
		if statusFilter != "" && string(run.Status) != statusFilter {
			continue
		}
		currentNode, err := s.currentNodeName(run.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, runSummaryResp{
			RunID:       run.ID.String(),
			Status:      string(run.Status),
			CurrentNode: currentNode,
			CreatedAt:   run.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, listRunsResp{Runs: out})
}

func (s *Server) currentNodeName(runID uuid.UUID) (string, error) {
	steps, err := s.store.GetStepsByRun(runID)
	if err != nil {
		return "", err
	}
	for _, st := range steps {
		if st.Status == domain.StepStatusWaitingUserInput {
			return st.Name, nil
		}
	}
	for _, st := range steps {
		if st.Status == domain.StepStatusRunning {
			return st.Name, nil
		}
	}
	return "", nil
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

// ─── Phase 0: WAITING_USER_INPUT Handlers ─────────────────────────────────────

type nodeStatusResp struct {
	RunID       string     `json:"run_id"`
	Status      string     `json:"status"`
	CurrentNode string     `json:"current_node"`
	Nodes       []nodeInfo `json:"nodes"`
}

type nodeInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Artifact string `json:"artifact,omitempty"`
}

type artifactResp struct {
	NodeID       string `json:"node_id"`
	Content      string `json:"content"`
	ArtifactType string `json:"artifact_type"`
}

type nodeFeedbackReq struct {
	Feedback     string `json:"feedback"`
	FeedbackType string `json:"feedback_type"`
	ToolCallID   string `json:"toolCallId,omitempty"`
}

type feedbackResp struct {
	FeedbackID string `json:"feedback_id"`
	NodeID     string `json:"node_id"`
	Success    bool   `json:"success"`
}

type approveReq struct {
	Comment    string `json:"comment,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
}

type approveResp struct {
	NodeID     string `json:"node_id"`
	NextNodeID string `json:"next_node_id,omitempty"`
	RunStatus  string `json:"run_status"`
	Success    bool   `json:"success"`
}

type retryReq struct {
	Reason     string `json:"reason,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
}

type retryResp struct {
	NodeID  string `json:"node_id"`
	JobID   string `json:"job_id,omitempty"`
	Status  string `json:"status"`
	Success bool   `json:"success"`
}

type historyResp struct {
	NodeID string         `json:"node_id"`
	Runs   []historyEntry `json:"runs"`
}

type historyEntry struct {
	RunID       string     `json:"run_id"`
	Status      string     `json:"status"`
	StartedAt   string     `json:"started_at"`
	CompletedAt string     `json:"completed_at,omitempty"`
	Feedback    []feedback `json:"feedback,omitempty"`
}

type feedback struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	SubmittedAt string `json:"submitted_at"`
}

func (s *Server) handleGetRunStatus(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(r, "id")
	run, err := s.store.GetRun(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	steps, err := s.store.GetStepsByRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	nodes := make([]nodeInfo, len(steps))
	currentNode := ""
	for i, st := range steps {
		nodeID := fmt.Sprintf("node_%d", i+1)
		if st.Status == domain.StepStatusWaitingUserInput || st.Status == domain.StepStatusRunning {
			currentNode = nodeID
		}
		status := string(st.Status)
		if st.Status == domain.StepStatusCompleted {
			status = "COMPLETED"
		}
		nodes[i] = nodeInfo{
			ID:     nodeID,
			Name:   st.Name,
			Status: status,
		}
	}

	resp := nodeStatusResp{
		RunID:       run.ID.String(),
		Status:      string(run.Status),
		CurrentNode: currentNode,
		Nodes:       nodes,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetNodeArtifact(w http.ResponseWriter, r *http.Request) {
	runIDStr := r.PathValue("run_id")
	nodeIDStr := r.PathValue("node")

	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run_id")
		return
	}

	// Parse node number from node_1, node_2, etc.
	nodeNum := 1
	if _, err := fmt.Sscanf(nodeIDStr, "node_%d", &nodeNum); err != nil {
		// Try direct parse
		nodeNum, _ = strconv.Atoi(nodeIDStr)
	}

	steps, err := s.store.GetStepsByRun(runID)
	if err != nil || nodeNum < 1 || nodeNum > len(steps) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	step := steps[nodeNum-1]
	artifacts, err := s.store.GetArtifactsByRun(runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Find artifact for this step
	var content string
	artifactType := "text"
	for _, a := range artifacts {
		if a.StepID == step.ID {
			content = a.Content
			artifactType = a.Type
			break
		}
	}

	resp := artifactResp{
		NodeID:       nodeIDStr,
		Content:      content,
		ArtifactType: artifactType,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNodeFeedback(w http.ResponseWriter, r *http.Request) {
	runIDStr := r.PathValue("run_id")
	nodeIDStr := r.PathValue("node")

	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run_id")
		return
	}

	nodeNum := 1
	if _, err := fmt.Sscanf(nodeIDStr, "node_%d", &nodeNum); err != nil {
		nodeNum, _ = strconv.Atoi(nodeIDStr)
	}

	steps, err := s.store.GetStepsByRun(runID)
	if err != nil || nodeNum < 1 || nodeNum > len(steps) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	var req nodeFeedbackReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.FeedbackType == "" {
		req.FeedbackType = "correction"
	}

	// Create feedback artifact
	feedbackUUID := uuid.New()
	artifact := &store.Artifact{
		ID:     feedbackUUID,
		RunID:  runID,
		StepID: steps[nodeNum-1].ID,
		Name:   "feedback",
		Type:   "json",
		Content: fmt.Sprintf(`{"feedback":"%s","type":"%s","submitted_at":"%s"}`,
			req.Feedback, req.FeedbackType, time.Now().Format(time.RFC3339)),
	}

	if err := s.store.CreateArtifact(artifact); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := feedbackResp{
		FeedbackID: feedbackUUID.String(),
		NodeID:     nodeIDStr,
		Success:    true,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNodeApprove(w http.ResponseWriter, r *http.Request) {
	runIDStr := r.PathValue("run_id")
	nodeIDStr := r.PathValue("node")

	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run_id")
		return
	}

	nodeNum := 1
	if _, err := fmt.Sscanf(nodeIDStr, "node_%d", &nodeNum); err != nil {
		nodeNum, _ = strconv.Atoi(nodeIDStr)
	}

	steps, err := s.store.GetStepsByRun(runID)
	if err != nil || nodeNum < 1 || nodeNum > len(steps) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	// Update step status to completed
	step := steps[nodeNum-1]
	step.Status = domain.StepStatusCompleted
	if err := s.store.UpdateStep(step); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Find next node
	nextNodeID := ""
	if nodeNum < len(steps) {
		nextNodeID = fmt.Sprintf("node_%d", nodeNum+1)
		// Start the next node
		nextStep := steps[nodeNum]
		nextStep.Status = domain.StepStatusRunning
		if err := s.store.UpdateStep(nextStep); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	resp := approveResp{
		NodeID:     nodeIDStr,
		NextNodeID: nextNodeID,
		RunStatus:  "running",
		Success:    true,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNodeRetry(w http.ResponseWriter, r *http.Request) {
	runIDStr := r.PathValue("run_id")
	nodeIDStr := r.PathValue("node")

	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run_id")
		return
	}

	nodeNum := 1
	if _, err := fmt.Sscanf(nodeIDStr, "node_%d", &nodeNum); err != nil {
		nodeNum, _ = strconv.Atoi(nodeIDStr)
	}

	steps, err := s.store.GetStepsByRun(runID)
	if err != nil || nodeNum < 1 || nodeNum > len(steps) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	// Update step status to running (retry)
	step := steps[nodeNum-1]
	step.Status = domain.StepStatusRunning
	step.Attempt++
	if err := s.store.UpdateStep(step); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jobID := uuid.New().String()
	resp := retryResp{
		NodeID:  nodeIDStr,
		JobID:   jobID,
		Status:  "RETRYING",
		Success: true,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNodeHistory(w http.ResponseWriter, r *http.Request) {
	runIDStr := r.PathValue("run_id")
	nodeIDStr := r.PathValue("node")

	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run_id")
		return
	}

	nodeNum := 1
	if _, err := fmt.Sscanf(nodeIDStr, "node_%d", &nodeNum); err != nil {
		nodeNum, _ = strconv.Atoi(nodeIDStr)
	}

	steps, err := s.store.GetStepsByRun(runID)
	if err != nil || nodeNum < 1 || nodeNum > len(steps) {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}

	step := steps[nodeNum-1]
	artifacts, err := s.store.GetArtifactsByRun(runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Build history from artifacts
	var entries []historyEntry
	var fbList []feedback
	for _, a := range artifacts {
		if a.StepID == step.ID {
			if a.Name == "feedback" {
				fbList = append(fbList, feedback{
					ID:          a.ID.String(),
					Content:     a.Content,
					SubmittedAt: a.CreatedAt.Format(time.RFC3339),
				})
			}
		}
	}

	entries = append(entries, historyEntry{
		RunID:       fmt.Sprintf("attempt_%d", step.Attempt),
		Status:      string(step.Status),
		StartedAt:   step.CreatedAt.Format(time.RFC3339),
		CompletedAt: step.UpdatedAt.Format(time.RFC3339),
		Feedback:    fbList,
	})

	resp := historyResp{
		NodeID: nodeIDStr,
		Runs:   entries,
	}
	writeJSON(w, http.StatusOK, resp)
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
	return s.ListenContext(context.Background(), addr)
}

// ListenContext starts the HTTP server and gracefully shuts down when ctx is done.
func (s *Server) ListenContext(ctx context.Context, addr string) error {
	if err := ctx.Err(); err != nil {
		return nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: addr, Handler: s.mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
