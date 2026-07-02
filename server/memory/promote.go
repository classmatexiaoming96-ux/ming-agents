package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PromotionRequest is the input for an L3 -> L2 promotion. Automation may
// evaluate eligibility, but the actual promotion is an operator action. When the
// evidence threshold is not met, a human actor may still promote with
// HumanOverride, a rationale, and at least one evidence ref (the single-run
// incident path from the design).
type PromotionRequest struct {
	SourceID      string
	TargetLayer   string
	Rationale     string
	Actor         PromotionActor
	EvidenceRefs  []string
	SourceRunIDs  []string
	HumanOverride bool
	DryRun        bool
}

// RevokeRequest retires a promoted memory without deleting it. Mode "archive"
// (default) marks it archived; mode "supersede" records a replacement.
type RevokeRequest struct {
	TargetID     string
	Reason       string
	Mode         string
	SupersededBy string
	Actor        PromotionActor
	DryRun       bool
}

// RevokeResult reports the outcome of a revoke.
type RevokeResult struct {
	TargetID     string
	AuditEventID string
	FromState    PromotionState
	ToState      PromotionState
	DryRun       bool
}

// PendingPromotion is one row in list-pending-promotion: a source together
// with its eligibility verdict for the target layer.
type PendingPromotion struct {
	ID              string
	Project         string
	Title           string
	FromLayer       string
	ToLayer         string
	State           PromotionState
	EvidenceRefs    []string
	SourceRunIDs    []string
	Eligible        bool
	ReadyForReview  bool
	BlockingReasons []string
}

// PromotionFilter narrows list-pending-promotion.
type PromotionFilter struct {
	Project   string
	ToLayer   string
	ReadyOnly bool
}

// promotedL2ID derives a stable L2 id for content promoted from a candidate.
func promotedL2ID(project, title, body string) string {
	sum := sha256.Sum256([]byte("l2\x00" + project + "\x00" + strings.TrimSpace(title) + "\x00" + strings.TrimSpace(body)))
	return "l2_" + hex.EncodeToString(sum[:])[:16]
}

