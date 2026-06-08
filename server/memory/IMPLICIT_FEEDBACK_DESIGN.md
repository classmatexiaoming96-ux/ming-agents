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
| `memory.go` | + `ImplicitFeedback`, helpers, 3 frontmatter fields, consts/hooks |
| `memory_api.py` | + `implicit_feedback` parity |
| `cmd/memory-cli/main.go` | + `implicit` subcommand |
| `memory_cli.py` | + `implicit` subcommand (parity) |
| `memory_test.go` | + tests above |
| `MEMORY.md` | document the mechanism, new fields, CLI, limitations |

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
```
