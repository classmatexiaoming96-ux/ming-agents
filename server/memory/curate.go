package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// promotionMu serialises the file-commit critical section of promotion and
// curation so a supersede + write + index sequence cannot interleave with a
// concurrent promotion, migration, or another curation of the same vault.
var promotionMu sync.Mutex

// CurationRequest is the input for an L2 -> L1 promotion. L1 affects every
// project, so the workflow is human-only: Approver.Kind must be "human" and
// Rationale is mandatory. DryRun is the safe default at the CLI layer.
type CurationRequest struct {
	SourceID     string
	Rationale    string
	Approver     PromotionActor
	ConflictMode string // "" (reject on conflict) | "supersede" | "allow_separate"
	Supersedes   []string
	DryRun       bool
}

// PromotionResult reports what a promotion or curation produced (or would have
// produced in dry-run). It always carries the conflict report so operators can
// see why an action was blocked.
type PromotionResult struct {
	SourceID       string
	TargetID       string
	AuditEventID   string
	FromState      PromotionState
	ToState        PromotionState
	ConflictReport ConflictReport
	DryRun         bool
}

// ConflictReport summarises how a candidate compares against active L1 memories.
type ConflictReport struct {
	HasBlockingConflict bool
	PossibleDuplicates  []string
	PossibleConflicts   []string
	RecommendedAction   string
}

// L1NotesPath is the global authority namespace. Keeping L1 memories in a
// dedicated folder keeps global policy separate from per-project L2 notes.
func L1NotesPath() string {
	return filepath.Join(VaultDir, "notes", "_global")
}

// l1MemoryID derives a stable global id from the promoted content.
func l1MemoryID(title, body string) string {
	sum := sha256.Sum256([]byte("l1\x00" + strings.TrimSpace(title) + "\x00" + strings.TrimSpace(body)))
	return "l1_" + hex.EncodeToString(sum[:])[:16]
}

