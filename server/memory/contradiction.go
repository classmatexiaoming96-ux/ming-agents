package memory

// Contradiction resolution (§13 矛盾淘汰机制). Detection and resolution are kept
// separate: multiple detectors (lexical at-rest, online implicit, external
// holographic) normalise their output into []Contradiction and funnel into the
// single resolution chokepoint ResolveContradictions, which applies one eviction
// policy and writes one audit trail. Eviction never runs on the hot path; it is
// a soft supersede (never hard-delete) and is fully auditable & reversible.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// §13.7 tunables (first guesses; calibrate with DryRun against real pairs).
const (
	resolveConfidence = 0.6 // min detector Confidence to even consider eviction
	evictMargin       = 1.0 // min survivorScore gap (score units) to name a winner
	recencyHalfLife   = 90  // days; recency weight halves every 90 days

	// survivorScore weights — evidence-first, recency-tempered, frequency-light.
	wScore    = 1.0
	wEvidence = 0.5
	wRecency  = 1.0
	wHits     = 0.1

	// lexicalContradictionScan heuristics.
	lexicalSimThreshold = 0.4 // min char-bigram overlap to count as "same subject"

	// implicitMarkerConfidence is the (low) confidence assigned to an online
	// conflicts_with marker on its own. Decision 2: online signals are never
	// trusted to evict — if the at-rest lexical scan can't independently
	// corroborate the pair with higher confidence, it abstains.
	implicitMarkerConfidence = 0.4
)

// contradictionsLog is the append-only audit file inside the vault.
const contradictionsLog = "_contradictions.jsonl"

// Contradiction is one candidate conflicting pair, detector-agnostic.
type Contradiction struct {
	A          string  `json:"a"` // canonicalised so A < B by id (stable pair key)
	B          string  `json:"b"`
	Similarity float64 `json:"similarity"` // same-subject overlap, 0..1
	Confidence float64 `json:"confidence"` // belief the pair is contradictory, 0..1; < resolveConfidence → abstain
	Source     string  `json:"source"`     // "holographic" | "implicit" | "lexical" | "manual"
	Detail     string  `json:"detail"`     // which entities/slots differ (for the audit log)
}