// Promote turns an L3-backed candidate into an authoritative L2 project memory.
// It refuses target layer l1 (that is human-only Curate). It is an atomic
// review-and-promote: internally the candidate passes through review before
// becoming promoted, which is why it does not use the direct candidate->promoted
// edge. Dry-run runs all checks and returns the eligibility verdict without
// writing or appending audit.
func Promote(req PromotionRequest) (*PromotionResult, error) {
	if req.TargetLayer == "l1" {
		return nil, fmt.Errorf("promotion to l1 is human-only; use Curate")
	}
	if req.TargetLayer != "l2" {
		return nil, fmt.Errorf("promotion supports target layer l2, got %q", req.TargetLayer)
	}
	if strings.TrimSpace(req.Rationale) == "" {
		return nil, fmt.Errorf("promotion requires a rationale")
	}
	// L3 -> L2 promotion is human-confirmed by default: an apply must carry a
	// human actor with a name. Service actors cannot promote until a signed
	// service policy exists.
	if req.Actor.Kind != "human" || strings.TrimSpace(req.Actor.Name) == "" {
		return nil, fmt.Errorf("promotion to l2 requires a human actor with a name")
	}
	source, err := loadMemoryByID(req.SourceID)
	if err != nil {
		return nil, err
	}
	// Only a candidate or under_review source may be promoted. This blocks
	// re-promoting an already-promoted L2/L1 authority memory or resurrecting an
	// archived/superseded/rejected one through the promotion path.
	fromState := ResolvePromotionState(source)
	if fromState != PromotionCandidate && fromState != PromotionUnderReview {
		return nil, fmt.Errorf("promotion source %q must be a candidate or under_review memory, got %q", req.SourceID, fromState)
	}
	// Merge any operator-supplied evidence so the promoted memory and the
	// eligibility check see the complete provenance. SourceRunIDs are derived
	// from the evidence refs (each ref names the run that backs it) so the count
	// reflects evidenced runs, not bare run ids.
	for _, ref := range req.EvidenceRefs {
		source.EvidenceRefs = appendUnique(source.EvidenceRefs, ref)
	}
	if source.EvidenceRef != "" {
		source.EvidenceRefs = appendUnique(source.EvidenceRefs, source.EvidenceRef)
	}
	source.SourceRunIDs = nil
	for _, ref := range allEvidenceRefs(source) {
		if runID := runIDFromEvidenceRef(ref); runID != "" {
			source.SourceRunIDs = appendUnique(source.SourceRunIDs, runID)
		}
	}

	report := evaluateL3ToL2(source, DefaultL3ToL2Threshold)
	result := &PromotionResult{
		SourceID:  req.SourceID,
		FromState: fromState,
	}

	// The single-run human override still requires a rationale, a human actor,
	// and at least one evidence ref, per the high-severity incident path.
	override := req.HumanOverride && req.Actor.Kind == "human" && len(allEvidenceRefs(source)) > 0
	if !report.Eligible && !override {
		if !req.DryRun {
			eventID, auditErr := appendPromotionAudit(PromotionAuditEvent{
				EventType:    PromotionEventBlocked,
				Actor:        req.Actor,
				SourceID:     req.SourceID,
				FromState:    fromState,
				ToState:      fromState,
				Outcome:      "blocked",
				Rationale:    req.Rationale,
				EvidenceRefs: allEvidenceRefs(source),
				SourceRunIDs: source.SourceRunIDs,
			})
			if auditErr != nil {
				return nil, auditErr
			}
			result.AuditEventID = eventID
		}
		return result, fmt.Errorf("candidate %q not eligible for L2: %s", req.SourceID, strings.Join(report.BlockingReasons, ","))
	}

	targetID := promotedL2ID(source.Project, source.Title, source.Body)
	result.TargetID = targetID
	result.ToState = PromotionPromoted
	result.DryRun = req.DryRun
	if req.DryRun {
		return result, nil
	}

	// Promote is an atomic review-and-promote: the candidate passes through
	// review before promotion. Validate the under_review -> promoted edge so the
	// terminal transition is the one the state machine actually allows (there is
	// no direct candidate -> promoted edge).
	if err := ValidatePromotionTransition(PromotionUnderReview, PromotionPromoted); err != nil {
		return nil, err
	}

	// Serialise the file commit against concurrent promotion/curation/migration.
	promotionMu.Lock()
	defer promotionMu.Unlock()

	// Prepare (but do not append) the audit event so the promoted memory can
	// reference it. The audit is appended only after every write succeeds so a
	// mid-commit failure never leaves an audit record for a promotion that did
	// not happen.
	promotedEvent := prepareAuditEvent(PromotionAuditEvent{
		EventType:    PromotionEventPromoted,
		Actor:        req.Actor,
		SourceID:     req.SourceID,
		TargetID:     targetID,
		FromState:    fromState,
		ToState:      PromotionPromoted,
		Outcome:      "promoted",
		Rationale:    req.Rationale,
		EvidenceRefs: allEvidenceRefs(source),
		SourceRunIDs: source.SourceRunIDs,
	})

	l2 := source
	l2.ID = targetID
	l2.Layer = "l2"
	l2.Status = "active"
	l2.CrossProject = false
	l2.PromotionState = PromotionPromoted
	l2.PromotedBy = req.Actor.Name
	l2.PromotedAt = now().UTC().Format(dateLayout)
	l2.PromotionAudit = auditReferenceForEvent(promotedEvent)
	if _, err := writeMemory(l2, filepath.Join(VaultDir, "notes", source.Project)); err != nil {
		return nil, err
	}

	// Mark the originating candidate promoted so it stops surfacing in the
	// pending list. It stays on disk (append-only history) but is no longer a
	// pending candidate. Its l2_inbox layer keeps it excluded from recall.
	if source.Path != "" {
		marked := source
		marked.PromotionState = PromotionPromoted
		if _, err := writeMemory(marked, filepath.Dir(source.Path)); err != nil {
			return nil, err
		}
	}

	if err := IndexMemory(l2.ID, l2.Title, l2.Body, l2.Project, l2.Type, l2.Tags); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] FTS5 index error for %s: %v\n", l2.ID, err)
	}

	// All file state committed: append the audit last.
	if err := appendPreparedAudit(promotedEvent); err != nil {
		return nil, err
	}
	result.AuditEventID = promotedEvent.EventID
	return result, nil
}

