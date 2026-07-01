package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBundlePath_IsolatedFromArchive(t *testing.T) {
	oldVault := VaultDir
	VaultDir = t.TempDir()
	t.Cleanup(func() { VaultDir = oldVault })

	got := RunBundlePath("ming-agents", "run-123")
	want := filepath.Join(VaultDir, "runs", "ming-agents", "run-123")
	if got != want {
		t.Fatalf("RunBundlePath() = %q, want %q", got, want)
	}
	if strings.Contains(got, filepath.Join("archive", "ming-agents")) {
		t.Fatalf("RunBundlePath() mixed with archive namespace: %s", got)
	}
}

func TestRunBundleReceiver_ReceivePhaseReuse(t *testing.T) {
	receiver := newTestRunBundleReceiver(t)

	if err := receiver.ReceivePhaseReuse("planning", "small reuse"); err != nil {
		t.Fatalf("ReceivePhaseReuse error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(receiver.Root(), "phase-reuse", "planning.md"))
	if err != nil {
		t.Fatalf("phase reuse file missing: %v", err)
	}
	if string(data) != "small reuse" {
		t.Fatalf("phase reuse content = %q", data)
	}
}

func TestRunBundleReceiver_ReceiveReuseAck(t *testing.T) {
	receiver := newTestRunBundleReceiver(t)

	if err := receiver.ReceiveReuseAck("review", ReuseAck{Accepted: true, Note: "ok"}); err != nil {
		t.Fatalf("ReceiveReuseAck error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(receiver.Root(), "reuse-ack", "review.json"))
	if err != nil {
		t.Fatalf("reuse ack file missing: %v", err)
	}
	var got ReuseAck
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("reuse ack json error = %v", err)
	}
	if !got.Accepted || got.RunID != "run-test" || got.Phase != "review" {
		t.Fatalf("reuse ack = %+v, want accepted run-test/review", got)
	}
}

func TestRunBundleReceiver_ReceiveBriefAudit(t *testing.T) {
	receiver := newTestRunBundleReceiver(t)
	audit := &BriefAudit{InjectedIDs: []string{"mem-1"}}

	if err := receiver.ReceiveBriefAudit(NodeKind("development"), audit, "api"); err != nil {
		t.Fatalf("ReceiveBriefAudit error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(receiver.Root(), "brief-audit", "api-brief.json"))
	if err != nil {
		t.Fatalf("brief audit file missing: %v", err)
	}
	var got BriefAudit
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("brief audit json error = %v", err)
	}
	if len(got.InjectedIDs) != 1 || got.InjectedIDs[0] != "mem-1" {
		t.Fatalf("brief audit = %+v", got)
	}
}

func TestRunBundleReceiver_ReceiveEvidencePointer(t *testing.T) {
	receiver := newTestRunBundleReceiver(t)
	source := filepath.Join(t.TempDir(), "test.log")
	if err := os.WriteFile(source, []byte("test evidence"), 0644); err != nil {
		t.Fatalf("WriteFile source error = %v", err)
	}

	if err := receiver.ReceiveEvidencePointer("test.log", source); err != nil {
		t.Fatalf("ReceiveEvidencePointer error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(receiver.Root(), "evidence", "pointers.jsonl"))
	if err != nil {
		t.Fatalf("evidence pointer jsonl missing: %v", err)
	}
	if !strings.Contains(string(data), `"source_path":"`+source+`"`) || !strings.Contains(string(data), `"sha256"`) {
		t.Fatalf("evidence pointer jsonl = %s", data)
	}
}

func TestRunBundleReceiver_ReceiveAutoMindSummary(t *testing.T) {
	receiver := newTestRunBundleReceiver(t)

	if err := receiver.ReceiveAutoMindSummary([]byte(`{"summary":"ok"}`), "json"); err != nil {
		t.Fatalf("ReceiveAutoMindSummary error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(receiver.Root(), "automind-summary", "raw-summary.json"))
	if err != nil {
		t.Fatalf("automind summary file missing: %v", err)
	}
	if string(data) != `{"summary":"ok"}` {
		t.Fatalf("automind summary content = %q", data)
	}
}

func TestRunBundleReceiver_LargePhaseReuseStoresPointer(t *testing.T) {
	receiver := newTestRunBundleReceiver(t)
	large := strings.Repeat("x", RunBundleLargeFileThreshold+1)

	if err := receiver.ReceivePhaseReuse("planning", large); err != nil {
		t.Fatalf("ReceivePhaseReuse large error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(receiver.Root(), "phase-reuse", "planning.md")); !os.IsNotExist(err) {
		t.Fatalf("large phase reuse data file err = %v, want not exist", err)
	}
	data, err := os.ReadFile(filepath.Join(receiver.Root(), "phase-reuse", "planning.pointer.json"))
	if err != nil {
		t.Fatalf("large phase reuse pointer missing: %v", err)
	}
	var pointer map[string]any
	if err := json.Unmarshal(data, &pointer); err != nil {
		t.Fatalf("large pointer json error = %v", err)
	}
	if int(pointer["size"].(float64)) != len(large) || pointer["sha256"] == "" {
		t.Fatalf("large pointer = %+v", pointer)
	}
}

func newTestRunBundleReceiver(t *testing.T) *RunBundleReceiver {
	t.Helper()
	oldVault := VaultDir
	VaultDir = t.TempDir()
	t.Cleanup(func() { VaultDir = oldVault })
	return NewRunBundleReceiver("ming-agents", "run-test")
}

func TestRunBundleReceiver_ImmutableAfterFreeze(t *testing.T) {
	oldVault := VaultDir
	VaultDir = t.TempDir()
	t.Cleanup(func() { VaultDir = oldVault })

	receiver := NewRunBundleReceiver("ming-agents", "run-immutable")
	if err := receiver.ReceivePhaseReuse("planning", "memory hits"); err != nil {
		t.Fatalf("ReceivePhaseReuse before freeze error = %v", err)
	}
	if err := receiver.Freeze(); err != nil {
		t.Fatalf("Freeze error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(receiver.Root(), "_frozen")); err != nil {
		t.Fatalf("_frozen marker missing: %v", err)
	}
	manifestPath := filepath.Join(receiver.Root(), "manifest.json")
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if got := info.Mode().Perm(); got != 0444 {
		t.Fatalf("manifest mode = %v, want 0444", got)
	}
	if err := receiver.ReceivePhaseReuse("review", "late write"); err == nil {
		t.Fatal("ReceivePhaseReuse after freeze error = nil, want immutable error")
	}
}
