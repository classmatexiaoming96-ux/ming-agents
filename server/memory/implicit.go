// Package memory is a self-evolving memory system.
package memory

// This file implements Phase 2: Implicit Feedback (§6-§8 of the design).
// It detects whether recalled memories were actually used in a conversation
// turn and adjusts their weights with a one-turn probation period to avoid
// false positives.
//
// Key concepts:
// - ReferenceDetector: callable that returns 0..1 similarity between a memory
//   body and the assistant's reply text. Default = char-bigram Jaccard + short
//   memory fallback.
// - ContainsContradiction: callable that returns true if the reply contradicts
//   this specific memory. Default = per-memory scoped sentence check with
//   explicit correction markers.
// - ImplicitFeedback: main entry point; applies probation logic and persists.

import (
	"fmt"
	"log"
	"math"
	"path/filepath"
	"regexp"
	"strings"
)

// Tunables — mirror the Python memory_api.py constants.
const (
	implicitRefThreshold = 0.5
	implicitBoost        = 0.1
	contradictPenalty    = 0.10
)

// Injectable detectors — same indirection style as `var now = time.Now`.
var (
	// ReferenceDetector computes the reference strength of a memory body
	// against the conversation log, returning a value clamped to [0, 1].
	ReferenceDetector = bigramReference

	// ContainsContradiction returns true if the conversation log contradicts
	// the given memory body (scoped per-memory via sentence-level check).
	ContainsContradiction = scopedContradiction
)

// ImplicitFeedbackResult is the per-memory outcome of ImplicitFeedback.
type ImplicitFeedbackResult struct {
	ID         string  `json:"id"`
	Found      bool    `json:"found"`
	Outcome    string  `json:"outcome"` // confirmed | contradicted | ignored
	Referenced bool    `json:"referenced"`
	Reference  float64 `json:"reference_score"` // clamped [0, 1]
	HitCount   int     `json:"hit_count"`
	Score      float64 `json:"score"`
	Pending    float64 `json:"pending_score"`
}

// sentenceRE splits text into sentences for contradiction scoping.
var sentenceRE = regexp.MustCompile(`[。．.!?！？\n]`)

// correctionMarkers are explicit negation/error words that signal a correction.
// Soft hedges (并不, 其实, 应该, 只是, actually) are excluded because they
// routinely appear in correct, non-corrective replies.
var correctionMarkers = []string{
	// Chinese
	"不对", "不是", "错了", "纠正", "搞错",
	// English
	"wrong", "incorrect", "not correct", "not right", "my mistake",
	"that's not", "isn't right",
}

// softHedgeWords are excluded from contradiction detection.
var softHedgeWords = map[string]bool{
	"其实": true, "并不": true, "应该": true, "只是": true, "actually": true,
}

// shortMemoryFallback computes char-bigram containment: the fraction of the
// memory's shingles that appear in the reply (|mem ∩ reply| / |mem|). Used as
// the §6 reference primitive for memories of any length (A3).
func shortMemoryFallback(memBigrams, replyBigrams map[string]bool) float64 {
	hits := 0
	for g := range memBigrams {
		if replyBigrams[g] {
			hits++
		}
	}
	return float64(hits) / float64(len(memBigrams))
}

// bigramReference implements ReferenceDetector as char-bigram CONTAINMENT (A3):
// "what fraction of the memory's bigrams appear in the reply", i.e.
// |memBigrams ∩ replyBigrams| / |memBigrams|.
//
// The old implementation used Jaccard against the whole conversation, whose
// denominator |mem ∪ reply| is dominated by the (much larger) reply: a 50-bigram
// memory quoted verbatim inside a 2000-bigram turn scored ~0.025, far below the
// 0.5 threshold, so the implicit-feedback pipeline was effectively always off.
// Containment is invariant to reply length and reads directly as the 0.5
// threshold's intent ("at least half the memory showed up"), and it unifies with
// what shortMemoryFallback already did for very short memories.
func bigramReference(memBody, conversationLog string) float64 {
	memBigrams := charBigrams(memBody)
	if len(memBigrams) == 0 {
		return 0
	}
	replyBigrams := charBigrams(conversationLog)
	return shortMemoryFallback(memBigrams, replyBigrams)
}

