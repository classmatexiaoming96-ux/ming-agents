package memory

import (
	"fmt"
	"strings"
)

// PromotionThreshold captures the conservative L3 -> L2 defaults from the Phase 7
// design. The shipped default requires evidence from at least three independent
// frozen run bundles; a reviewer may still override with a single strong run.
type PromotionThreshold struct {
	MinIndependentRuns int
	RequireFrozen      bool
	RequireBody        bool
	RequireTags        bool
	RequireEvidenceRef bool
}

// DefaultL3ToL2Threshold is the conservative shipped policy.
var DefaultL3ToL2Threshold = PromotionThreshold{
	MinIndependentRuns: 3,
	RequireFrozen:      true,
	RequireBody:        true,
	RequireTags:        true,
	RequireEvidenceRef: true,
}

// EligibilityReport is the read-only verdict for a promotion source. It never
// mutates state; it only explains whether automation may mark the candidate
// ready for review and what, if anything, is blocking promotion.
type EligibilityReport struct {
	Eligible        bool
	ReadyForReview  bool
	BlockingReasons []string
	IndependentRuns int
	Checks          map[string]bool
}

// allEvidenceRefs returns the full set of evidence refs backing a memory,
// merging the legacy single EvidenceRef field with the EvidenceRefs slice and
// de-duplicating. Older candidates that only set EvidenceRef keep working.
func allEvidenceRefs(mem Memory) []string {
	var out []string
	seen := map[string]bool{}
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		out = append(out, ref)
	}
	add(mem.EvidenceRef)
	for _, ref := range mem.EvidenceRefs {
		add(ref)
	}
	return out
}

// runIDFromEvidenceRef extracts the L3 run id from an evidence ref of the form
// "runs/{project}/{run_id}/...". It returns "" when the ref does not point at a
// run bundle, so a bare log path cannot be miscounted as run provenance.
func runIDFromEvidenceRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if i := strings.IndexAny(ref, "#?"); i >= 0 {
		ref = ref[:i]
	}
	ref = strings.TrimPrefix(ref, "/")
	parts := strings.Split(ref, "/")
	if len(parts) >= 3 && parts[0] == "runs" {
		return parts[2]
	}
	return ""
}

// evidenceBackedRuns returns the distinct run ids that are (a) referenced by at
// least one evidence ref and (b) backed by a frozen, integrity-verified L3 run
// bundle. This is stricter than counting SourceRunIDs: a run only counts when an
// evidence ref actually ties the lesson to that run's bundle. allVerified is
// false when any evidence-referenced run is open or fails verification.
func evidenceBackedRuns(project string, evidenceRefs []string) (count int, allVerified bool) {
	runIDs := make([]string, 0, len(evidenceRefs))
	sawRunRef := false
	for _, ref := range evidenceRefs {
		runID := runIDFromEvidenceRef(ref)
		if runID == "" {
			continue
		}
		sawRunRef = true
		runIDs = append(runIDs, runID)
	}
	count, allVerified = independentFrozenRuns(project, runIDs)
	// If no evidence ref pointed at a run bundle we cannot vouch for any run.
	if !sawRunRef {
		return 0, false
	}
	return count, allVerified
}

// loadMemoryByID finds a single memory anywhere in the vault (notes, inbox,
// archive, and nested candidate folders) by its id. It returns a descriptive
// error when the id is unknown so callers can surface a stable failure.
func loadMemoryByID(id string) (Memory, error) {
	if id == "" {
		return Memory{}, fmt.Errorf("memory id is required")
	}
	all, err := readAllMemories("", "")
	if err != nil {
		return Memory{}, err
	}
	for _, m := range all {
		if m.ID == id {
			return m, nil
		}
	}
	return Memory{}, fmt.Errorf("memory %q not found", id)
}

