# Implicit Feedback Scoring — Design

Status: **DRAFT, awaiting review** (no code written yet per task gate)
Scope: `server/memory/` only. Explicit `Feedback` is preserved untouched as a fallback.

## 1. Problem

`Recall` returns a ranked list of memories, but the only way a memory's weight
grows is an explicit `Feedback(id, used, helpful)` call. In practice callers
forget to send it, so genuinely useful memories never gain weight and slowly
lose ranking against noise.

We want to **infer** usefulness from the conversation itself: after a turn,
look at the assistant's reply and decide which recalled memories were actually
*used*, then nudge their weight — without ever lowering the bar for false
positives (a memory the model parroted but that was wrong must not gain weight).

## 2. Non-goals

- Not replacing explicit `Feedback` — it stays as a stronger, higher-trust signal.
- Not adding an LLM dependency to the `memory` package. Detection is a pluggable
  function; a real semantic judge can be injected by the caller (orchestrator),
  but the package ships a deterministic lexical default so it stays testable and
  self-contained.
- Not touching `Ingest` / `Recall` / `Cleanup` scoring math.

## 3. Data flow

```
turn ends
  └─ orchestrator has: recalledIDs []string  (what Recall returned this turn)
                       conversationLog string (the assistant's reply text for this
                                               turn — the generated response, NOT the
                                               user prompt and NOT the raw memory text)
        │
        ▼
  ImplicitFeedback(recalledIDs, conversationLog)
        │  for each id:
        │   1. load memory from notes/ or inbox/ (found?)
        │   2. ref := ReferenceDetector(mem.Body, conversationLog)   // clamped 0..1
        │   3. contradicted := ContainsContradiction(mem.Body, conversationLog)
        │       // scoped per memory — see §7
        │   4. classify outcome → apply weight change (with probation)
        │   5. persist
        ▼
  []ImplicitFeedbackResult   (one per id, for logging / CLI output)
```

The package does **not** capture `recalledIDs` itself — `Recall` stays a pure
read. The caller threads the IDs from its `Recall` call into `ImplicitFeedback`.
This keeps `Recall` side-effect-free and matches the existing layering.

## 4. Outcome classification

For each recalled memory, given `ref` (reference strength 0..1) and
`contradicted` (bool):

| condition | outcome | hit_count | score effect |
| --- | --- | --- | --- |
| `ref < refThreshold` | `ignored` | unchanged | none — recalled but not used |
| `ref ≥ refThreshold` and **not** contradicted | `confirmed` | `++` | enters probation (see §5) |
| `ref ≥ refThreshold` and contradicted | `contradicted` | `++` | discard pending; small penalty |

Rationale:
- A memory that was *referenced* is a real usage event regardless of correctness,
  so `hit_count++` fires for both `confirmed` and `contradicted` (hit_count is a
  usage counter, not a quality counter). Only `ignored` leaves everything alone.
- A *contradicted* reference means the model leaned on the memory and was then
  corrected → that memory is suspect, so we never raise its score and apply a
  small penalty.

## 5. The probation / "observation period" — avoiding false positives

The risk the task calls out: the model confidently follows a memory that is
actually wrong, no correction appears *this* turn, so we'd wrongly +score it.

Mechanism: **a confirmed implicit boost is staged as `pending_score`, not added
to `score`. It is promoted into `score` only when the memory survives at least
one more `ImplicitFeedback` turn without being contradicted.**

Per-memory state, on a `confirmed` outcome:
1. **Promote** any existing `pending_score` into `score` first — it survived
   from a previous turn to this one without contradiction → observation passed.
2. Stage **this** turn's boost as the new `pending_score`.
3. `implicit_hits++`, `hit_count++`, update `last_implicit`.

On a `contradicted` outcome:
1. **Discard** `pending_score` (set to 0) — the unconfirmed boost is thrown away.
2. Apply `contradictPenalty` to `score` (floored at 0).
3. `hit_count++`, update `last_implicit`. (`implicit_hits` not incremented.)

On `ignored`: no state change at all.

Net effect: a boost takes hold only after the memory has been *usefully
referenced in two distinct turns without correction*. A single parroted-but-wrong
reference never reaches `score`; if the next turn corrects it, the staged boost
is dropped.

### Optional sweep (documented, off by default in v1)
A memory confirmed once then never seen again keeps its `pending_score` forever
unpromoted (conservative — fine). A future enhancement could promote
`pending_score` during `Cleanup` once `last_implicit` is older than an
observation window; left out of v1 to keep the first cut small.

