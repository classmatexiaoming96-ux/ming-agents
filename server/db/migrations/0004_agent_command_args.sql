-- Add command and args columns to agents table for Issue 1 fix.

ALTER TABLE agents ADD COLUMN IF NOT EXISTS command TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS args    TEXT NOT NULL DEFAULT '';
