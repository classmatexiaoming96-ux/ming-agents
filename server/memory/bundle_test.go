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

	got, err := RunBundlePath("ming-agents", "run-123")
	if err != nil {
		t.Fatalf("RunBundlePath error = %v", err)
	}
	want := filepath.Join(VaultDir, "runs", "ming-agents", "run-123")
	if got != want {
		t.Fatalf("RunBundlePath() = %q, want %q", got, want)
	}
	if strings.Contains(got, filepath.Join("archive", "ming-agents")) {
		t.Fatalf("RunBundlePath() mixed with archive namespace: %s", got)
	}
}

func TestRunBundlePath_RejectsPathTraversal(t *testing.T) {
	tests := []struct {
		name    string
		project string
		runID   string
	}{
		{name: "parent traversal", project: "ming-agents", runID: "../../etc"},
		{name: "absolute run", project: "ming-agents", runID: "/absolute"},
		{name: "windows drive path", project: "ming-agents", runID: `C:\foo`},
		{name: "windows traversal", project: "ming-agents", runID: `..\..`},
		{name: "subdirectory run", project: "ming-agents", runID: "subdir/foo"},
		{name: "dot segment", project: "ming-agents", runID: "."},
		{name: "empty project", project: "", runID: "run-123"},
		{name: "reserved project", project: "CON", runID: "run-123"},
		{name: "control character", project: "ming-agents", runID: "run-\n123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RunBundlePath(tt.project, tt.runID); err == nil {
				t.Fatalf("RunBundlePath(%q, %q) error = nil, want rejection", tt.project, tt.runID)
			}
		})
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

func TestRunBundleReceiver_SoftFailureOnVaultCorruption(t *testing.T) {
	receiver := newTestRunBundleReceiver(t)
	manifestDir := filepath.Join(receiver.Root(), "manifest.json")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		t.Fatalf("MkdirAll manifest corruption error = %v", err)
	}

	err := receiver.ReceivePhaseReuse("planning", "content")
	if err == nil {
		t.Fatal("ReceivePhaseReuse error = nil, want corruption error")
	}
	data, readErr := os.ReadFile(filepath.Join(receiver.Root(), "receiver-status.json"))
	if readErr != nil {
		t.Fatalf("receiver-status.json missing after soft failure: %v", readErr)
	}
	var status map[string]runBundleArtifactStatus
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("receiver-status.json decode error = %v", err)
	}
	if status["phase_reuse"].Status != "failed" || status["phase_reuse"].Error == "" {
		t.Fatalf("receiver-status.json = %+v, want failed phase_reuse entry", status)
	}
}

func TestManifest_ArtifactCountsAccurate(t *testing.T) {
	receiver := newTestRunBundleReceiver(t)
	source := filepath.Join(t.TempDir(), "test.log")
	if err := os.WriteFile(source, []byte("evidence"), 0644); err != nil {
		t.Fatalf("WriteFile source error = %v", err)
	}

	if err := receiver.ReceivePhaseReuse("clarification", "reuse"); err != nil {
		t.Fatalf("ReceivePhaseReuse error = %v", err)
	}
	if err := receiver.ReceiveReuseAck("clarification", ReuseAck{Accepted: true}); err != nil {
		t.Fatalf("ReceiveReuseAck error = %v", err)
	}
	if err := receiver.ReceiveBriefAudit(NodeKind("clarification"), &BriefAudit{}, ""); err != nil {
		t.Fatalf("ReceiveBriefAudit error = %v", err)
	}
	if err := receiver.ReceiveEvidencePointer("test.log", source); err != nil {
		t.Fatalf("ReceiveEvidencePointer error = %v", err)
	}
	if err := receiver.ReceiveAutoMindSummary([]byte("summary"), "md"); err != nil {
		t.Fatalf("ReceiveAutoMindSummary error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(receiver.Root(), "manifest.json"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	var manifest runBundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest decode error = %v", err)
	}
	want := map[string]int{
		"phase_reuse":       1,
		"reuse_ack":         1,
		"brief_audit":       1,
		"evidence_pointers": 1,
		"automind_summary":  1,
	}
	for key, value := range want {
		if manifest.ArtifactCounts[key] != value {
			t.Fatalf("manifest.ArtifactCounts[%q] = %d, want %d in %+v", key, manifest.ArtifactCounts[key], value, manifest.ArtifactCounts)
		}
	}
}

func newTestRunBundleReceiver(t *testing.T) *RunBundleReceiver {
	t.Helper()
	oldVault := VaultDir
	VaultDir = t.TempDir()
	t.Cleanup(func() { VaultDir = oldVault })
	receiver, err := NewRunBundleReceiver("ming-agents", "run-test")
	if err != nil {
		t.Fatalf("NewRunBundleReceiver error = %v", err)
	}
	return receiver
}

func TestRunBundleReceiver_ImmutableAfterFreeze(t *testing.T) {
	oldVault := VaultDir
	VaultDir = t.TempDir()
	t.Cleanup(func() { VaultDir = oldVault })

	receiver, err := NewRunBundleReceiver("ming-agents", "run-immutable")
	if err != nil {
		t.Fatalf("NewRunBundleReceiver error = %v", err)
	}
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