## 6. Reference detection (default heuristic)

`ReferenceDetector(memoryBody, conversationLog string) float64`, package var so
it can be overridden (same indirection style as `var now = time.Now`).

Default = **character-level bigram Jaccard**. Word tokenisation with `\w+` is
deliberately avoided: it splits poorly on CJK (no spaces) and discards the
sub-word overlap that catches paraphrase. Instead both texts are lowercased,
whitespace-collapsed, and cut into overlapping character bigrams:

```
memBigrams   = charBigrams(normalize(memoryBody))     // set of 2-char shingles
replyBigrams = charBigrams(normalize(conversationLog))
ref          = |memBigrams ∩ replyBigrams| / |memBigrams ∪ replyBigrams|   // Jaccard, 0..1
```

`normalize` lowercases and collapses runs of whitespace to a single space;
bigrams are taken over the resulting rune sequence (rune-based, so multibyte
CJK characters form proper 2-rune shingles).

**Short-memory fallback (Jaccard fallback).** Jaccard punishes very short
memories: a 2-character memory yields a single bigram, so any miss drops `ref`
to 0 and any hit spikes it to a value the union with a long reply still crushes.
When the memory produces **fewer than 3 bigrams** (`len(memBigrams) < 3`), the
detector instead returns a **length-ratio containment** score:

```
ref = (count of memBigrams present in replyBigrams) / len(memBigrams)
```

i.e. "what fraction of the tiny memory's shingles appear in the reply at all",
ignoring the reply's length. This keeps short, distinctive memories detectable
without letting a long reply dilute them. (A memory with 0 bigrams — empty/1-char
body — returns 0.)

`refThreshold` default `0.5`. The `minOverlapTokens` guard from the token design
is dropped; the `< 3 bigrams` fallback subsumes the "too short to trust" case.

**Bounds.** Whatever detector is in use (default or an injected LLM judge), the
returned value is **clamped to `[0, 1]`** by `ImplicitFeedback` before it is
compared against `refThreshold` or recorded — a misbehaving custom detector
returning `1.7` or `-0.2` cannot corrupt classification or the persisted
`reference_score`.

Limitations (documented, not hidden):
- Lexical, not truly semantic — paraphrase with no shared characters is missed,
  and topical overlap can cause a false `confirmed`. The probation period is the
  backstop against the costly direction of that error.
- Callers wanting real semantics inject an LLM-backed `ReferenceDetector` (still
  clamped to `[0,1]` as above).

## 7. Contradiction detection (default heuristic)

`ContainsContradiction(memoryBody, conversationLog string) bool`, also a package
var. It takes the memory body so detection is **scoped per memory**, not global.

**Per-memory scoping.** A correction marker firing somewhere in a long reply does
not mean *this* memory was the thing corrected. So the default:
1. Splits `conversationLog` into sentences (on `。．.!?！？\n`).
2. Keeps only sentences that *reference this memory* — a sentence whose
   character-bigram overlap with `memoryBody` clears a small floor (reuses the
   §6 bigram machinery; a sentence with no shared bigrams is irrelevant to this
   memory and ignored).
3. Returns `true` only if a **correction marker appears in one of those
   memory-referencing sentences**. A marker in an unrelated sentence is ignored.

This means two memories recalled in the same turn can get *different*
`contradicted` verdicts from the same log — the one the correction was about is
penalised, the unrelated one is not.

Correction markers — **explicit negation/error words only** (case-insensitive).
Soft hedges that don't actually signal "the memory was wrong" (`其实`, `并不`,
`应该`, `只是`, and the English `actually`) are removed, since they routinely
appear in correct, non-corrective replies and caused false `contradicted`:

```
zh: 不对 不是 错了 纠正 搞错
en: wrong, incorrect, "not correct", "not right", "my mistake",
    "that's not", "isn't right"
```

Limitation: sentence scoping is still lexical — a correction in a sentence that
references the memory only by pronoun ("that's wrong") right after the referencing
sentence may be missed. Conservative by design (biases toward *not* penalising,
i.e. toward *not* boosting via the probation path). Cross-sentence coreference is
future work.

## 8. Function signatures

### Go (`memory.go`)

