-- Add max_attempts column to agent_task_queue for Issue 3 fix.

ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS max_attempts INT NOT NULL DEFAULT 3;