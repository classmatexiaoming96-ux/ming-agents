-- +migrate File:SeqNum,Up
-- 0008_cleanup_retention.up.sql
--
-- Supporting indexes for data-retention cleanup of agent_task_queue and
-- loop_iterations. These tables are append-only at runtime (no TTL / DELETE
-- path existed), so they grow unbounded with the full agent_request /
-- agent_result / eval_details JSONB payloads. CleanupExpired (store/cleanup.go)
-- deletes rows belonging to terminal runs older than a retention period; these
-- indexes make both the cleanup join and the hot worker queries efficient.

-- agent_task_queue had NO indexes besides the PK. run_id is used by the cleanup
-- join and by ByRun/ClaimedCount/PendingCount.
CREATE INDEX IF NOT EXISTS idx_task_queue_run_id ON agent_task_queue(run_id);

-- Claim() scans WHERE status='pending' ORDER BY created_at; this also speeds up
-- status-filtered cleanup scans.
CREATE INDEX IF NOT EXISTS idx_task_queue_status_created ON agent_task_queue(status, created_at);

-- Cleanup selects terminal runs whose end time is older than the cutoff.
CREATE INDEX IF NOT EXISTS idx_runs_status_ended_at ON runs(status, ended_at);
