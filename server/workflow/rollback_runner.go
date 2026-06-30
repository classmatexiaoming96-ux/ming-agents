package workflow

import (
	"fmt"
	"time"
)

const defaultRollbackMaxAttempts = 3

type RollbackRunner struct{}

func NewRollbackRunner() *RollbackRunner {
	return &RollbackRunner{}
}

func (r *RollbackRunner) Decide(rctx RollbackContext, spec RollbackSpec, unit RollbackUnit, existingAttempts []AttemptEvent, signal RollbackSignal) *RollbackDecision {
	if unit.Scope == "" {
		unit = spec.DefaultUnit
	}
	if unit.Scope == "" {
		unit = rctx.Unit
	}
	maxAttempts := unit.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = spec.DefaultUnit.MaxAttempts
	}
	if maxAttempts <= 0 {
		maxAttempts = rctx.Budget.MaxAttempts
	}
	if maxAttempts <= 0 {
		maxAttempts = defaultRollbackMaxAttempts
	}

	action := rollbackActionForSignal(spec, signal.FailureClass)
	used, nextAttempt := rollbackAttemptsForScope(unit.Scope, existingAttempts)
	if action == "" {
		return &RollbackDecision{
			Action:      RollbackActionBlocked,
			TargetScope: unit.Scope,
			Rationale:   fmt.Sprintf("no rollback action configured for failure class %s", signal.FailureClass),
		}
	}
	if used >= maxAttempts {
		exhaustedAction := rctx.Budget.ExhaustedAction
		if exhaustedAction == "" {
			exhaustedAction = RollbackActionBlocked
		}
		return &RollbackDecision{
			Action:      exhaustedAction,
			TargetScope: unit.Scope,
			NewAttempt:  nextAttempt,
			Rationale:   fmt.Sprintf("rollback budget exhausted for scope %s: used %d of %d attempts", unit.Scope, used, maxAttempts),
		}
	}
	return &RollbackDecision{
		Action:       action,
		TargetScope:  unit.Scope,
		NewAttempt:   nextAttempt,
		ReuseSession: reuseSessionFor(unit.ReusePolicy, signal.FailureClass),
		Rationale:    signal.Reason,
	}
}

func (r *RollbackRunner) RecordRollbackEvent(rctx RollbackContext, decision *RollbackDecision) error {
	if rctx.Lineage == nil || decision == nil {
		return nil
	}
	now := time.Now().UTC()
	scope := decision.TargetScope
	if scope == "" {
		scope = rctx.Unit.Scope
	}
	return rctx.Lineage.Append(AttemptEvent{
		RunID:      rctx.RunID,
		NodeID:     rctx.NodeID,
		NodeKind:   rctx.NodeKind,
		Scope:      scope,
		Attempt:    decision.NewAttempt,
		Trigger:    "rollback_decision",
		Decision:   decision,
		StartedAt:  now,
		FinishedAt: now,
	})
}

func rollbackActionForSignal(spec RollbackSpec, fc FailureClass) RollbackAction {
	switch fc {
	case FailureClassContractError:
		return spec.OnContract
	case FailureClassHumanReject:
		return spec.OnHumanReject
	case FailureClassProductDefect:
		return spec.OnProductDefect
	default:
		return ""
	}
}

func rollbackAttemptsForScope(scope string, events []AttemptEvent) (int, int) {
	used := 0
	maxAttempt := 0
	for _, event := range events {
		if event.Scope != scope {
			continue
		}
		used++
		if event.Attempt >= maxAttempt {
			maxAttempt = event.Attempt + 1
		}
	}
	if maxAttempt == 0 {
		maxAttempt = 1
	}
	return used, maxAttempt
}

func reuseSessionFor(policy SessionReusePolicy, fc FailureClass) bool {
	switch policy {
	case SessionReuseSameSession:
		return true
	case SessionReuseOnHumanReject:
		return fc == FailureClassHumanReject
	default:
		return false
	}
}

func rollbackBudgetEvents(events []AttemptEvent) []AttemptEvent {
	filtered := make([]AttemptEvent, 0, len(events))
	for _, event := range events {
		if event.Trigger == "initial" {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func syntheticRollbackAttempts(scope string, revisions int) []AttemptEvent {
	events := make([]AttemptEvent, 0, revisions)
	for attempt := 1; attempt <= revisions; attempt++ {
		events = append(events, AttemptEvent{Scope: scope, Attempt: attempt})
	}
	return events
}
