-- +migrate File:SeqNum,Down
-- 0006_loop_engineering.down.sql
DROP INDEX IF EXISTS idx_artifacts_step_id;
DROP INDEX IF EXISTS idx_artifacts_run_id;
DROP INDEX IF EXISTS idx_steps_run_id;
DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS steps;
DROP TABLE IF EXISTS runs;