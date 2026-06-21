-- +migrate File:SeqNum,Down
-- 0008_cleanup_retention.down.sql
DROP INDEX IF EXISTS idx_runs_status_ended_at;
DROP INDEX IF EXISTS idx_task_queue_status_created;
DROP INDEX IF EXISTS idx_task_queue_run_id;
