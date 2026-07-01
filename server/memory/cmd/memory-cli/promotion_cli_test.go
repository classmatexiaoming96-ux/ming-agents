package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ming-agents/server/memory"
)

// seedFrozenRun creates and freezes an L3 run bundle in the CLI temp vault.
func seedFrozenRun(t *testing.T, project, runID string) {
	t.Helper()
	receiver, err := memory.NewRunBundleReceiver(project, runID)
	if err != nil {
		t.Fatalf("receiver: %v", err)
	}
	if err := receiver.ReceivePhaseReuse("planning", "note "+runID); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := receiver.Freeze(); err != nil {
		t.Fatalf("freeze: %v", err)
	}
}

// seedCandidate writes an l2_inbox candidate memory for promotion tests by
// emitting frontmatter directly, so the CLI package does not depend on any
// unexported memory helper.
func seedCandidate(t *testing.T, id, project string, runIDs []string) {
	t.Helper()
	dir := filepath.Join(memory.VaultDir, "notes", "_inbox", "cross_project_candidates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir candidate dir: %v", err)
	}
	var runs strings.Builder
	for _, r := range runIDs {
		fmt.Fprintf(&runs, "\n- %s", r)
	}
	content := fmt.Sprintf(`---
id: %s
type: decision
project: %s
tags:
- workflow
title: Retry review node
status: active
layer: l2_inbox
promotion_state: candidate
evidence_ref: runs/%s/%s/summary/items.jsonl#sha256=abc
source_run_ids:%s
---
Retry once before fallback when the review node times out.`,
		id, project, project, runIDs[0], runs.String())
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write candidate: %v", err)
	}
}

func TestCmdPromote_DryRunByDefault(t *testing.T) {
	vault := useTempCLIVault(t)
	runs := []string{"run-a", "run-b", "run-c"}
	for _, r := range runs {
		seedFrozenRun(t, "ming-agents", r)
	}
	seedCandidate(t, "cand-1", "ming-agents", runs)

	var out bytes.Buffer
	if err := cmdPromote([]string{"--source", "cand-1", "--to", "l2", "--rationale", "three runs agree", "--actor", "alice"}, &out); err != nil {
		t.Fatalf("cmdPromote error = %v", err)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Fatalf("output = %q, want dry-run", out.String())
	}
	if _, err := os.Stat(filepath.Join(vault, "runs", "_promotion_audit")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote audit: %v", err)
	}
}

func TestCmdPromote_ApplyWritesAudit(t *testing.T) {
	vault := useTempCLIVault(t)
	runs := []string{"run-a", "run-b", "run-c"}
	for _, r := range runs {
		seedFrozenRun(t, "ming-agents", r)
	}
	seedCandidate(t, "cand-1", "ming-agents", runs)

	var out bytes.Buffer
	if err := cmdPromote([]string{"--source", "cand-1", "--to", "l2", "--rationale", "three runs agree", "--actor", "alice", "--apply"}, &out); err != nil {
		t.Fatalf("cmdPromote error = %v", err)
	}
	if !strings.Contains(out.String(), "audit=") {
		t.Fatalf("output = %q, want audit id", out.String())
	}
	if _, err := os.Stat(filepath.Join(vault, "runs", "_promotion_audit")); err != nil {
		t.Fatalf("audit dir missing after apply: %v", err)
	}
}

func TestCmdCurate_RequiresApprover(t *testing.T) {
	useTempCLIVault(t)
	var out bytes.Buffer
	err := cmdCurate([]string{"--source", "l2-1", "--to", "l1", "--rationale", "global"}, &out)
	if err == nil {
		t.Fatal("curate without --approver must fail")
	}
}

func TestCmdListPendingPromotion_JSON(t *testing.T) {
	useTempCLIVault(t)
	runs := []string{"run-a", "run-b", "run-c"}
	for _, r := range runs {
		seedFrozenRun(t, "ming-agents", r)
	}
	seedCandidate(t, "cand-1", "ming-agents", runs)

	var out bytes.Buffer
	if err := cmdListPendingPromotion([]string{"--to", "l2", "--format", "json"}, &out); err != nil {
		t.Fatalf("cmdListPendingPromotion error = %v", err)
	}
	if !strings.Contains(out.String(), "cand-1") {
		t.Fatalf("output = %q, want cand-1", out.String())
	}
}

func TestCmdRevoke_RequiresReason(t *testing.T) {
	useTempCLIVault(t)
	var out bytes.Buffer
	if err := cmdRevoke([]string{"--target", "l2-1"}, &out); err == nil {
		t.Fatal("revoke without --reason must fail")
	}
}