```go
// Tunables (package consts)
const (
    implicitRefThreshold = 0.5
    implicitBoost        = 0.1  // matches explicit helpful(+0.1); used(+0.05) is weaker
    contradictPenalty    = 0.10
)

// Injectable hooks (default to the heuristics above; tests/LLM callers override).
// ReferenceDetector's return value is clamped to [0,1] by ImplicitFeedback (§6).
var ReferenceDetector = bigramReference          // char-bigram Jaccard + short fallback
var ContainsContradiction = scopedContradiction  // per-memory scoped (§7)

type ImplicitFeedbackResult struct {
    ID         string  `json:"id"`
    Found      bool    `json:"found"`            // false → no such memory file; everything below is zero
    Outcome    string  `json:"outcome"`          // confirmed | contradicted | ignored
    Referenced bool    `json:"referenced"`
    Reference  float64 `json:"reference_score"`  // clamped to [0,1]
    HitCount   int     `json:"hit_count"`
    Score      float64 `json:"score"`
    Pending    float64 `json:"pending_score"`
}

// ImplicitFeedback analyses one turn: for each recalled memory id it detects
// whether the conversation actually used that memory and adjusts weight with a
// one-turn observation period. Unknown ids are skipped (reported Found=false,
// Outcome="ignored", Referenced=false) rather than erroring, so a stale id in the
// batch is harmless and distinguishable from a real-but-ignored memory (Found=true).
func ImplicitFeedback(memoryIDs []string, conversationLog string) ([]ImplicitFeedbackResult, error)
```

New `Memory` frontmatter fields (backward compatible — absent in old files → zero):

```go
ImplicitHits int     `yaml:"implicit_hits"`
PendingScore float64 `yaml:"pending_score"`
LastImplicit string  `yaml:"last_implicit"`
```

### Python (`memory_api.py`)

```python
def implicit_feedback(memory_ids: list[str], conversation_log: str) -> list[dict]:
    """Mirror of the Go ImplicitFeedback: same thresholds, probation, fields."""
```
Same constants (`implicit_boost = 0.1`), same `reference_detector(memory_body,
conversation_log)` / `contains_contradiction(memory_body, conversation_log)`
module-level callables — both taking the memory body so the char-bigram Jaccard
(+ short-memory fallback) and the per-memory contradiction scoping behave
identically across the two ports. Each result dict includes `found`.

## 9. CLI

New subcommand in `cmd/memory-cli/main.go` and `memory_cli.py`:

```bash
memory-cli implicit <id[,id2,...]> --log "<conversation text>"
memory-cli implicit <id> --log @reply.txt      # @file reads from disk
echo "<reply>" | memory-cli implicit <id> --log -   # - reads stdin
```

Prints one line per id: `id=… found=… outcome=… ref=… hit_count=… score=… pending=…`.
Useful for manual testing and for the orchestrator to shell out per turn.

## 10. Tests (`memory_test.go`)

Core logic, all on a temp vault + pinned clock + overridden detectors for
determinism:

1. **confirmed across two turns promotes pending** — turn 1 confirm stages
   pending (score unchanged); turn 2 confirm promotes turn 1's boost into score.
2. **single confirm does not change score** — only `pending_score`/`implicit_hits`/`hit_count` move.
3. **contradiction discards pending + penalises** — after a staged pending, a
   contradicted turn zeroes pending, lowers score, no `implicit_hits++`.
4. **ignored leaves memory untouched** — `ref` below threshold → no field changes.
5. **detector override is honoured** — inject a stub `ReferenceDetector`.
6. **backward compat** — a memory file written without the new fields parses and
   updates correctly (fields default to zero).
7. **batch + unknown id** — multiple ids in one call; an unknown id yields a
   `Found=false`, `ignored`/not-referenced result without erroring the batch, and
   a real-but-ignored memory in the same batch reports `Found=true`.
8. **explicit Feedback still works unchanged** — regression guard.
9. **detector bounds clamp** — an injected `ReferenceDetector` returning `1.7`
   and one returning `-0.2` are clamped to `1.0`/`0.0` in both classification and
   the recorded `reference_score`.
10. **per-memory contradiction scoping** — two memories recalled in one turn; a
    correction marker in a sentence referencing only memory A yields
    `contradicted` for A and `confirmed`/`ignored` for B.

Lexical helpers (`bigramReference`, `scopedContradiction`) get small direct table
tests: char-bigram Jaccard values, the `<3 bigrams` length-ratio fallback,
0-bigram → 0, and that the removed soft markers (`其实`/`actually`/…) no longer
trigger a contradiction while the retained explicit ones still do.

