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
	source, err := loadMemoryByID(req.SourceID)
	if err != nil {
		return nil, err
	}
	// Merge any operator-supplied evidence/run ids so the promoted memory and the
	// eligibility check see the complete provenance.
	if len(req.EvidenceRefs) > 0 && source.EvidenceRef == "" {
		source.EvidenceRef = req.EvidenceRefs[0]
	}
	for _, r := range req.SourceRunIDs {
		source.SourceRunIDs = appendUnique(source.SourceRunIDs, r)
	}

	report := evaluateL3ToL2(source, DefaultL3ToL2Threshold)
	fromState := ResolvePromotionState(source)
	result := &PromotionResult{
		SourceID:  req.SourceID,
		FromState: fromState,
	}

	// The single-run human override still requires a rationale, a human actor,
	// and at least one evidence ref, per the high-severity incident path.
	override := req.HumanOverride && req.Actor.Kind == "human" && source.EvidenceRef != ""
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
				EvidenceRefs: nonEmpty(source.EvidenceRef),
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

	eventID, err := appendPromotionAudit(PromotionAuditEvent{
		EventType:    PromotionEventPromoted,
		Actor:        req.Actor,
		SourceID:     req.SourceID,
		TargetID:     targetID,
		FromState:    fromState,
		ToState:      PromotionPromoted,
		Outcome:      "promoted",
		Rationale:    req.Rationale,
		EvidenceRefs: nonEmpty(source.EvidenceRef),
		SourceRunIDs: source.SourceRunIDs,
	})
	if err != nil {
		return nil, err
	}
	result.AuditEventID = eventID

	l2 := source
	l2.ID = targetID
	l2.Layer = "l2"
	l2.Status = "active"
	l2.CrossProject = false
	l2.PromotionState = PromotionPromoted
	l2.PromotedBy = req.Actor.Name
	l2.PromotedAt = now().UTC().Format(dateLayout)
	l2.PromotionAudit = auditReference(eventID)
	if _, err := writeMemory(l2, filepath.Join(VaultDir, "notes", source.Project)); err != nil {
		return nil, err
	}
	if err := IndexMemory(l2.ID, l2.Title, l2.Body, l2.Project, l2.Type, l2.Tags); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] FTS5 index error for %s: %v\n", l2.ID, err)
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

	eventID, err := appendPromotionAudit(PromotionAuditEvent{
		EventType: PromotionEventRevoked,
		Actor:     req.Actor,
		SourceID:  req.TargetID,
		TargetID:  req.SupersededBy,
		FromState: fromState,
		ToState:   toState,
		Outcome:   "revoked",
		Rationale: req.Reason,
	})
	if err != nil {
		return nil, err
	}
	result.AuditEventID = eventID

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
	dir := filepath.Dir(target.Path)
	if target.Path == "" {
		dir = filepath.Join(VaultDir, "notes", target.Project)
	}
	if _, err := writeMemory(target, dir); err != nil {
		return nil, err
	}
	if err := DeleteFromIndex(req.TargetID); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] FTS5 delete error for %s: %v\n", req.TargetID, err)
	}
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
				EvidenceRefs: nonEmpty(m.EvidenceRef), SourceRunIDs: m.SourceRunIDs,
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
				EvidenceRefs: nonEmpty(m.EvidenceRef), SourceRunIDs: m.SourceRunIDs,
				Eligible: true, ReadyForReview: true,
			})
		default:
			return nil, fmt.Errorf("list-pending-promotion supports --to l2 or l1, got %q", toLayer)
		}
	}
	return out, nil
}
