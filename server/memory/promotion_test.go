package memory

import "testing"

// makeFrozenRun creates and freezes an L3 run bundle in the current temp vault
// so eligibility checks that require frozen, integrity-verified bundles can run.
func makeFrozenRun(t *testing.T, project, runID string) {
	t.Helper()
	receiver, err := NewRunBundleReceiver(project, runID)
	if err != nil {
		t.Fatalf("NewRunBundleReceiver(%q,%q) error = %v", project, runID, err)
	}
	if err := receiver.ReceivePhaseReuse("planning", "reuse note for "+runID); err != nil {
		t.Fatalf("ReceivePhaseReuse() error = %v", err)
	}
	if err := receiver.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
}

func candidateMemory(project string, runIDs []string) Memory {
	evidenceRefs := make([]string, 0, len(runIDs))
	for _, r := range runIDs {
		evidenceRefs = append(evidenceRefs, "runs/"+project+"/"+r+"/summary/items.jsonl#sha256=abc")
	}
	return Memory{
		ID:                "cand-1",
		Project:           project,
		Title:             "Retry review node after transient timeout",
		Body:              "Retry once before fallback when the review node times out.",
		Tags:              []string{"workflow", "retry"},
		Layer:             "l2_inbox",
		Status:            "active",
		PromotionState:    PromotionCandidate,
		EvidenceRefs:      evidenceRefs,
		SourceRunIDs:      runIDs,
		SourceSystem:      "automind",
		SourceGranularity: "task_summary",
	}
}

func TestEvaluateL3ToL2_ThreeIndependentFrozenRunsPass(t *testing.T) {
	useTempVault(t)
	runs := []string{"run-a", "run-b", "run-c"}
	for _, r := range runs {
		makeFrozenRun(t, "ming-agents", r)
	}
	report := evaluateL3ToL2(candidateMemory("ming-agents", runs), DefaultL3ToL2Threshold)
	if !report.Eligible {
		t.Fatalf("report = %+v, want eligible", report)
	}
	if report.IndependentRuns != 3 {
		t.Fatalf("IndependentRuns = %d, want 3", report.IndependentRuns)
	}
	if len(report.BlockingReasons) != 0 {
		t.Fatalf("BlockingReasons = %v, want none", report.BlockingReasons)
	}
}

func TestEvaluateL3ToL2_ThreeRefsFromOneRunFail(t *testing.T) {
	useTempVault(t)
	makeFrozenRun(t, "ming-agents", "run-a")
	// Same run id repeated does not count as independent evidence.
	report := evaluateL3ToL2(candidateMemory("ming-agents", []string{"run-a", "run-a", "run-a"}), DefaultL3ToL2Threshold)
	if report.Eligible {
		t.Fatalf("report = %+v, want ineligible (only 1 independent run)", report)
	}
	if report.IndependentRuns != 1 {
		t.Fatalf("IndependentRuns = %d, want 1", report.IndependentRuns)
	}
}

func TestEvaluateL3ToL2_OpenBundleBlocks(t *testing.T) {
	useTempVault(t)
	// Create bundles but do not freeze two of them.
	makeFrozenRun(t, "ming-agents", "run-a")
	for _, r := range []string{"run-b", "run-c"} {
		receiver, err := NewRunBundleReceiver("ming-agents", r)
		if err != nil {
			t.Fatalf("receiver: %v", err)
		}
		if err := receiver.ReceivePhaseReuse("planning", "open note"); err != nil {
			t.Fatalf("receive: %v", err)
		}
	}
	report := evaluateL3ToL2(candidateMemory("ming-agents", []string{"run-a", "run-b", "run-c"}), DefaultL3ToL2Threshold)
	if report.Eligible {
		t.Fatalf("report = %+v, want ineligible with open bundles", report)
	}
	if !containsReason(report.BlockingReasons, "bundle_unverified") {
		t.Fatalf("BlockingReasons = %v, want bundle_unverified", report.BlockingReasons)
	}
}

func TestEvaluateL3ToL2_MissingFieldsBlock(t *testing.T) {
	useTempVault(t)
	mem := Memory{ID: "cand-x", Project: "ming-agents"} // no body/tags/evidence/runs
	report := evaluateL3ToL2(mem, DefaultL3ToL2Threshold)
	if report.Eligible {
		t.Fatalf("report = %+v, want ineligible", report)
	}
	for _, want := range []string{"missing_body", "missing_tags", "missing_evidence_ref"} {
		if !containsReason(report.BlockingReasons, want) {
			t.Fatalf("BlockingReasons = %v, want %s", report.BlockingReasons, want)
		}
	}
}

func TestEvaluateL3ToL2_OneEvidenceRefWithExtraRunIDsFails(t *testing.T) {
	useTempVault(t)
	runs := []string{"run-a", "run-b", "run-c"}
	for _, r := range runs {
		makeFrozenRun(t, "ming-agents", r)
	}
	// One evidence ref (run-a) but three source run ids: the two unevidenced
	// runs must not count toward independence.
	mem := candidateMemory("ming-agents", runs)
	mem.EvidenceRefs = []string{"runs/ming-agents/run-a/summary/items.jsonl#sha256=abc"}
	report := evaluateL3ToL2(mem, DefaultL3ToL2Threshold)
	if report.Eligible {
		t.Fatalf("report = %+v, want ineligible (only 1 evidenced run)", report)
	}
	if report.IndependentRuns != 1 {
		t.Fatalf("IndependentRuns = %d, want 1", report.IndependentRuns)
	}
	if !containsReason(report.BlockingReasons, "insufficient_independent_runs:1<3") {
		t.Fatalf("BlockingReasons = %v, want insufficient_independent_runs:1<3", report.BlockingReasons)
	}
}

func TestEvaluateEligibility_RejectsNonL2Target(t *testing.T) {
	useTempVault(t)
	if _, err := EvaluateEligibility("cand-1", "l1"); err == nil {
		t.Fatal("EvaluateEligibility to l1 must be rejected; L1 is human-only curation")
	}
}

func TestEvaluateEligibility_UnknownSource(t *testing.T) {
	useTempVault(t)
	if _, err := EvaluateEligibility("does-not-exist", "l2"); err == nil {
		t.Fatal("EvaluateEligibility for unknown source must error")
	}
}

func containsReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}
