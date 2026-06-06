// Package api provides HTTP handlers for the CodeGraph REST API.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shrimp-mvp/server/codegraph"
)

// GraphHandler handles graph-related API endpoints.
type GraphHandler struct {
	pool *pgxpool.Pool
	graph *codegraph.RepoGraph
}

// NewGraphHandler creates a new GraphHandler.
func NewGraphHandler(pool *pgxpool.Pool, graph *codegraph.RepoGraph) *GraphHandler {
	return &GraphHandler{
		pool:  pool,
		graph: graph,
	}
}

// RegisterRoutes registers graph routes on the given mux.
func (h *GraphHandler) RegisterRoutes(mux *http.ServeMux) {
	// Node routes
	mux.HandleFunc("GET /api/graph/nodes", h.handleListNodes)
	mux.HandleFunc("POST /api/graph/nodes", h.handleAddNode)
	mux.HandleFunc("GET /api/graph/nodes/{repoID}", h.handleGetNode)
	mux.HandleFunc("PUT /api/graph/nodes/{repoID}", h.handleUpdateNode)
	mux.HandleFunc("DELETE /api/graph/nodes/{repoID}", h.handleRemoveNode)

	// Edge routes
	mux.HandleFunc("GET /api/graph/edges", h.handleListEdges)
	mux.HandleFunc("POST /api/graph/edges", h.handleAddEdge)
	mux.HandleFunc("GET /api/graph/edges/{edgeID}", h.handleGetEdge)
	mux.HandleFunc("DELETE /api/graph/edges/{edgeID}", h.handleRemoveEdge)

	// Traversal routes
	mux.HandleFunc("GET /api/graph/nodes/{repoID}/outgoing", h.handleGetOutgoingEdges)
	mux.HandleFunc("GET /api/graph/nodes/{repoID}/incoming", h.handleGetIncomingEdges)
	mux.HandleFunc("GET /api/graph/nodes/{repoID}/reachable", h.handleGetReachable)

	// DB persistence routes
	mux.HandleFunc("POST /api/graph/sync-db", h.handleSyncFromDB)
	mux.HandleFunc("POST /api/graph/persist-db", h.handlePersistToDB)
}

// --- Node handlers ---

func (h *GraphHandler) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes := h.graph.ListNodes()
	writeJSON(w, http.StatusOK, nodes)
}

func (h *GraphHandler) handleAddNode(w http.ResponseWriter, r *http.Request) {
	var node codegraph.RepoNode
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if node.RepoID == "" {
		writeErrorMsg(w, http.StatusBadRequest, "repoID is required")
		return
	}

	if err := h.graph.AddNode(&node); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}

	writeJSON(w, http.StatusCreated, node)
}

