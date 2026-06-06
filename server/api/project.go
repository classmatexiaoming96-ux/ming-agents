// Package project is deprecated. Use ../codegraph/repo_graph.go and ../api/graph.go instead.
// Package api provides HTTP handlers for the SHRIMP REST API.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shrimp-mvp/server/codegraph"
)

// ProjectHandler handles project-related API endpoints.
type ProjectHandler struct {
	pool *pgxpool.Pool
	codegraph  *codegraph.CodeGraphCLI
	registry   *codegraph.RepoGraph
}

// NewProjectHandler creates a new ProjectHandler.
func NewProjectHandler(pool *pgxpool.Pool, cg *codegraph.CodeGraphCLI, reg *codegraph.RepoGraph) *ProjectHandler {
	return &ProjectHandler{
		pool:      pool,
		codegraph: cg,
		registry:  reg,
	}
}

// RegisterRoutes registers project routes on the given mux.
func (h *ProjectHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects", h.handleListProjects)
	mux.HandleFunc("POST /api/projects", h.handleCreateProject)
	mux.HandleFunc("GET /api/projects/{id}", h.handleGetProject)
	mux.HandleFunc("POST /api/projects/{id}/repos", h.handleAddRepoToProject)
	mux.HandleFunc("GET /api/projects/{id}/endpoints", h.handleListEndpoints)
	mux.HandleFunc("POST /api/projects/{id}/endpoints", h.handleRegisterEndpoint)
	mux.HandleFunc("GET /api/projects/{id}/bindings", h.handleListBindings)
	mux.HandleFunc("POST /api/projects/{id}/bindings", h.handleCreateBinding)
	mux.HandleFunc("GET /api/repos/{repo_id}/callers", h.handleGetCallers)
	mux.HandleFunc("GET /api/repos/{repo_id}/callees", h.handleGetCallees)
	mux.HandleFunc("POST /api/codegraph/init", h.handleCodeGraphInit)
}

// Project represents a development project with multiple repos.
type Project struct {
	ID string    `json:"id"`
	Name string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ProjectWithRepos includes associated repositories.
type ProjectWithRepos struct {
	Project
	Repositories []ProjectRepo `json:"repositories,omitempty"`
}

// ProjectRepo represents a repository in a project.
type ProjectRepo struct {
	ID string `json:"id"`
	ProjectID string `json:"project_id"`
	RepoID  string `json:"repo_id"`
	Role    string `json:"role"`
	AddedAt time.Time `json:"added_at"`
}

// APIEndpoint represents a discovered API endpoint.
type APIEndpoint struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	RepoID      string    `json:"repo_id"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Description string    `json:"description,omitempty"`
	Confidence  float64   `json:"confidence"`
	DiscoveredAt time.Time `json:"discovered_at"`
}

// APIBinding represents a connection between two API endpoints.
type APIBinding struct {
	ID string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	SourceEndpointID string    `json:"source_endpoint_id"`
	TargetEndpointID string    `json:"target_endpoint_id"`
	BindingType      string    `json:"binding_type"`
	Description string    `json:"description,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

func (h *ProjectHandler) handleListProjects(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(), `
		SELECT id, name, description, created_at FROM projects ORDER BY created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		projects = append(projects, p)
	}
	if projects == nil {
		projects = []Project{}
	}
	writeJSON(w, http.StatusOK, projects)
}

func (h *ProjectHandler) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" {
		writeErrorMsg(w, http.StatusBadRequest, "name is required")
		return
	}

	id := fmt.Sprintf("proj_%d", time.Now().UnixNano())
	var description *string
	if req.Description != "" {
		description = &req.Description
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO projects (id, name, description) VALUES ($1, $2, $3)`,
		id, req.Name, description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, Project{ID: id, Name: req.Name, Description: req.Description})
}

func (h *ProjectHandler) handleGetProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var proj Project
	err := h.pool.QueryRow(r.Context(),
		`SELECT id, name, description, created_at FROM projects WHERE id = $1`, id).
		Scan(&proj.ID, &proj.Name, &proj.Description, &proj.CreatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	repos, err := h.listProjectRepos(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, ProjectWithRepos{
		Project:      proj,
		Repositories: repos,
	})
}

func (h *ProjectHandler) handleAddRepoToProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")

	var req struct {
		RepoID string `json:"repo_id"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.RepoID == "" || req.Role == "" {
		writeErrorMsg(w, http.StatusBadRequest, "repo_id and role are required")
		return
	}

	id := fmt.Sprintf("prepo_%d", time.Now().UnixNano())
	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO project_repositories (id, project_id, repo_id, role) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (project_id, repo_id) DO UPDATE SET role = $4`,
		id, projectID, req.RepoID, req.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, ProjectRepo{
		ID: id, ProjectID: projectID, RepoID: req.RepoID, Role: req.Role,
	})
}

