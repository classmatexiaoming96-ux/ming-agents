-- SHRIMP MVP Version A — initial schema.

CREATE TABLE IF NOT EXISTS agents (
    id                   BIGSERIAL PRIMARY KEY,
    name                 TEXT        NOT NULL UNIQUE,
    runtime_mode         TEXT        NOT NULL DEFAULT 'exec',
    max_concurrent_tasks INT         NOT NULL DEFAULT 1 CHECK (max_concurrent_tasks > 0),
    model                TEXT        NOT NULL DEFAULT 'claude-opus-4-8',
    thinking_level       TEXT        NOT NULL DEFAULT 'medium',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent_task_queue (
    id               BIGSERIAL PRIMARY KEY,
    agent_id         BIGINT      NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    status           TEXT        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','claimed','running','completed','failed','canceled')),
    priority         SMALLINT    NOT NULL DEFAULT 2 CHECK (priority BETWEEN 1 AND 3),
    prompt           TEXT        NOT NULL,
    result           TEXT,
    error            TEXT,
    worker_id        TEXT,
    attempts         INT         NOT NULL DEFAULT 0,
    cancel_requested BOOLEAN     NOT NULL DEFAULT false,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at       TIMESTAMPTZ,
    started_at       TIMESTAMPTZ,
    heartbeat_at     TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ
);

-- Claim ordering + fast "find next pending for these agents".
CREATE INDEX IF NOT EXISTS idx_queue_claim
    ON agent_task_queue (agent_id, priority, created_at)
    WHERE status = 'pending';

-- Orphan recovery scan.
CREATE INDEX IF NOT EXISTS idx_queue_active_heartbeat
    ON agent_task_queue (heartbeat_at)
    WHERE status IN ('claimed','running');