func (h *GraphHandler) handleGetNode(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("repoID")

	node, err := h.graph.GetNode(repoID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	writeJSON(w, http.StatusOK, node)
}

func (h *GraphHandler) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("repoID")

	var updates struct {
		Name        *string `json:"name,omitempty"`
		Path        *string `json:"path,omitempty"`
		Language    *string `json:"language,omitempty"`
		Initialized *bool   `json:"initialized,omitempty"`
		Role        *string `json:"role,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	err := h.graph.UpdateNode(repoID, func(node *codegraph.RepoNode) error {
		if updates.Name != nil {
			node.Name = *updates.Name
		}
		if updates.Path != nil {
			node.Path = *updates.Path
		}
		if updates.Language != nil {
			node.Language = *updates.Language
		}
		if updates.Initialized != nil {
			node.Initialized = *updates.Initialized
		}
		if updates.Role != nil {
			node.Role = *updates.Role
		}
		return nil
	})

	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	node, _ := h.graph.GetNode(repoID)
	writeJSON(w, http.StatusOK, node)
}

func (h *GraphHandler) handleRemoveNode(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("repoID")

	if err := h.graph.RemoveNode(repoID); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "repoID": repoID})
}

// --- Edge handlers ---

func (h *GraphHandler) handleListEdges(w http.ResponseWriter, r *http.Request) {
	edges := h.graph.ListEdges()
	writeJSON(w, http.StatusOK, edges)
}

func (h *GraphHandler) handleAddEdge(w http.ResponseWriter, r *http.Request) {
	var edge codegraph.RepoEdge
	if err := json.NewDecoder(r.Body).Decode(&edge); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if edge.ID == "" {
		edge.ID = fmt.Sprintf("edge_%d", time.Now().UnixNano())
	}

	if err := h.graph.AddEdge(&edge); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}

	writeJSON(w, http.StatusCreated, edge)
}

func (h *GraphHandler) handleGetEdge(w http.ResponseWriter, r *http.Request) {
	edgeID := r.PathValue("edgeID")

	edge, err := h.graph.GetEdge(edgeID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	writeJSON(w, http.StatusOK, edge)
}

func (h *GraphHandler) handleRemoveEdge(w http.ResponseWriter, r *http.Request) {
	edgeID := r.PathValue("edgeID")

	if err := h.graph.RemoveEdge(edgeID); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "edgeID": edgeID})
}

// --- Traversal handlers ---

func (h *GraphHandler) handleGetOutgoingEdges(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("repoID")

	edges := h.graph.GetOutgoingEdges(repoID)
	writeJSON(w, http.StatusOK, edges)
}

func (h *GraphHandler) handleGetIncomingEdges(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("repoID")

	edges := h.graph.GetIncomingEdges(repoID)
	writeJSON(w, http.StatusOK, edges)
}

func (h *GraphHandler) handleGetReachable(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("repoID")
	direction := r.URL.Query().Get("direction")
	depthStr := r.URL.Query().Get("depth")

	if direction == "" {
		direction = "upstream"
	}
	if direction != "upstream" && direction != "downstream" {
		writeErrorMsg(w, http.StatusBadRequest, "direction must be 'upstream' or 'downstream'")
		return
	}

	maxDepth := 3
	if depthStr != "" {
		var err error
		maxDepth, err = strconv.Atoi(depthStr)
		if err != nil || maxDepth < 0 {
			writeErrorMsg(w, http.StatusBadRequest, "depth must be a non-negative integer")
			return
		}
	}

	reachable := h.graph.GetReachable([]string{repoID}, direction, maxDepth)
	writeJSON(w, http.StatusOK, reachable)
}

// --- DB persistence handlers ---

func (h *GraphHandler) handleSyncFromDB(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Load nodes from DB
	rows, err := h.pool.Query(ctx, `
		SELECT repo_id, name, path, language, role, initialized, created_at
		FROM repo_nodes
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var node codegraph.RepoNode
		var createdAt time.Time
		if err := rows.Scan(&node.RepoID, &node.Name, &node.Path, &node.Language, &node.Role, &node.Initialized, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		node.CreatedAt = createdAt.Unix()
		if err := h.graph.AddNode(&node); err != nil {
			// Ignore duplicate errors during sync
			continue
		}
	}

	// Load edges from DB
	edgeRows, err := h.pool.Query(ctx, `
		SELECT id, source_repo_id, target_repo_id, edge_type, endpoint, description, confidence, discovered_at
		FROM repo_edges
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer edgeRows.Close()

	for edgeRows.Next() {
		var edge codegraph.RepoEdge
		var discoveredAt time.Time
		if err := edgeRows.Scan(&edge.ID, &edge.SourceRepoID, &edge.TargetRepoID, &edge.EdgeType, &edge.Endpoint, &edge.Description, &edge.Confidence, &discoveredAt); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		edge.DiscoveredAt = discoveredAt.Unix()
		if err := h.graph.AddEdge(&edge); err != nil {
			// Ignore duplicate errors during sync
			continue
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "synced from DB"})
}

func (h *GraphHandler) handlePersistToDB(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Persist nodes to DB
	nodes := h.graph.ListNodes()
	for _, node := range nodes {
		createdAt := time.Unix(node.CreatedAt, 0)
		_, err := h.pool.Exec(ctx, `
			INSERT INTO repo_nodes (repo_id, name, path, language, role, initialized, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (repo_id) DO UPDATE SET
				name = EXCLUDED.name,
				path = EXCLUDED.path,
				language = EXCLUDED.language,
				role = EXCLUDED.role,
				initialized = EXCLUDED.initialized
		`, node.RepoID, node.Name, node.Path, node.Language, node.Role, node.Initialized, createdAt)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	// Persist edges to DB
	edges := h.graph.ListEdges()
	for _, edge := range edges {
		discoveredAt := time.Unix(edge.DiscoveredAt, 0)
		_, err := h.pool.Exec(ctx, `
			INSERT INTO repo_edges (id, source_repo_id, target_repo_id, edge_type, endpoint, description, confidence, discovered_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO UPDATE SET
				source_repo_id = EXCLUDED.source_repo_id,
				target_repo_id = EXCLUDED.target_repo_id,
				edge_type = EXCLUDED.edge_type,
				endpoint = EXCLUDED.endpoint,
				description = EXCLUDED.description,
				confidence = EXCLUDED.confidence
		`, edge.ID, edge.SourceRepoID, edge.TargetRepoID, edge.EdgeType, edge.Endpoint, edge.Description, edge.Confidence, discoveredAt)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "persisted to DB", "nodes": fmt.Sprintf("%d", len(nodes)), "edges": fmt.Sprintf("%d", len(edges))})
}
