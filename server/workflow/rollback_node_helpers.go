package workflow

import (
	"path/filepath"
	"strconv"
	"strings"
)

func DefaultRollbackSpec(kind NodeKind) RollbackSpec {
	switch kind {
	case NodeKindClarification:
		return RollbackSpec{
			DefaultUnit:   RollbackUnit{Scope: "clarification", MaxAttempts: 3, ReusePolicy: SessionReuseSameSession},
			OnHumanReject: RollbackActionFixClarification,
		}
	case NodeKindPlanning:
		return RollbackSpec{
			DefaultUnit:   RollbackUnit{Scope: "planning", MaxAttempts: 3, ReusePolicy: SessionReuseSameSession},
			OnContract:    RollbackActionRegeneratePlan,
			OnHumanReject: RollbackActionRegeneratePlan,
		}
	case NodeKindDevelopment:
		return RollbackSpec{
			DefaultUnit:     RollbackUnit{Scope: "development", MaxAttempts: 3, ReusePolicy: SessionReuseOnHumanReject},
			OnContract:      RollbackActionRegenerateSubtask,
			OnHumanReject:   RollbackActionRetrySubtask,
			OnProductDefect: RollbackActionRetrySubtask,
		}
	case NodeKindEvaluation:
		return RollbackSpec{
			DefaultUnit: RollbackUnit{Scope: "evaluation", MaxAttempts: 2, ReusePolicy: SessionReuseNewSession},
		}
	default:
		return RollbackSpec{
			DefaultUnit: RollbackUnit{Scope: string(kind), MaxAttempts: 3, ReusePolicy: SessionReuseNewSession},
		}
	}
}

func BuildRollbackContext(req NodeRequest) RollbackContext {
	spec := DefaultRollbackSpec(req.Spec.Kind)
	return RollbackContext{
		RunID:    req.RunID,
		NodeID:   req.Spec.ID,
		NodeKind: req.Spec.Kind,
		Unit:     spec.DefaultUnit,
		Budget: RollbackBudget{
			MaxAttempts:     spec.DefaultUnit.MaxAttempts,
			ExhaustedAction: RollbackActionBlocked,
		},
		Lineage: NewFileLineageStore(req.RepoRoot),
	}
}

func HumanRejectSignal(unit RollbackUnit, decision ReviewDecision, paths ...string) RollbackSignal {
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = "reviewer rejected the attempt"
	}
	sourceNode := decision.NodeName
	if sourceNode == "" {
		sourceNode = unit.Scope
	}
	return RollbackSignal{
		FailureClass: FailureClassHumanReject,
		Reason:       reason,
		Suggestion:   strings.Join(paths, "\n"),
		SourceNode:   sourceNode,
	}
}

func AttemptPathsForRevision(basePrompt, baseOut, baseExit string, revision int) (string, string, string) {
	return revisionPath(basePrompt, ".prompt.md", revision),
		revisionPath(baseOut, ".out.md", revision),
		revisionPath(baseExit, ".exit", revision)
}

func revisionPath(path, suffix string, revision int) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, suffix)
	revised := stem + "-revision-" + strconv.Itoa(revision) + suffix
	if dir == "." {
		return revised
	}
	return filepath.Join(dir, revised)
}
