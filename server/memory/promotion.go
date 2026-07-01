package memory

import (
	"fmt"
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

	hasBody := mem.Body != ""
	hasTags := len(mem.Tags) > 0
	hasEvidence := mem.EvidenceRef != ""
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
	if !hasRunIDs {
		report.BlockingReasons = append(report.BlockingReasons, "missing_provenance")
	}

	independent, allVerified := independentFrozenRuns(mem.Project, mem.SourceRunIDs)
	report.IndependentRuns = independent
	report.Checks["all_runs_frozen"] = allVerified && hasRunIDs
	report.Checks["meets_independent_run_threshold"] = independent >= threshold.MinIndependentRuns

	if hasRunIDs && !allVerified {
		// A referenced run is open or failed integrity verification.
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
	report.ReadyForReview = hasBody && hasTags && hasEvidence && hasRunIDs && allVerified
	return report
}
