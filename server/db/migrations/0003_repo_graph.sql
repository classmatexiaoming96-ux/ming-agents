-- SHRIMP MVP Version A — Graph-based CodeGraph multi-repo schema.
-- Replaces the flat projects/project_repositories/api_endpoints/api_bindings
-- tables with a proper node+edge graph structure.

CREATE TABLE IF NOT EXISTS repo_nodes (
    repo_id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    language TEXT,
    role TEXT DEFAULT 'shared' CHECK (role IN ('frontend', 'backend', 'shared', 'tool')),
    initialized BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS repo_edges (
    id TEXT PRIMARY KEY,
    source_repo_id TEXT NOT NULL REFERENCES repo_nodes(repo_id) ON DELETE CASCADE,
    target_repo_id TEXT NOT NULL REFERENCES repo_nodes(repo_id) ON DELETE CASCADE,
    edge_type TEXT NOT NULL CHECK (edge_type IN ('http','grpc','event','db')),
    endpoint TEXT NOT NULL,
    description TEXT,
    confidence REAL DEFAULT 1.0,
    discovered_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(source_repo_id, target_repo_id, endpoint)
);

CREATE INDEX IF NOT EXISTS idx_edges_source ON repo_edges(source_repo_id);
CREATE INDEX IF NOT EXISTS idx_edges_target ON repo_edges(target_repo_id);

-- Legacy tables are retained for backward compatibility during migration.
-- They can be removed after migration is complete.