// Revoke retires a promoted memory. It never deletes files: archive mode flips
// the memory to archived and removes it from recall/index; supersede mode
// records a replacement. Dry-run reports the intended transition only.
func Revoke(req RevokeRequest) (*RevokeResult, error) {
	if strings.TrimSpace(req.Reason) == "" {
		return nil, fmt.Errorf("revoke requires a reason")
	}
	mode := req.Mode
	if mode == "" {
		mode = "archive"
	}
	if mode != "archive" && mode != "supersede" {
		return nil, fmt.Errorf("revoke mode %q is not supported", mode)
	}
	if mode == "supersede" && req.SupersededBy == "" {
		return nil, fmt.Errorf("revoke supersede mode requires --superseded-by")
	}
	target, err := loadMemoryByID(req.TargetID)
	if err != nil {
		return nil, err
	}
	fromState := ResolvePromotionState(target)
	toState := PromotionArchived
	if mode == "supersede" {
		toState = PromotionSuperseded
	}
	result := &RevokeResult{
		TargetID:  req.TargetID,
		FromState: fromState,
		ToState:   toState,
		DryRun:    req.DryRun,
	}
	if req.DryRun {
		return result, nil
	}

	// Serialise the file commit against concurrent promotion/curation/migration.
	promotionMu.Lock()
	defer promotionMu.Unlock()

	revokedEvent := prepareAuditEvent(PromotionAuditEvent{
		EventType: PromotionEventRevoked,
		Actor:     req.Actor,
		SourceID:  req.TargetID,
		TargetID:  req.SupersededBy,
		FromState: fromState,
		ToState:   toState,
		Outcome:   "revoked",
		Rationale: req.Reason,
	})

	// Snapshot the target before mutation so an audit-append failure after the
	// write can restore it. Revoke owns the whole file+audit transaction: the
	// target must never end up retired (superseded/archived) without a durable
	// promotion audit record, even when the caller (contradiction resolve) only
	// rolls back the winner.
	origTarget := target
	dir := filepath.Dir(target.Path)
	if target.Path == "" {
		dir = filepath.Join(VaultDir, "notes", target.Project)
	}

	if mode == "supersede" {
		target.Status = "superseded"
		target.PromotionState = PromotionSuperseded
		target.SupersededBy = req.SupersededBy
		target.SupersededAt = now().UTC().Format(dateLayout)
		target.SupersededReason = req.Reason
	} else {
		target.Status = "archived"
		target.PromotionState = PromotionArchived
	}
	target.PromotionAudit = auditReferenceForEvent(revokedEvent)
	if _, err := writeMemory(target, dir); err != nil {
		return nil, err
	}
	if err := DeleteFromIndex(req.TargetID); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] FTS5 delete error for %s: %v\n", req.TargetID, err)
	}

	// Commit gate: append the audit last. If it fails, roll the target file back
	// to its pre-revoke state and re-index it so the target is not left retired
	// without an audit trail.
	if err := appendPreparedAudit(revokedEvent); err != nil {
		if _, rbErr := writeMemory(origTarget, dir); rbErr != nil {
			return nil, fmt.Errorf("revoke: audit append failed (%v) and target rollback failed: %w", err, rbErr)
		}
		if idxErr := IndexMemory(origTarget.ID, origTarget.Title, origTarget.Body, origTarget.Project, origTarget.Type, origTarget.Tags); idxErr != nil {
			fmt.Fprintf(os.Stderr, "[memory] FTS5 reindex error for %s: %v\n", origTarget.ID, idxErr)
		}
		return nil, err
	}
	result.AuditEventID = revokedEvent.EventID
	return result, nil
}

// ListPending returns candidates awaiting promotion. For ToLayer "l2" it lists
// candidate-state memories with their L3 eligibility verdict. For ToLayer "l1"
// it lists promoted L2 memories that could be curated into the global layer.
func ListPending(filter PromotionFilter) ([]PendingPromotion, error) {
	toLayer := filter.ToLayer
	if toLayer == "" {
		toLayer = "l2"
	}
	all, err := readAllMemories("active", "")
	if err != nil {
		return nil, err
	}
	var out []PendingPromotion
	for _, m := range all {
		if filter.Project != "" && m.Project != filter.Project {
			continue
		}
		state := ResolvePromotionState(m)
		switch toLayer {
		case "l2":
			if state != PromotionCandidate {
				continue
			}
			report := evaluateL3ToL2(m, DefaultL3ToL2Threshold)
			if filter.ReadyOnly && !report.Eligible && !report.ReadyForReview {
				continue
			}
			out = append(out, PendingPromotion{
				ID: m.ID, Project: m.Project, Title: m.Title,
				FromLayer: "l3", ToLayer: "l2", State: state,
				EvidenceRefs: allEvidenceRefs(m), SourceRunIDs: m.SourceRunIDs,
				Eligible: report.Eligible, ReadyForReview: report.ReadyForReview,
				BlockingReasons: report.BlockingReasons,
			})
		case "l1":
			if m.Layer != "l2" || state != PromotionPromoted {
				continue
			}
			out = append(out, PendingPromotion{
				ID: m.ID, Project: m.Project, Title: m.Title,
				FromLayer: "l2", ToLayer: "l1", State: state,
				EvidenceRefs: allEvidenceRefs(m), SourceRunIDs: m.SourceRunIDs,
				Eligible: true, ReadyForReview: true,
			})
		default:
			return nil, fmt.Errorf("list-pending-promotion supports --to l2 or l1, got %q", toLayer)
		}
	}
	return out, nil
}