## 11. Files touched

| File | Change |
| --- | --- |
| `memory.go` | + `ImplicitFeedback`, helpers, 3 frontmatter fields, consts/hooks; + §13 `ResolveContradictions`/`survivorScore`/`Unsupersede`, 5 conflict fields, `superseded` status, `Cleanup` resolution phase |
| `memory_api.py` | + `implicit_feedback` parity; + §13 `resolve_contradictions`/`survivor_score`/`unsupersede` parity |
| `cmd/memory-cli/main.go` | + `implicit` subcommand; + `conflicts`/`resolve`/`unsupersede` subcommands |
| `memory_cli.py` | + `implicit` subcommand (parity); + conflict subcommands (parity) |
| `memory_test.go` | + tests above; + resolution tier/supersede/undo/audit-log tests |
| `MEMORY.md` | document the mechanism, new fields, CLI, limitations; + §13 resolution + audit log |

No changes outside `server/memory/`. No commits/pushes until reviewed and approved.

## 12. Open questions for review

1. **Boost magnitude / penalty** — `implicitBoost=0.1` (= explicit helpful),
   `contradictPenalty=0.10`. The probation period (§5) is what justifies a boost
   this large: it only lands after two clean turns. Too aggressive given that?
2. **Probation length** — v1 = "survive 1 additional turn". Want N>1 turns
   (stronger gate, needs a small turn/age counter) instead?
3. **Score floor** — penalty floors at 0. Should a memory ever be auto-archived
   when implicit feedback pushes it below some floor, or stay put for `Cleanup`?
4. **`refThreshold=0.5` on bigram Jaccard** — Jaccard over char bigrams runs
   lower than token coverage did (the union includes the whole reply), so 0.5 may
   be too strict for a short memory quoted inside a long reply. Calibrate against
   real logs, or switch the long-reply case to containment (∩/memBigrams) like the
   short-memory fallback already does?

## 13. Contradiction resolution (矛盾淘汰机制)

### 13.0 What this section adds

Two detectors find contradictions but **neither acts on them**:

- **Approach B (existing)** — `holographic/retrieval.py :: contradict()` uses HRR
  vectors + entity-Jaccard to surface contradictory *pairs* at rest. Detect-only.
- **Approach A (this design)** — `ImplicitFeedback`'s per-turn / same-recall-set
  contradiction marking. Detect-only (it sets `contradicted` / a `conflicts_with`
  marker, never evicts).

§13 is the **single resolution layer both funnel into**: given contradiction
candidates, decide *which memory survives*, *what happens to the loser*, *when*,
and *how it stays auditable & reversible*. It is deliberately **separate from
detection** so the two detectors (and a future LLM judge) share one eviction
policy and one audit trail, and so eviction never runs on the hot path.

> **Scope note vs §2.** This intentionally extends beyond §2's "not touching
> Cleanup": it adds a *new resolution phase* alongside `Cleanup`, but does **not**
> modify `Cleanup`'s existing archival/scoring math. The §5 probation machinery
> is **read** as an evidence input and is never mutated by resolution.

### 13.1 Normalised input — `Contradiction`

Both detectors are adapted into one struct so the resolver is detector-agnostic.
`contradict()`'s `(id_a, id_b, hrr_cosine, entity_jaccard)` output maps directly;
Approach A emits the same struct with `Source="implicit"`.

```go
// Contradiction is one candidate conflicting pair, detector-agnostic.
type Contradiction struct {
    A          string  `json:"a"`          // canonicalised so A < B by id (stable pair key)
    B          string  `json:"b"`
    Similarity float64 `json:"similarity"` // same-subject overlap (HRR cosine / entity Jaccard), 0..1
    Confidence float64 `json:"confidence"` // detector's belief the pair is *contradictory* (not just similar), 0..1
    Source     string  `json:"source"`     // "holographic" | "implicit" | "manual"
    Detail     string  `json:"detail"`     // which entities/slots differ (for the audit log)
}

// PairKey returns the order-independent identity of the pair.
func (c Contradiction) PairKey() string // = c.A + "|" + c.B, A<B already enforced
```

### 13.2 Eviction strategy — who survives (combination, evidence-first)

