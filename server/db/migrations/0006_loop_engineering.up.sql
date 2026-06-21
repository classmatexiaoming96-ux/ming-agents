-- +migrate File:SeqNum,Up
-- 0006_loop_engineering.up.sql

-- runs table: top-level execution unit
CREATE TABLE IF NOT EXISTS runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    wdl_version TEXT NOT NULL DEFAULT '1.0',
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','paused','completed','failed','cancelled')),
    wdl_src     TEXT,
    max_parallel INT NOT NULL DEFAULT 4,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,
    error_msg   TEXT,
    version     INTEGER NOT NULL DEFAULT 1
);

-- steps table: DAG nodes
CREATE TABLE IF NOT EXISTS steps (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    step_type       TEXT NOT NULL CHECK (step_type IN ('task','loop','conditional','input')),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','skipped','failed')),
    iteration       INTEGER NOT NULL DEFAULT 0,
    attempt         INTEGER NOT NULL DEFAULT 1,
    inputs_json     TEXT,                     -- JSON map of input bindings
    outputs_json    TEXT,                     -- JSON map after execution
    skip_reason     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- artifacts table: step inputs/outputs context
CREATE TABLE IF NOT EXISTS artifacts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id      UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    step_id     UUID NOT NULL REFERENCES steps(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('json','text','file')),
    content     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_steps_run_id  ON steps(run_id);
CREATE INDEX idx_artifacts_run_id ON artifacts(run_id);
CREATE INDEX idx_artifacts_step_id ON artifacts(step_id);