func (h *ProjectHandler) handleListEndpoints(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, project_id, repo_id, method, path, description, confidence, discovered_at
		FROM api_endpoints WHERE project_id = $1 ORDER BY discovered_at DESC
	`, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var endpoints []APIEndpoint
	for rows.Next() {
		var e APIEndpoint
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.RepoID, &e.Method, &e.Path, &e.Description, &e.Confidence, &e.DiscoveredAt); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		endpoints = append(endpoints, e)
	}
	if endpoints == nil {
		endpoints = []APIEndpoint{}
	}
	writeJSON(w, http.StatusOK, endpoints)
}

func (h *ProjectHandler) handleRegisterEndpoint(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")

	var req struct {
		RepoID     string  `json:"repo_id"`
		Method     string  `json:"method"`
		Path       string  `json:"path"`
		Confidence float64 `json:"confidence"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.RepoID == "" || req.Method == "" || req.Path == "" {
		writeErrorMsg(w, http.StatusBadRequest, "repo_id, method, and path are required")
		return
	}

	if req.Confidence == 0 {
		req.Confidence = 1.0
	}

	id := fmt.Sprintf("ep_%d", time.Now().UnixNano())
	var description *string
	if req.Description != "" {
		description = &req.Description
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO api_endpoints (id, project_id, repo_id, method, path, description, confidence)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, projectID, req.RepoID, req.Method, req.Path, description, req.Confidence)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, APIEndpoint{
		ID: id, ProjectID: projectID, RepoID: req.RepoID, Method: req.Method,
		Path: req.Path, Description: req.Description, Confidence: req.Confidence,
	})
}

func (h *ProjectHandler) handleListBindings(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, project_id, source_endpoint_id, target_endpoint_id, binding_type, description, created_at
		FROM api_bindings WHERE project_id = $1 ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var bindings []APIBinding
	for rows.Next() {
		var b APIBinding
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.SourceEndpointID, &b.TargetEndpointID, &b.BindingType, &b.Description, &b.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		bindings = append(bindings, b)
	}
	if bindings == nil {
		bindings = []APIBinding{}
	}
	writeJSON(w, http.StatusOK, bindings)
}

func (h *ProjectHandler) handleCreateBinding(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")

	var req struct {
		SourceEndpointID string `json:"source_endpoint_id"`
		TargetEndpointID string `json:"target_endpoint_id"`
		BindingType      string `json:"binding_type"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.SourceEndpointID == "" || req.TargetEndpointID == "" || req.BindingType == "" {
		writeErrorMsg(w, http.StatusBadRequest, "source_endpoint_id, target_endpoint_id, and binding_type are required")
		return
	}

	id := fmt.Sprintf("bind_%d", time.Now().UnixNano())
	var description *string
	if req.Description != "" {
		description = &req.Description
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO api_bindings (id, project_id, source_endpoint_id, target_endpoint_id, binding_type, description)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		id, projectID, req.SourceEndpointID, req.TargetEndpointID, req.BindingType, description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, APIBinding{
		ID: id, ProjectID: projectID, SourceEndpointID: req.SourceEndpointID,
		TargetEndpointID: req.TargetEndpointID, BindingType: req.BindingType,
		Description: req.Description,
	})
}

func (h *ProjectHandler) handleGetCallers(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("repo_id")
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeErrorMsg(w, http.StatusBadRequest, "symbol query parameter is required")
		return
	}

	repoPath, err := h.registry.ResolveRepoPath(repoID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	result, err := h.codegraph.GetCallers(r.Context(), repoPath, symbol)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *ProjectHandler) handleGetCallees(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("repo_id")
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeErrorMsg(w, http.StatusBadRequest, "symbol query parameter is required")
		return
	}

	repoPath, err := h.registry.ResolveRepoPath(repoID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	result, err := h.codegraph.GetCallees(r.Context(), repoPath, symbol)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *ProjectHandler) handleCodeGraphInit(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("repo_id")
	if repoID == "" {
		writeErrorMsg(w, http.StatusBadRequest, "repo_id query parameter is required")
		return
	}

	repoPath, err := h.registry.ResolveRepoPath(repoID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	if err := h.codegraph.Init(r.Context(), repoPath); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if err := h.codegraph.Index(r.Context(), repoPath); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "initialized and indexed", "repo_id": repoID})
}

func (h *ProjectHandler) listProjectRepos(ctx context.Context, projectID string) ([]ProjectRepo, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT id, project_id, repo_id, role, added_at
		FROM project_repositories WHERE project_id = $1
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []ProjectRepo
	for rows.Next() {
		var pr ProjectRepo
		if err := rows.Scan(&pr.ID, &pr.ProjectID, &pr.RepoID, &pr.Role, &pr.AddedAt); err != nil {
			return nil, err
		}
		repos = append(repos, pr)
	}
	return repos, nil
}

// Helper functions

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