// independentFrozenRuns returns the number of distinct run ids in the source's
// SourceRunIDs whose L3 run bundle is frozen and passes integrity verification.
// Repeated attempts within the same run collapse to one; an open or corrupt
// bundle does not count. It also reports whether every referenced run was
// counted, so the caller can distinguish "not enough runs" from "a run failed
// verification".
func independentFrozenRuns(project string, runIDs []string) (count int, allVerified bool) {
	seen := map[string]bool{}
	allVerified = true
	for _, runID := range runIDs {
		if runID == "" || seen[runID] {
			continue
		}
		seen[runID] = true
		receiver, err := NewRunBundleReceiver(project, runID)
		if err != nil {
			allVerified = false
			continue
		}
		frozen, err := receiver.IsFrozen()
		if err != nil || !frozen {
			allVerified = false
			continue
		}
		if err := receiver.VerifyIntegrity(); err != nil {
			allVerified = false
			continue
		}
		count++
	}
	return count, allVerified
}

// EvaluateEligibility reports whether a source memory may become an L2 project
// memory. It is read-only and safe for automation to call: it can mark a
// candidate ready for review but never transitions state. Promotion still
// requires an explicit human decision (or the single-run override) applied by
// Promote.
//
// The default L3 -> L2 rule is satisfied when the source carries evidence from
// at least three independent frozen run bundles and has a body, tags, and an
// evidence ref. When fewer independent runs back the source, the candidate is
// not automatically eligible but may still be reviewable, and a reviewer may use
// the single-run override at promote time.
func EvaluateEligibility(sourceID, targetLayer string) (*EligibilityReport, error) {
	if targetLayer != "l2" {
		return nil, fmt.Errorf("eligibility evaluation supports target layer l2, got %q", targetLayer)
	}
	mem, err := loadMemoryByID(sourceID)
	if err != nil {
		return nil, err
	}
	return evaluateL3ToL2(mem, DefaultL3ToL2Threshold), nil
}

func evaluateL3ToL2(mem Memory, threshold PromotionThreshold) *EligibilityReport {
	report := &EligibilityReport{Checks: map[string]bool{}}

	evidenceRefs := allEvidenceRefs(mem)
	hasBody := mem.Body != ""
	hasTags := len(mem.Tags) > 0
	hasEvidence := len(evidenceRefs) > 0
	hasRunIDs := len(mem.SourceRunIDs) > 0

	report.Checks["has_body"] = hasBody
	report.Checks["has_tags"] = hasTags
	report.Checks["has_evidence_ref"] = hasEvidence
	report.Checks["has_source_run_ids"] = hasRunIDs

	if threshold.RequireBody && !hasBody {
		report.BlockingReasons = append(report.BlockingReasons, "missing_body")
	}
	if threshold.RequireTags && !hasTags {
		report.BlockingReasons = append(report.BlockingReasons, "missing_tags")
	}
	if threshold.RequireEvidenceRef && !hasEvidence {
		report.BlockingReasons = append(report.BlockingReasons, "missing_evidence_ref")
	}

	// Independence is measured by distinct runs that each have an evidence ref
	// pointing at their frozen L3 bundle — not by a bare SourceRunIDs count. A
	// candidate with one evidence ref and three unbacked run ids no longer
	// qualifies; every counted run must be evidenced and verified.
	independent, allVerified := evidenceBackedRuns(mem.Project, evidenceRefs)
	report.IndependentRuns = independent
	report.Checks["all_runs_frozen"] = allVerified && hasEvidence
	report.Checks["meets_independent_run_threshold"] = independent >= threshold.MinIndependentRuns

	if hasEvidence && !allVerified {
		// A referenced run is open, missing, or failed integrity verification.
		report.BlockingReasons = append(report.BlockingReasons, "bundle_unverified")
	}
	if independent < threshold.MinIndependentRuns {
		report.BlockingReasons = append(report.BlockingReasons,
			fmt.Sprintf("insufficient_independent_runs:%d<%d", independent, threshold.MinIndependentRuns))
	}

	report.Eligible = len(report.BlockingReasons) == 0
	// A candidate is reviewable when it has the quality fields even if it has not
	// yet reached the automatic independent-run threshold: a reviewer may then
	// apply the single-run override.
	report.ReadyForReview = hasBody && hasTags && hasEvidence && allVerified
	return report
}
