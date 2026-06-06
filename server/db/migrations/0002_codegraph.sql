-- Legacy schema: kept for backward compatibility only.
-- New code should use 0003_repo_graph.sql (repo_nodes / repo_edges).
-- SHRIMP MVP Version A — CodeGraph multi-repo schema.

CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS project_repositories (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    repo_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('frontend', 'backend', 'shared', 'tool')),
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(project_id, repo_id)
);

CREATE TABLE IF NOT EXISTS api_endpoints (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    repo_id TEXT NOT NULL,
    method TEXT NOT NULL CHECK (method IN ('GET','POST','PUT','DELETE','PATCH')),
    path TEXT NOT NULL,
    description TEXT,
    confidence REAL NOT NULL DEFAULT 1.0,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(repo_id, method, path)
);

CREATE TABLE IF NOT EXISTS api_bindings (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_endpoint_id TEXT NOT NULL REFERENCES api_endpoints(id) ON DELETE CASCADE,
    target_endpoint_id TEXT NOT NULL REFERENCES api_endpoints(id) ON DELETE CASCADE,
    binding_type TEXT NOT NULL CHECK (binding_type IN ('http','grpc','event','db')),
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_project_repos_project ON project_repositories(project_id);
CREATE INDEX IF NOT EXISTS idx_api_endpoints_project ON api_endpoints(project_id);
CREATE INDEX IF NOT EXISTS idx_api_endpoints_repo ON api_endpoints(repo_id);
CREATE INDEX IF NOT EXISTS idx_api_bindings_project ON api_bindings(project_id);