// activeL1Memories returns promoted, active global memories by scanning the
// dedicated L1 namespace directly. It cannot use readAllMemories' project filter
// because L1 memories retain their originating project in frontmatter.
func activeL1Memories() ([]Memory, error) {
	root := L1NotesPath()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}
	var out []Memory
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		mem, body, err := parseFrontmatter(string(raw))
		if err != nil {
			return err
		}
		mem.Body = body
		mem.Path = path
		if mem.Layer == "l1" && mem.Status == "active" && ResolvePromotionState(mem) == PromotionPromoted {
			out = append(out, mem)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DetectL1Conflicts compares a candidate against active L1 memories using a
// deliberately simple lexical heuristic (no LLM): high title/body similarity or
// tag overlap flags a relationship, and opposite polarity turns that into a
// blocking contradiction. Duplicates are surfaced but do not block by default.
func DetectL1Conflicts(candidate Memory, existing []Memory) ConflictReport {
	report := ConflictReport{RecommendedAction: "approve"}
	candText := candidate.Title + " " + candidate.Body
	candNeg := hasNegation(candText)
	for _, m := range existing {
		sim := bigramJaccard(candText, m.Title+" "+m.Body)
		tagOverlap := anyTagMatch(candidate.Tags, m.Tags)
		related := sim >= 0.5 || (tagOverlap && sim >= 0.3)
		if !related {
			continue
		}
		if candNeg != hasNegation(m.Title+" "+m.Body) {
			report.PossibleConflicts = append(report.PossibleConflicts, m.ID)
			report.HasBlockingConflict = true
			report.RecommendedAction = "reject_or_supersede"
			continue
		}
		if sim >= 0.8 {
			report.PossibleDuplicates = append(report.PossibleDuplicates, m.ID)
			if report.RecommendedAction == "approve" {
				report.RecommendedAction = "review_duplicate"
			}
		}
	}
	return report
}

// Curate promotes an L2 project memory into the global L1 layer. It enforces the
// human-only rule, mandatory rationale, and conflict gating. Dry-run performs
// all checks and returns the conflict report without writing anything or
// appending audit. On a blocking conflict without supersede mode it appends a
// "blocked" audit event and returns an error.
func Curate(req CurationRequest) (*PromotionResult, error) {
	if req.Approver.Kind != "human" || strings.TrimSpace(req.Approver.Name) == "" {
		return nil, fmt.Errorf("L2 -> L1 curation requires a human approver")
	}
	if strings.TrimSpace(req.Rationale) == "" {
		return nil, fmt.Errorf("curation requires a rationale")
	}
	source, err := loadMemoryByID(req.SourceID)
	if err != nil {
		return nil, err
	}
	// L1 is the global authority layer: only an active, promoted L2 project
	// memory may be curated into it. This rejects l1 (already global), l2_inbox
	// candidates, archived/superseded memories, and under_review/rejected items,
	// enforcing the design rule that there is no direct candidate -> L1 path.
	if sourceState := ResolvePromotionState(source); source.Layer != "l2" || source.Status != "active" || sourceState != PromotionPromoted {
		return nil, fmt.Errorf(
			"curation source %q must be an active, promoted L2 memory (got layer=%q status=%q promotion_state=%q)",
			req.SourceID, source.Layer, source.Status, sourceState)
	}

	existing, err := activeL1Memories()
	if err != nil {
		return nil, err
	}
	conflict := DetectL1Conflicts(source, existing)

	result := &PromotionResult{
		SourceID:       req.SourceID,
		FromState:      ResolvePromotionState(source),
		ConflictReport: conflict,
		DryRun:         req.DryRun,
	}

	supersede := req.ConflictMode == "supersede"
	if conflict.HasBlockingConflict && !supersede && req.ConflictMode != "allow_separate" {
		if !req.DryRun {
			eventID, auditErr := appendPromotionAudit(PromotionAuditEvent{
				EventType:   PromotionEventBlocked,
				Actor:       req.Approver,
				SourceID:    req.SourceID,
				FromState:   result.FromState,
				ToState:     result.FromState,
				Outcome:     "blocked",
				Rationale:   req.Rationale,
				ConflictIDs: conflict.PossibleConflicts,
			})
			if auditErr != nil {
				return nil, auditErr
			}
			result.AuditEventID = eventID
		}
		return result, fmt.Errorf("L1 conflict with %v; rerun with --mode supersede to replace", conflict.PossibleConflicts)
	}

	targetID := l1MemoryID(source.Title, source.Body)
	result.TargetID = targetID
	result.ToState = PromotionPromoted

	if req.DryRun {
		return result, nil
	}

	// Determine which existing L1 memories this promotion replaces.
	supersededIDs := append([]string(nil), req.Supersedes...)
	if supersede {
		for _, id := range conflict.PossibleConflicts {
			supersededIDs = appendUnique(supersededIDs, id)
		}
	}

	// The file mutations below must be atomic relative to other promotion,
	// curation, and migration work: hold the vault promotion lock for the whole
	// commit so no concurrent writer can observe a half-applied supersession.
	promotionMu.Lock()
	defer promotionMu.Unlock()

	// Load the memories this promotion supersedes before mutating anything so a
	// missing/invalid id fails the whole curation before the old L1 is touched.
	olds := make([]Memory, 0, len(supersededIDs))
	for _, oldID := range supersededIDs {
		old, err := loadMemoryByID(oldID)
		if err != nil {
			return nil, err
		}
		olds = append(olds, old)
	}

	// Pre-compute the audit event so the new L1 can reference it, but do not
	// append it until every file write has succeeded (the audit must not claim a
	// promotion that did not commit).
	promotedEvent := PromotionAuditEvent{
		EventType:    PromotionEventPromoted,
		Actor:        req.Approver,
		SourceID:     req.SourceID,
		TargetID:     targetID,
		FromState:    result.FromState,
		ToState:      PromotionPromoted,
		Outcome:      "promoted",
		Rationale:    req.Rationale,
		EvidenceRefs: nonEmpty(source.EvidenceRef),
		SourceRunIDs: source.SourceRunIDs,
		ConflictIDs:  supersededIDs,
	}
	promotedEvent = prepareAuditEvent(promotedEvent)

	l1 := source
	l1.ID = targetID
	l1.Layer = "l1"
	l1.Status = "active"
	l1.CrossProject = false
	l1.PromotionState = PromotionPromoted
	l1.PromotedBy = req.Approver.Name
	l1.PromotedAt = now().UTC().Format(dateLayout)
	l1.PromotionAudit = auditReferenceForEvent(promotedEvent)
	l1.SourceRunIDs = source.SourceRunIDs
	if len(supersededIDs) > 0 {
		l1.Supersedes = supersededIDs
	}
	l1.Body = source.Body

	// Commit the new L1 first. If this fails, no old memory has been retired and
	// no audit has been written, so the vault is unchanged and the previous L1s
	// stay active.
	if _, err := writeMemory(l1, L1NotesPath()); err != nil {
		return nil, err
	}

	// Now retire the superseded memories. Each write is followed by the index
	// delete; the pre-loaded olds guarantee we never mutate a memory we could
	// not read.
	supersededEvents := make([]PromotionAuditEvent, 0, len(olds))
	for _, old := range olds {
		event, err := commitL1Supersession(old, targetID, req)
		if err != nil {
			return nil, err
		}
		supersededEvents = append(supersededEvents, event)
	}

	if err := IndexMemory(l1.ID, l1.Title, l1.Body, l1.Project, l1.Type, l1.Tags); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] FTS5 index error for %s: %v\n", l1.ID, err)
	}

	// All file state committed: append audit last so a mid-commit failure never
	// leaves an audit record for a promotion that did not happen.
	if err := appendPreparedAudit(promotedEvent); err != nil {
		return nil, err
	}
	result.AuditEventID = promotedEvent.EventID
	for _, event := range supersededEvents {
		if err := appendPreparedAudit(event); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// commitL1Supersession flips an existing L1 memory to superseded, records the
// winner, and drops it from the recall index. It returns the prepared (but not
// yet appended) superseded audit event so the caller can append it only after
// the whole curation transaction succeeds.
func commitL1Supersession(old Memory, winnerID string, req CurationRequest) (PromotionAuditEvent, error) {
	old.Status = "superseded"
	old.PromotionState = PromotionSuperseded
	old.SupersededBy = winnerID
	old.SupersededAt = now().UTC().Format(dateLayout)
	old.SupersededReason = req.Rationale
	dir := L1NotesPath()
	if old.Path != "" {
		dir = filepath.Dir(old.Path)
	}
	event := prepareAuditEvent(PromotionAuditEvent{
		EventType: PromotionEventSuperseded,
		Actor:     req.Approver,
		SourceID:  old.ID,
		TargetID:  winnerID,
		FromState: PromotionPromoted,
		ToState:   PromotionSuperseded,
		Outcome:   "superseded",
		Rationale: req.Rationale,
	})
	if _, err := writeMemory(old, dir); err != nil {
		return PromotionAuditEvent{}, err
	}
	if err := DeleteFromIndex(old.ID); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] FTS5 delete error for %s: %v\n", old.ID, err)
	}
	return event, nil
}

func nonEmpty(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}