// PairKey returns the order-independent identity of the pair (A<B canonical).
func (c Contradiction) PairKey() string {
	a, b := c.A, c.B
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// ResolveOptions controls a resolution pass.
type ResolveOptions struct {
	DryRun    bool // compute + return decisions, write nothing (required for first rollout)
	AutoEvict bool // false = only flag conflicts_with, never supersede
}

// ResolutionResult is one decision from ResolveContradictions.
type ResolutionResult struct {
	Pair   [2]string `json:"pair"`             // [A, B] canonical
	Action string    `json:"action"`           // superseded | flagged | skipped
	Winner string    `json:"winner,omitempty"` // populated when a winner was determined
	Loser  string    `json:"loser,omitempty"`
	Margin float64   `json:"margin"`
	Reason string    `json:"reason"`
}

// ContradictionDetector is the injectable at-rest scanner. The default is the
// pure-Go lexical scan; an LLM judge or an external holographic source can
// override it. (Holographic itself is an external/optional source that feeds
// ResolveContradictions directly as Contradiction{Source:"holographic"} — it is
// NOT called from inside the Go package.)
var ContradictionDetector func(memories []Memory) []Contradiction = lexicalContradictionScan

// recencyFactor is 2^(-ageDays/recencyHalfLife) ∈ (0,1]; unparseable/missing
// CreatedAt yields the one-half-life neutral prior 0.5 rather than 0, so a
// parsing artifact can't strip a full wRecency unit (and thus tip an eviction)
// off a memory whose age we simply can't read.
func recencyFactor(m Memory) float64 {
	created, err := time.Parse(dateLayout, m.CreatedAt)
	if err != nil {
		return 0.5
	}
	ageDays := now().Sub(created).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	return math.Pow(2, -ageDays/recencyHalfLife)
}

// evidence counts higher-trust signals. Phase 1 degraded form: explicit Feedback
// only (ExplicitHits). §7's promoted implicit_hits join here in Phase 3.
func evidence(m Memory) float64 {
	return float64(m.ExplicitHits)
}

// survivorScore is the §13.2 composite keep-score; higher survives.
func survivorScore(m Memory) float64 {
	return wScore*m.Score +
		wEvidence*evidence(m) +
		wRecency*recencyFactor(m) +
		wHits*math.Log1p(float64(m.HitCount))
}

// decision is the internal outcome of the §13.2 tiers for one pair.
type decision struct {
	action    string // superseded | flagged
	winnerID  string // empty when no winner could be determined
	loserID   string
	margin    float64
	reason    string
	hasWinner bool
}

// decide applies the three §13.2 tiers to an active pair (a, b) under candidate c.
func decide(a, b Memory, c Contradiction) decision {
	// Layer 1: confidence floor — a weak signal must never delete data.
	if c.Confidence < resolveConfidence {
		return decision{
			action: "flagged",
			reason: fmt.Sprintf("confidence %.2f < %.2f (abstain)", c.Confidence, resolveConfidence),
		}
	}

	aExp := a.ExplicitHits > 0
	bExp := b.ExplicitHits > 0

	// Layer 2: explicit-trumps — exactly one side has explicit feedback. Human
	// feedback overrides a moderately-higher automatic score, but it is worth a
	// bounded edge (one evictMargin), not an unconditional win: a drastically
	// inferior explicit memory must not evict a far-superior one. So if the
	// explicit winner trails the loser by more than evictMargin, abstain (flag).
	if aExp != bExp {
		winner, loser := a, b
		if bExp {
			winner, loser = b, a
		}
		margin := survivorScore(winner) - survivorScore(loser)
		if margin < -evictMargin {
			return decision{
				action:    "flagged",
				winnerID:  winner.ID,
				loserID:   loser.ID,
				margin:    margin,
				reason:    fmt.Sprintf("explicit-trumps abstained: %s has explicit feedback but trails by %.2f > %.2f margin (too inferior to evict)", winner.ID, -margin, evictMargin),
				hasWinner: true,
			}
		}
		return decision{
			action:    "superseded",
			winnerID:  winner.ID,
			loserID:   loser.ID,
			margin:    margin,
			reason:    fmt.Sprintf("explicit-trumps: %s has explicit feedback (%d), %s has none", winner.ID, winner.ExplicitHits, loser.ID),
			hasWinner: true,
		}
	}

	// Layer 3: composite margin guard.
	sa, sb := survivorScore(a), survivorScore(b)
	winner, loser, hi, lo := a, b, sa, sb
	if sb > sa {
		winner, loser, hi, lo = b, a, sb, sa
	}
	margin := hi - lo
	if margin < evictMargin {
		return decision{
			action:    "flagged",
			winnerID:  winner.ID,
			loserID:   loser.ID,
			margin:    margin,
			reason:    fmt.Sprintf("survivorScore margin %.2f < %.2f (too close, abstain)", margin, evictMargin),
			hasWinner: true,
		}
	}
	return decision{
		action:    "superseded",
		winnerID:  winner.ID,
		loserID:   loser.ID,
		margin:    margin,
		reason:    fmt.Sprintf("composite: survivorScore %.2f vs %.2f, margin %.2f ≥ %.2f", hi, lo, margin, evictMargin),
		hasWinner: true,
	}
}

// ResolveContradictions is the single chokepoint both detectors funnel into. It
// dedups candidates by PairKey (keeping the highest-confidence one), applies the
// §13.2 tiers, performs the §13.3 supersede action (unless DryRun or !AutoEvict),
// and appends every decision — abstentions included — to _contradictions.jsonl.
func ResolveContradictions(cands []Contradiction, opts ResolveOptions) ([]ResolutionResult, error) {
	// Dedup by PairKey, keeping the strongest (highest-Confidence) signal.
	byKey := map[string]Contradiction{}
	var order []string
	for _, c := range cands {
		if c.A > c.B {
			c.A, c.B = c.B, c.A
		}
		k := c.PairKey()
		if existing, ok := byKey[k]; ok {
			if c.Confidence > existing.Confidence {
				byKey[k] = c
			}
			continue
		}
		byKey[k] = c
		order = append(order, k)
	}

	active, err := readAllMemories("active")
	if err != nil {
		return nil, err
	}
	idx := map[string]Memory{}
	for _, m := range active {
		idx[m.ID] = m
	}

	today := now().Format(dateLayout)
	supersededThisPass := map[string]bool{}
	var results []ResolutionResult

	for _, k := range order {
		c := byKey[k]
		res := ResolutionResult{Pair: [2]string{c.A, c.B}}

		a, okA := idx[c.A]
		b, okB := idx[c.B]
		if !okA || !okB || supersededThisPass[c.A] || supersededThisPass[c.B] {
			res.Action = "skipped"
			res.Reason = "one or both memories no longer active"
			if !opts.DryRun {
				if err := appendContradictionLog(auditRecord{
					Time: now().Format(time.RFC3339), Pair: res.Pair,
					Action: res.Action, Source: c.Source, Confidence: c.Confidence,
					Reason: res.Reason, Mode: modeOf(opts),
				}); err != nil {
					return nil, err
				}
			}
			results = append(results, res)
			continue
		}

		d := decide(a, b, c)
		finalAction := d.action
		reason := d.reason
		// AutoEvict off → an eviction decision is downgraded to flag-only.
		if finalAction == "superseded" && !opts.AutoEvict {
			finalAction = "flagged"
			reason = d.reason + " (auto-evict disabled → flagged only)"
		}

		res.Action = finalAction
		res.Margin = round2(d.margin)
		res.Reason = reason
		if d.hasWinner {
			res.Winner = d.winnerID
			res.Loser = d.loserID
		}

		if !opts.DryRun {
			switch finalAction {
			case "superseded":
				winner := idx[d.winnerID]
				loser := idx[d.loserID]
				winner.Supersedes = appendUnique(winner.Supersedes, loser.ID)
				winner.ConflictsWith = removeString(winner.ConflictsWith, loser.ID)
				loser.Status = "superseded"
				loser.SupersededBy = winner.ID
				loser.SupersededAt = today
				loser.SupersededReason = reason
				loser.ConflictsWith = nil
				if err := persistSupersede(winner, loser); err != nil {
					return nil, err
				}
				supersededThisPass[loser.ID] = true
				idx[winner.ID] = winner // keep fresh for later pairs touching the winner
				delete(idx, loser.ID)
			case "flagged":
				if err := flagConflict(c.A, c.B, idx); err != nil {
					return nil, err
				}
			}
			if err := appendContradictionLog(auditRecord{
				Time:       now().Format(time.RFC3339),
				Pair:       res.Pair,
				Action:     res.Action,
				Winner:     res.Winner,
				Loser:      res.Loser,
				ScoreA:     round2(survivorScore(a)),
				ScoreB:     round2(survivorScore(b)),
				Source:     c.Source,
				Confidence: c.Confidence,
				Similarity: c.Similarity,
				Margin:     res.Margin,
				Reason:     res.Reason,
				Mode:       modeOf(opts),
			}); err != nil {
				return nil, err
			}
		}

		results = append(results, res)
	}
	return results, nil
}

// persistSupersede moves the loser into archive/{project} and rewrites the winner
// in place. Reuses the Cleanup archive pattern (write new + remove old).
func persistSupersede(winner, loser Memory) error {
	project := loser.Project
	if project == "" {
		project = "unknown"
	}
	archiveDir := filepath.Join(VaultDir, "archive", project)
	oldPath := loser.Path
	if _, err := writeMemory(loser, archiveDir); err != nil {
		return err
	}
	if oldPath != "" && filepath.Dir(oldPath) != archiveDir {
		if err := os.Remove(oldPath); err != nil {
			return fmt.Errorf("remove %s: %w", oldPath, err)
		}
	}
	if _, err := writeMemory(winner, filepath.Dir(winner.Path)); err != nil {
		return err
	}
	return nil
}

// flagConflict records the unresolved pair on both sides (durable pending queue).
func flagConflict(idA, idB string, idx map[string]Memory) error {
	for _, pair := range [][2]string{{idA, idB}, {idB, idA}} {
		m, ok := idx[pair[0]]
		if !ok {
			continue
		}
		m.ConflictsWith = appendUnique(m.ConflictsWith, pair[1])
		if _, err := writeMemory(m, filepath.Dir(m.Path)); err != nil {
			return err
		}
		idx[pair[0]] = m
	}
	return nil
}

// Unsupersede restores a superseded loser to active, clears its supersede fields,
// removes the winner's supersedes entry, and appends a reversal record.
func Unsupersede(id string) (Memory, error) {
	all, err := readAllMemories("superseded")
	if err != nil {
		return Memory{}, err
	}
	var loser Memory
	found := false
	for _, m := range all {
		if m.ID == id {
			loser = m
			found = true
			break
		}
	}
	if !found {
		return Memory{}, fmt.Errorf("superseded memory %q not found", id)
	}

	winnerID := loser.SupersededBy

	// Restore the loser: status → active, clear supersede metadata, move back to
	// notes/{project} (out of archive).
	loser.Status = "active"
	loser.SupersededBy = ""
	loser.Supersedes = nil
	loser.SupersededAt = ""
	loser.SupersededReason = ""
	project := loser.Project
	if project == "" {
		project = "unknown"
	}
	notesDir := filepath.Join(VaultDir, "notes", project)
	oldPath := loser.Path
	if _, err := writeMemory(loser, notesDir); err != nil {
		return Memory{}, err
	}
	if oldPath != "" && filepath.Dir(oldPath) != notesDir {
		if err := os.Remove(oldPath); err != nil {
			return Memory{}, fmt.Errorf("remove %s: %w", oldPath, err)
		}
	}

	// Remove the loser from the winner's supersedes list, if the winner is around.
	if winnerID != "" {
		actives, err := readAllMemories("active")
		if err != nil {
			return Memory{}, err
		}
		for _, w := range actives {
			if w.ID == winnerID {
				w.Supersedes = removeString(w.Supersedes, id)
				if _, err := writeMemory(w, filepath.Dir(w.Path)); err != nil {
					return Memory{}, err
				}
				break
			}
		}
	}

	if err := appendContradictionLog(auditRecord{
		Time:   now().Format(time.RFC3339),
		Pair:   [2]string{winnerID, id},
		Action: "unsuperseded",
		Winner: winnerID,
		Loser:  id,
		Reason: "manual reversal",
		Mode:   "manual",
	}); err != nil {
		return Memory{}, err
	}
	return loser, nil
}

// lexicalContradictionScan is the default at-rest detector: two active memories
// with high char-bigram overlap (same subject) but differing negation polarity
// are flagged as a candidate contradiction. Confidence is a conservative lexical
// heuristic that mostly sits below resolveConfidence, so a lexical signal alone
// rarely evicts.
//
// Memories are partitioned by Project before pairing: a contradiction only makes
// sense between notes about the same thing, so project-A's "use pooling" and
// project-B's "don't use pooling" must never be matched. Partitioning is both a
// correctness fix (kills cross-project false positives) and a cost cut — O(n²)
// becomes Σ O(nᵢ²). Unclassified memories (empty Project) form their own "" group
// and still compare against each other.
func lexicalContradictionScan(memories []Memory) []Contradiction {
	byProject := map[string][]Memory{}
	for _, m := range memories {
		if m.Status == "active" {
			byProject[m.Project] = append(byProject[m.Project], m)
		}
	}
	// Iterate projects in a stable order so the candidate stream (and thus the
	// audit log) is reproducible across runs.
	projects := make([]string, 0, len(byProject))
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)

	var out []Contradiction
	for _, p := range projects {
		out = append(out, scanGroup(byProject[p])...)
	}
	return out
}

