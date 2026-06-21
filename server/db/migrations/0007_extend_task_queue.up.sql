-- +migrate File:SeqNum,Up
-- 0007_extend_task_queue.up.sql

-- agent_task_queue: task fan-out queue consumed by workers
CREATE TABLE IF NOT EXISTS agent_task_queue (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    step_id         UUID NOT NULL REFERENCES steps(id) ON DELETE CASCADE,
    iteration       INTEGER NOT NULL DEFAULT 0,
    attempt         INTEGER NOT NULL DEFAULT 1,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','claimed','completed','failed')),
    adapter_key     TEXT NOT NULL,
    agent_request   JSONB NOT NULL,
    agent_result    JSONB,
    result_summary  TEXT,
    claimed_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    version         INTEGER NOT NULL DEFAULT 1
);

-- loop_iterations: iteration state for loop steps
CREATE TABLE IF NOT EXISTS loop_iterations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    step_id         UUID NOT NULL REFERENCES steps(id) ON DELETE CASCADE,
    iteration       INTEGER NOT NULL,
    status          TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','converged','max_iterations','failed')),
    eval_score      DECIMAL(10,4),
    eval_details    JSONB,
    converged       BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(run_id, step_id, iteration)
);

-- +migrate File:SeqNum,Down
-- 0007_extend_task_queue.down.sql
DROP TABLE IF EXISTS loop_iterations;
DROP TABLE IF EXISTS agent_task_queue;