// scopedContradiction implements ContainsContradiction by checking only
// sentences that reference the memory (via bigram overlap), then looking for
// correction markers in those sentences.
func scopedContradiction(memBody, conversationLog string) bool {
	// Split conversation into sentences.
	sentences := sentenceRE.Split(conversationLog, -1)
	if len(sentences) == 0 {
		return false
	}

	memBigrams := charBigrams(memBody)
	if len(memBigrams) == 0 {
		return false
	}

	for _, sentence := range sentences {
		if sentence == "" {
			continue
		}
		sentenceBigrams := charBigrams(sentence)

		// Check if this sentence references the memory at all.
		// A sentence with no shared bigrams is irrelevant to this memory.
		shared := 0
		for g := range memBigrams {
			if sentenceBigrams[g] {
				shared++
			}
		}
		if shared == 0 {
			continue // irrelevant sentence — skip it
		}

		// This sentence references the memory — check for correction markers.
		lower := strings.ToLower(sentence)

		// A2: if the sentence contains a soft hedge (其实/并不/actually…), be
		// conservative and treat it as non-corrective. The previous code (a) used
		// strings.Fields, which never tokenises space-free CJK so 其实/并不 were
		// never matched, and (b) only `break`ed the inner loop and then fell
		// through to `return true`, so the exclusion never actually took effect.
		// Substring matching is CJK-safe and `continue` correctly skips the
		// sentence.
		hedged := false
		for hedge := range softHedgeWords {
			if strings.Contains(lower, hedge) {
				hedged = true
				break
			}
		}
		if hedged {
			continue
		}

		for _, marker := range correctionMarkers {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

// clamp clamps v to the range [lo, hi].
func clamp(v float64, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ImplicitFeedback analyses one conversation turn and adjusts weights for
// recalled memories. It returns one result per id (unknown ids → Found=false,
// not an error).
//
// The probation period works as follows:
// - confirmed (ref ≥ threshold, not contradicted): pending_score is staged;
//   if there's an existing pending_score from the previous turn, it is
//   promoted to score (survived observation).
// - contradicted (ref ≥ threshold, contradicted): pending_score is discarded,
//   a small penalty is applied to score.
// - ignored (ref < threshold): no state change.
func ImplicitFeedback(memoryIDs []string, conversationLog string) ([]ImplicitFeedbackResult, error) {
	var results []ImplicitFeedbackResult

	for _, id := range memoryIDs {
		res := ImplicitFeedbackResult{ID: id, Found: false}
		results = append(results, res)
	}

	// Collect all active memories for lookup.
	active, err := readAllMemories("active", "")
	if err != nil {
		return nil, fmt.Errorf("readAllMemories: %w", err)
	}
	byID := map[string]Memory{}
	for _, m := range active {
		byID[m.ID] = m
	}

	for i, id := range memoryIDs {
		mem, ok := byID[id]
		if !ok {
			// Memory not found — Found=false, Outcome=ignored.
			results[i] = ImplicitFeedbackResult{
				ID:         id,
				Found:      false,
				Outcome:    "ignored",
				Referenced: false,
				Reference:  0,
			}
			continue
		}

		// Compute reference and contradiction, clamped to [0, 1].
		ref := clamp(ReferenceDetector(mem.Body, conversationLog), 0, 1)
		contradicted := ContainsContradiction(mem.Body, conversationLog)

		// §12 refThreshold calibration: log every score so we can tune threshold
		// against real conversation logs. Remove or guard with a debug flag for
		// production if the log volume is too high.
		log.Printf("[memory:implicit] id=%s ref=%.4f threshold=%.2f referenced=%v",
			id, ref, implicitRefThreshold, ref >= implicitRefThreshold)

		referenced := ref >= implicitRefThreshold

		if !referenced {
			// ignored — no state change.
			results[i] = ImplicitFeedbackResult{
				ID:         id,
				Found:      true,
				Outcome:    "ignored",
				Referenced: false,
				Reference:  ref,
				HitCount:   mem.HitCount,
				Score:      mem.Score,
				Pending:    mem.PendingScore,
			}
			continue
		}

		// Referenced memory — classify outcome.
		if contradicted {
			// contradicted: discard pending, apply penalty, hit_count++.
			// implicit_hits NOT incremented.
			mem.PendingScore = 0
			mem.Score = math.Max(0, mem.Score-contradictPenalty)
			mem.HitCount++
			mem.LastImplicit = now().Format(dateLayout)

			results[i] = ImplicitFeedbackResult{
				ID:         id,
				Found:      true,
				Outcome:    "contradicted",
				Referenced: true,
				Reference:  ref,
				HitCount:   mem.HitCount,
				Score:      mem.Score,
				Pending:    mem.PendingScore,
			}
		} else {
			// confirmed: promote any existing pending, stage new pending,
			// implicit_hits++, hit_count++.
			if mem.PendingScore > 0 {
				mem.Score = round1(mem.Score + mem.PendingScore)
				mem.PendingScore = 0
				mem.PromotedHits++
			}
			mem.PendingScore = implicitBoost
			mem.ImplicitHits++
			mem.HitCount++
			mem.LastImplicit = now().Format(dateLayout)

			results[i] = ImplicitFeedbackResult{
				ID:         id,
				Found:      true,
				Outcome:    "confirmed",
				Referenced: true,
				Reference:  ref,
				HitCount:   mem.HitCount,
				Score:      mem.Score,
				Pending:    mem.PendingScore,
			}
		}

		// Persist the updated memory.
		targetDir := filepath.Dir(mem.Path)
		if targetDir == "" || targetDir == "." {
			// Memory has no on-disk path — reconstruct from vault structure.
			if mem.Status == "active" && mem.Project != "" {
				targetDir = filepath.Join(VaultDir, "notes", mem.Project)
			} else {
				targetDir = filepath.Join(VaultDir, "inbox")
			}
		}
		if _, err := writeMemory(mem, targetDir); err != nil {
			return nil, fmt.Errorf("writeMemory %s: %w", id, err)
		}
	}

	return results, nil
}