// memWithBigrams caches a memory's char-bigram set so each body is tokenised once
// per scan rather than once per pair — turning the O(n²) inner loop's O(n²) set
// builds (and their allocations) into O(n).
type memWithBigrams struct {
	m     Memory
	grams map[string]bool
}

// scanGroup runs the pairwise polarity-flip check within one already-partitioned
// group (callers guarantee every member shares a Project). Bigram sets are
// precomputed once per memory and reused via jaccardOfSets.
func scanGroup(group []Memory) []Contradiction {
	if len(group) < 2 {
		return nil
	}
	pre := make([]memWithBigrams, len(group))
	for i, m := range group {
		pre[i] = memWithBigrams{m: m, grams: charBigrams(m.Body)}
	}

	var out []Contradiction
	for i := 0; i < len(pre); i++ {
		for j := i + 1; j < len(pre); j++ {
			a, b := pre[i], pre[j]
			sim := jaccardOfSets(a.grams, b.grams)
			if sim < lexicalSimThreshold {
				continue
			}
			na := hasNegation(a.m.Body)
			nb := hasNegation(b.m.Body)
			if na == nb {
				continue // same polarity → similar, not contradictory
			}
			ca, cb := a.m.ID, b.m.ID
			if ca > cb {
				ca, cb = cb, ca
			}
			out = append(out, Contradiction{
				A:          ca,
				B:          cb,
				Similarity: round2(sim),
				Confidence: round2(lexicalConfidence(sim)),
				Source:     "lexical",
				Detail:     fmt.Sprintf("negation polarity differs at sim=%.2f", sim),
			})
		}
	}
	return out
}