A **composite survivor score** decides the winner; higher survives. Pure recency
or pure score alone each misfire (recency lets an unproven new note kill a
long-confirmed one; score alone ignores preference *change*), so we combine, with
evidence weighted highest and recency tempered by a half-life:

```
survivorScore(m) =
      wScore    * m.Score
    + wEvidence * evidence(m)        // explicit Feedback + promoted implicit_hits
    + wRecency  * recencyFactor(m)   // 2^(-ageDays/recencyHalfLife)  ∈ (0,1]
    + wHits     * log1p(m.HitCount)  // usage, low weight (already correlated w/ score)
```

`evidence(m)` counts higher-trust signals: explicit `Feedback` (used/helpful)
dominates, then `implicit_hits` that have **passed probation** (a still-`pending`
boost contributes little — an unproven memory cannot supersede a confirmed one).

The decision is **tiered** for determinism and to refuse low-confidence evictions:

1. **Abstain tier** — if `Confidence < resolveConfidence` → **do not evict**; mark
   both `conflicts_with` and log a `flagged` decision. (Honours "never lower the
   bar for false positives" — a weak contradiction signal must not delete data.)
2. **Explicit-trumps tier** — if exactly one side has explicit `Feedback` evidence
   and the other has none → that side wins outright (explicit > implicit, per §2).
3. **Composite tier** — else winner = higher `survivorScore`, but **only if the
   margin ≥ evictMargin**. If the two are within `evictMargin` (too close to call)
   → abstain & flag, same as tier 1.

This answers "score vs time vs frequency vs evidence": it's a weighted blend
(evidence-first, recency-tempered, frequency-light) gated by a confidence floor
and a margin guard, with an explicit-feedback override on top.

### 13.3 Eviction action — supersede (soft), never hard-delete

Reuse the existing archive machinery (`Cleanup` already moves files to
`archive/{project}` and flips `status`). The loser is **superseded**, not deleted:

- **Loser** → `status: "superseded"` (new value alongside `active`/`archived`),
  moved to `archive/{project}`, with `superseded_by`, `superseded_at`,
  `superseded_reason` set. File + all fields preserved → fully auditable & reversible.
- **Winner** → gains `supersedes: [loserID]`; **its score/probation fields are not
  touched** (no reward for winning — avoids gaming detection into a boost path).
- **Recall is unchanged**: it already filters `status=="active"`, so a superseded
  memory drops out of recall automatically — no new Recall logic, no §2 violation.

`superseded` is kept distinct from `archived` (TTL expiry) so the audit trail
records *why* a memory left active circulation.

Hard delete is never the default; an operator-only `--purge` on the CLI can later
remove superseded files, but the design ships with soft-delete only.

### 13.4 Trigger timing — detect anytime, resolve in batch

Hybrid, mapping each detector to where it naturally lives:

| stage | who | what it does |
| --- | --- | --- |
| online (per turn) | Approach A in `ImplicitFeedback` | **mark only**: set `conflicts_with`, emit a `Contradiction{Source:"implicit"}` into a pending store. Never evicts (respects probation; avoids single-turn thrash). |
| at rest (batch) | `ContradictionDetector` over the vault | produce `[]Contradiction` for the resolver. |
| resolution (batch) | `ResolveContradictions`, run inside `Cleanup()` | the **only** place eviction happens: dedup pairs by `PairKey`, apply §13.2 tiers, supersede losers, write audit log. |

Lazy resolution (resolve on next recall) is **rejected** as primary — a wrong
memory could be recalled before the sweep. As an interim mitigation, Recall *may*
optionally down-rank memories carrying an unresolved `conflicts_with` marker
(documented option, off by default).

### 13.5 Auditability & reversibility

- **Self-describing frontmatter** on both files (`superseded_by` / `supersedes` /
  `superseded_at` / `superseded_reason`).
- **Append-only resolution log**: `vault/_contradictions.jsonl`, one record per
  decision — `pair`, `action` (`superseded|flagged|skipped`), `winner`, `loser`,
  both `survivorScore`s, the deciding factors, `source`, `confidence`, `margin`,
  `auto|manual`, timestamp. **Abstentions are logged too**, so missed evictions
  are auditable, not silent.
- **User controls**: `memory-cli conflicts` lists open conflicts + recent
  decisions; `memory-cli unsupersede <id>` restores a loser (`status`→`active`,
  clears `superseded_*`, removes the winner's `supersedes` entry, appends a
  reversal record).
- **Dry-run**: `ResolveOptions.DryRun` reports decisions without writing — required
  for first rollout.

### 13.6 Data-structure changes (frontmatter, backward compatible)

All new fields are `omitempty`; absent in old files → zero, so existing memories
parse unchanged.

```go
// added to Memory
ConflictsWith    []string `yaml:"conflicts_with,omitempty"`   // unresolved conflict partners
SupersededBy     string   `yaml:"superseded_by,omitempty"`    // winner id (set on loser)
Supersedes       []string `yaml:"supersedes,omitempty"`       // loser ids (set on winner)
SupersededAt     string   `yaml:"superseded_at,omitempty"`    // date (dateLayout)
SupersededReason string   `yaml:"superseded_reason,omitempty"`
```

`Status` gains a third value `"superseded"` (alongside `active`/`archived`).
`Stats()` counts it separately. `Recall`'s default `status=="active"` filter
already excludes it.

### 13.7 Function signatures

```go
// Tunables (package consts)
const (
    resolveConfidence = 0.6  // min detector Confidence to even consider eviction
    evictMargin       = 1.0  // min survivorScore gap (score units) to name a winner
    recencyHalfLife   = 90   // days; recency weight halves every 90 days
)
const ( wScore = 1.0; wEvidence = 0.5; wRecency = 1.0; wHits = 0.1 )

// Injectable at-rest detector (default lexical/HRR scan; LLM/holographic override),
// mirroring the var ReferenceDetector indirection in §6.
var ContradictionDetector func(memories []Memory) []Contradiction = lexicalContradictionScan

type ResolveOptions struct {
    DryRun    bool // compute + log decisions, write nothing
    AutoEvict bool // false = only flag conflicts_with, never supersede
}

type ResolutionResult struct {
    Pair   [2]string `json:"pair"`
    Action string    `json:"action"` // superseded | flagged | skipped
    Winner string    `json:"winner,omitempty"`
    Loser  string    `json:"loser,omitempty"`
    Margin float64   `json:"margin"`
    Reason string    `json:"reason"`
}

// ResolveContradictions is the single chokepoint both detectors funnel into.
// It dedups candidates by PairKey, applies the §13.2 tiers, performs the §13.3
// supersede action (unless DryRun/!AutoEvict), and appends to _contradictions.jsonl.
func ResolveContradictions(cands []Contradiction, opts ResolveOptions) ([]ResolutionResult, error)

// survivorScore is the §13.2 composite keep-score; higher survives.
func survivorScore(m Memory) float64

// Unsupersede restores a superseded loser to active and logs the reversal.
func Unsupersede(id string) (Memory, error)

// Cleanup gains a resolution phase (existing archival math untouched).
type CleanupResult struct {
    Archived int `json:"archived"`
    Resolved int `json:"resolved"` // contradictions superseded this pass
}
```

Python parity (`memory_api.py`): `resolve_contradictions(cands, dry_run=False,
auto_evict=True) -> list[dict]`, `survivor_score(mem) -> float`,
`unsupersede(id) -> dict`, module-level `contradiction_detector` callable — same
constants and tiers so behaviour matches across ports.

CLI (`cmd/memory-cli/main.go` + `memory_cli.py`):

```bash
memory-cli conflicts                      # list open conflicts + recent resolutions
memory-cli resolve [--dry-run] [--no-evict]
memory-cli unsupersede <id>
```

### 13.8 Probation safety (does not break §5)

- Online `ImplicitFeedback` **only marks** — it never calls the resolver, so the
  §5 promote/discard logic is untouched.
- Resolution **reads** `implicit_hits`/`pending_score` as evidence but never
  writes them; a still-`pending` (unproven) boost counts for little, so a memory
  in probation cannot supersede a confirmed one on evidence alone.
- The loser's fields are frozen in archive, so `Unsupersede` restores its exact
  pre-eviction probation state.

### 13.9 Open questions

1. **Weights / `evictMargin`** — `wEvidence=0.5`, `evictMargin=1.0` are first
   guesses; calibrate against real conflict pairs.
2. **3+ way conflicts** — current model resolves pairs; a conflict *cluster*
   (A↔B↔C) is handled as pairwise rounds. Need a cluster-winner pass instead?
3. **Auto-supersede on strong recency** — should a much-newer, explicitly-confirmed
   memory supersede regardless of the older one's score (preference change), or
   always go through the margin guard?
