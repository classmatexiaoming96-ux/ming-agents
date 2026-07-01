package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writePromotionStateMemory writes an active L2 project memory with the given
// promotion_state so recall/brief visibility gating can be exercised directly.
func writePromotionStateMemory(t *testing.T, id, title, body string, state PromotionState) {
	t.Helper()
	mem := Memory{
		ID:             id,
		Type:           "decision",
		Project:        "ming-agents",
		Tags:           []string{"workflow"},
		Title:          title,
		Body:           body,
		Score:          9,
		Status:         "active",
		Layer:          "l2",
		PromotionState: state,
		Inject:         "query",
		CreatedAt:      now().Format(dateLayout),
		ExpiresAt:      neverExpires,
	}
	if _, err := writeMemory(mem, filepath.Join(VaultDir, "notes", "ming-agents")); err != nil {
		t.Fatalf("writeMemory %s: %v", id, err)
	}
	if err := IndexMemory(mem.ID, mem.Title, mem.Body, mem.Project, mem.Type, mem.Tags); err != nil {
		t.Fatalf("IndexMemory %s: %v", id, err)
	}
}

func TestRecall_ExcludesNonPromotedPromotionStates(t *testing.T) {
	for _, state := range []PromotionState{PromotionCandidate, PromotionUnderReview, PromotionRejected} {
		t.Run(string(state), func(t *testing.T) {
			useTempVault(t)
			fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
			writePromotionStateMemory(t, "gated", "Retry token orbitneedle", "Retry once on orbitneedle timeout before fallback.", state)
			got, _, err := Recall("orbitneedle", "ming-agents", "", nil, 0, "active", 10)
			if err != nil {
				t.Fatalf("Recall error = %v", err)
			}
			if hasMemoryTitle(got, "Retry token orbitneedle") {
				t.Fatalf("promotion_state %q must not be recall-visible: %+v", state, got)
			}
		})
	}
}

func TestRecall_IncludesPromotedPromotionState(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	writePromotionStateMemory(t, "promoted", "Retry token orbitneedle", "Retry once on orbitneedle timeout before fallback.", PromotionPromoted)
	got, _, err := Recall("orbitneedle", "ming-agents", "", nil, 0, "active", 10)
	if err != nil {
		t.Fatalf("Recall error = %v", err)
	}
	if !hasMemoryTitle(got, "Retry token orbitneedle") {
		t.Fatalf("promoted memory must be recall-visible: %+v", got)
	}
}

func TestBrief_ExcludesNonPromotedAlwaysInject(t *testing.T) {
	useTempVault(t)
	fixedNow(t, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	mem := Memory{
		ID:             "cand-always",
		Type:           "decision",
		Project:        "ming-agents",
		Tags:           []string{"workflow"},
		Title:          "Candidate always memory",
		Body:           "This candidate should never be force-injected.",
		Score:          9,
		Status:         "active",
		Layer:          "l2",
		PromotionState: PromotionCandidate,
		Inject:         "always",
		CreatedAt:      now().Format(dateLayout),
		ExpiresAt:      neverExpires,
	}
	if _, err := writeMemory(mem, filepath.Join(VaultDir, "notes", "ming-agents")); err != nil {
		t.Fatalf("writeMemory: %v", err)
	}
	block, audit, err := Brief("ming-agents", "", Budget{MaxTokens: 4000})
	if err != nil {
		t.Fatalf("Brief error = %v", err)
	}
	if audit.AlwaysCount != 0 || strings.Contains(block, "Candidate always memory") {
		t.Fatalf("candidate always memory injected: count=%d block=%q", audit.AlwaysCount, block)
	}
}