// negationMarkers are CJK + English tokens that flip the polarity of a statement.
var negationMarkers = []string{
	"不", "没", "未", "无", "别", "勿", "非", "禁用", "弃用", "废弃", "取消", "停用",
	"no", "not", "never", "n't", "without", "disable", "deprecated", "avoid",
}

func hasNegation(s string) bool {
	low := strings.ToLower(s)
	for _, mk := range negationMarkers {
		if strings.Contains(low, mk) {
			return true
		}
	}
	return false
}

// lexicalConfidence maps bigram similarity to a conservative contradiction
// confidence: only very-high-overlap polarity flips approach the floor.
func lexicalConfidence(sim float64) float64 {
	conf := 0.3 + 0.4*sim
	if conf > 1 {
		conf = 1
	}
	return conf
}

func modeOf(opts ResolveOptions) string {
	if opts.AutoEvict {
		return "auto"
	}
	return "manual"
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func removeString(s []string, v string) []string {
	var out []string
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// auditRecord is one line in _contradictions.jsonl.
type auditRecord struct {
	Time       string    `json:"time"`
	Pair       [2]string `json:"pair"`
	Action     string    `json:"action"` // superseded | flagged | skipped | unsuperseded
	Winner     string    `json:"winner,omitempty"`
	Loser      string    `json:"loser,omitempty"`
	ScoreA     float64   `json:"score_a,omitempty"`
	ScoreB     float64   `json:"score_b,omitempty"`
	Source     string    `json:"source,omitempty"`
	Confidence float64   `json:"confidence,omitempty"`
	Similarity float64   `json:"similarity,omitempty"`
	Margin     float64   `json:"margin,omitempty"`
	Reason     string    `json:"reason"`
	Mode       string    `json:"mode"` // auto | manual
}

func appendContradictionLog(rec auditRecord) error {
	if err := os.MkdirAll(VaultDir, 0o755); err != nil {
		return fmt.Errorf("mkdir vault: %w", err)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal audit record: %w", err)
	}
	path := filepath.Join(VaultDir, contradictionsLog)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	return nil
}
