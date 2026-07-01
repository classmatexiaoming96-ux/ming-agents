package memory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PromotionActor identifies who performed a promotion action. Kind is "human"
// or "service"; L2 -> L1 curation requires a human actor.
type PromotionActor struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// PromotionAuditEvent is one append-only record explaining who changed memory
// authority and why. Blocked and rejected actions are logged too — a missing
// evidence ref is valuable because it explains why promotion did not happen.
type PromotionAuditEvent struct {
	SchemaVersion int            `json:"schema_version"`
	EventID       string         `json:"event_id"`
	EventType     string         `json:"event_type"`
	Timestamp     string         `json:"timestamp"`
	Actor         PromotionActor `json:"actor"`
	SourceID      string         `json:"source_id"`
	TargetID      string         `json:"target_id,omitempty"`
	FromState     PromotionState `json:"from_state"`
	ToState       PromotionState `json:"to_state"`
	Outcome       string         `json:"outcome"`
	Rationale     string         `json:"rationale"`
	EvidenceRefs  []string       `json:"evidence_refs,omitempty"`
	SourceRunIDs  []string       `json:"source_run_ids,omitempty"`
	ConflictIDs   []string       `json:"conflict_ids,omitempty"`
}

// Promotion audit event types.
const (
	PromotionEventReviewStarted = "review_started"
	PromotionEventPromoted      = "promoted"
	PromotionEventRejected      = "rejected"
	PromotionEventBlocked       = "blocked"
	PromotionEventRevoked       = "revoked"
	PromotionEventSuperseded    = "superseded"
)

// promotionAuditSchemaVersion versions the audit record shape.
const promotionAuditSchemaVersion = 1

// PromotionAuditDir is the vault-level, append-only namespace for promotion
// audit. It is intentionally separate from the per-run, frozen L3 bundles:
// promotion can occur weeks later and may involve multiple runs or L2 memories.
func PromotionAuditDir() string {
	return filepath.Join(VaultDir, "audit", "promotion")
}

// PromotionAuditPath returns the daily append-only JSONL log for the given time,
// partitioned by year and month: audit/promotion/YYYY/MM/promotion-YYYYMMDD.jsonl.
func PromotionAuditPath(t time.Time) string {
	t = t.UTC()
	return filepath.Join(
		PromotionAuditDir(),
		t.Format("2006"),
		t.Format("01"),
		"promotion-"+t.Format("20060102")+".jsonl",
	)
}

// legacyPromotionAuditDir is the pre-migration namespace under the runs tree. It
// is still read for backwards compatibility but never written to.
func legacyPromotionAuditDir() string {
	return filepath.Join(VaultDir, "runs", "_promotion_audit")
}

// legacyPromotionAuditPath returns the pre-migration daily log path.
func legacyPromotionAuditPath(t time.Time) string {
	return filepath.Join(legacyPromotionAuditDir(), t.UTC().Format(dateLayout)+".jsonl")
}

// newAuditEventID derives an id from the event's identifying fields plus random
// entropy so two actions on the same source/target within the same second still
// get distinct ids (the earlier second-resolution hash could collide).
func newAuditEventID(eventType, sourceID, targetID, timestamp string) string {
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		// Fall back to the nanosecond clock if the RNG is unavailable.
		binary.LittleEndian.PutUint64(entropy[:], uint64(now().UnixNano()))
	}
	sum := sha256.Sum256([]byte(eventType + "\x00" + sourceID + "\x00" + targetID + "\x00" + timestamp + "\x00" + string(entropy[:])))
	return "evt_" + hex.EncodeToString(sum[:])[:16]
}

// prepareAuditEvent stamps the schema version, timestamp, and event id onto an
// event without writing anything. This lets a caller reference the event (its
// id and log path) before committing file state, then append the record only
// after every mutation has succeeded.
func prepareAuditEvent(event PromotionAuditEvent) PromotionAuditEvent {
	if event.Timestamp == "" {
		event.Timestamp = now().UTC().Format(time.RFC3339)
	}
	event.SchemaVersion = promotionAuditSchemaVersion
	if event.EventID == "" {
		event.EventID = newAuditEventID(event.EventType, event.SourceID, event.TargetID, event.Timestamp)
	}
	return event
}

// auditEventTime returns the UTC time a prepared event should be filed under,
// derived from the event's own timestamp so its reference and its log line
// always agree (even across a UTC day boundary).
func auditEventTime(event PromotionAuditEvent) time.Time {
	if event.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, event.Timestamp); err == nil {
			return t.UTC()
		}
	}
	return now().UTC()
}

// appendPreparedAudit writes an already-prepared event to its daily JSONL log.
// It is the commit step of the prepare/commit split: callers append only after
// their file mutations have succeeded.
func appendPreparedAudit(event PromotionAuditEvent) error {
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	path := PromotionAuditPath(auditEventTime(event))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir promotion audit dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open promotion audit log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append promotion audit: %w", err)
	}
	return nil
}

// appendPromotionAudit prepares and appends an event in one step, returning the
// event id. It remains the convenience path for blocked/rejected events that do
// not mutate memory files.
func appendPromotionAudit(event PromotionAuditEvent) (string, error) {
	event = prepareAuditEvent(event)
	if err := appendPreparedAudit(event); err != nil {
		return "", err
	}
	return event.EventID, nil
}

// auditRelativePath returns the vault-relative log path for the given time,
// used to build a memory's promotion_audit reference.
func auditRelativePath(t time.Time) string {
	full := PromotionAuditPath(t)
	if rel, err := filepath.Rel(VaultDir, full); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(full)
}

// auditReferenceForEvent returns a vault-relative pointer to a prepared event's
// log line, using the event's own timestamp so the reference always points at
// the file the event was written to.
func auditReferenceForEvent(event PromotionAuditEvent) string {
	return auditRelativePath(auditEventTime(event)) + "#" + event.EventID
}

// auditReference returns a vault-relative pointer suitable for storing in a
// memory's PromotionAudit field, linking it to its append-only log line.
func auditReference(eventID string) string {
	return auditRelativePath(now().UTC()) + "#" + eventID
}

// ReadPromotionAudit reads all audit events for a given day, in append order.
// It reads the current audit/promotion path first and then the legacy
// runs/_promotion_audit path so pre-migration logs remain visible. It is used by
// tests and future indexers; it never mutates the log.
func ReadPromotionAudit(t time.Time) ([]PromotionAuditEvent, error) {
	var events []PromotionAuditEvent
	for _, path := range []string{PromotionAuditPath(t), legacyPromotionAuditPath(t)} {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, line := range splitJSONLines(data) {
			var event PromotionAuditEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				return nil, fmt.Errorf("decode audit event: %w", err)
			}
			events = append(events, event)
		}
	}
	return events, nil
}

func splitJSONLines(data []byte) []string {
	var out []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				out = append(out, string(data[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, string(data[start:]))
	}
	return out
}
