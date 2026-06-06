package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shrimp-mvp/server/agent"
	"github.com/shrimp-mvp/server/api"
	"github.com/shrimp-mvp/server/codegraph"
	"github.com/shrimp-mvp/server/task"
)

// Server exposes the REST API and the WebSocket stream.
type Server struct {
	daemon *Daemon
	queue  *task.Queue
	reg *agent.Registry
	bus    *EventBus
	up     websocket.Upgrader
	codegraph *codegraph.CodeGraphCLI
	registry  *codegraph.RepoGraph
}

func NewServer(d *Daemon, q *task.Queue, reg *agent.Registry, bus *EventBus, cg *codegraph.CodeGraphCLI, reg2 *codegraph.RepoGraph) *Server {
	return &Server{
		daemon: d,
		queue:  q,
		reg:    reg,
		bus:    bus,
		up: websocket.Upgrader{
			// MVP: allow any origin (the console runs on a different port).
			CheckOrigin: func(*http.Request) bool { return true },
		},
		codegraph: cg,
		registry:  reg2,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents", s.handleAgents)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("POST /api/tasks/{id}/cancel", s.handleCancelTask)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ws", s.handleWS)

	// Project API routes (deprecated - using graphHandler instead)
	// projHandler := api.NewProjectHandler(s.daemon.pool, s.codegraph, s.registry)
	// projHandler.RegisterRoutes(mux)

	// Graph API routes
	graphHandler := api.NewGraphHandler(s.daemon.pool, s.registry)
	graphHandler.RegisterRoutes(mux)

	return cors(mux)
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	type dto struct {
		ID                 int64  `json:"id"`
		Name               string `json:"name"`
		RuntimeMode        string `json:"runtime_mode"`
		MaxConcurrentTasks int    `json:"max_concurrent_tasks"`
		Model              string `json:"model"`
		ThinkingLevel      string `json:"thinking_level"`
	}
	var out []dto
	for _, a := range s.reg.All() {
		out = append(out, dto{a.ID, a.Name, a.RuntimeMode, a.MaxConcurrentTasks, a.Model, a.ThinkingLevel})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tasks, err := s.queue.List(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if tasks == nil {
		tasks = []*task.Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.queue.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type createTaskReq struct {
	Agent    string `json:"agent"`
	Prompt   string `json:"prompt"`
	Priority int    `json:"priority"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeErrorMsg(w, http.StatusBadRequest, "prompt is required")
		return
	}
	// Default to the first agent if none specified.
	var a *agent.Agent
	var ok bool
	if req.Agent != "" {
		a, ok = s.reg.ByName(req.Agent)
	} else if all := s.reg.All(); len(all) > 0 {
		a, ok = all[0], true
	}
	if !ok || a == nil {
		writeErrorMsg(w, http.StatusBadRequest, "unknown agent")
		return
	}
	if req.Priority == 0 {
		req.Priority = task.PriorityMedium
	}
	t, err := s.queue.Enqueue(r.Context(), a.ID, req.Prompt, req.Priority)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.bus.Publish(Event{Type: EventCreated, Task: t, TaskID: t.ID})
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.daemon.CancelTask(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleWS upgrades to a WebSocket and streams every bus event as JSON.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	events, unsub := s.bus.Subscribe()
	defer unsub()

	// Reader goroutine: detect client disconnect / drain control frames.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-closed:
			return
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case e, ok := <-events:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(e); err != nil {
				return
			}
		}
	}
}

// --- helpers ---

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeErrorMsg(w, status, err.Error())
}

func writeErrorMsg(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// StartHTTP runs the HTTP server until ctx is canceled.
func StartHTTP(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	log.Printf("http listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
