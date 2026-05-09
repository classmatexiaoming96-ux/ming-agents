"""
Orchestrator graph (PM + PLAN + DEV + REV).

Stages wired:
  INIT → PM_PHASE → GATE_1 → PLANNING_PHASE → GATE_1_5 → DEV_PHASE
       → GATE_2 → REV_PHASE → GATE_3 → DONE / BLOCKED

Each phase node (and each per-subtask DEV node, and each REV sub-node)
pauses with a `type=subagent_dispatch` dynamic interrupt; each gate pauses
with `type=gate_decision_needed`. The runner outside the graph does the
real work and resumes with a stage-result/v1 dict.

DEV_PHASE iterates over planner-supplied subtasks: each subtask gets one
dev_agent dispatch then one codex_reviewer dispatch (read-only). Codex
review failures are recorded but do NOT abort DEV; they aggregate into
Gate 2's gate_result.

REV_PHASE is two dispatches in series: rev_agent (writes rev_report +
acceptance_checklist under artifacts/rev/) then codex_final (read-only
end-to-end review whose gate_result becomes the Gate 3 signal).

Gate options:
  - GATE_1:   approve / rollback_to_pm / reject
  - GATE_1_5: approve / rollback_to_pm / reject
  - GATE_2:   approve / rollback_to_planning / rollback_to_pm / reject
  - GATE_3:   approve / rollback_to_dev / rollback_to_planning
              / rollback_to_pm / reject
              (rollback_to_dev clears subtask_results and reruns DEV from scratch)

No LLM calls anywhere — all LLM work happens outside the graph and is fed
back via `resume --decision-file`.
"""

from __future__ import annotations

import hashlib
from pathlib import Path

from langgraph.graph import StateGraph, START, END
from langgraph.types import interrupt

from state import (
    ArtifactRef,
    ConsultationRecord,
    Decision,
    GateResult,
    GuardRetryPending,
    GuardWarning,
    MsgRef,
    PhaseSummary,
    PolicyConfig,
    Stage,
    StageRecord,
    SubtaskResult,
    SubtaskSpec,
    WorkflowState,
    now_iso,
)


_VALID_STATUSES = ("done", "blocked", "needs_input")
MAX_PLANNING_CONSULTATIONS = 3
GUARD_RETRY_LIMIT = 1
# Hard cap on a user-supplied hint file (continue_planning).  Real planning
# hints are short prose; anything bigger is a misclick (wrong path) or an
# attempt to wedge orch (e.g. /dev/zero, /proc/self/environ, multi-MB pdf).
# We stat() before reading and refuse over-cap files with a severe warning,
# rather than streaming bytes blindly into memory + sha + state.
MAX_USER_HINT_BYTES = 64 * 1024


def _sha256(s: str) -> str:
    return hashlib.sha256(s.encode("utf-8")).hexdigest()


def _append_log(state: WorkflowState, stage: Stage, note: str) -> list[StageRecord]:
    return state.stage_log + [
        StageRecord(
            stage=stage,
            entered_at=now_iso(),
            exited_at=now_iso(),
            seen_msg_ids=[m.msg_id for m in state.user_msgs],
            seen_decision_ids=[d.decision_id for d in state.decisions],
            note=note,
        )
    ]


def _default_artifact_paths(workflow_id: str, base_dir: str = "") -> dict[str, str]:
    """Map artifact key → absolute path (when base_dir is provided, e.g. by cli
    at start). When base_dir is empty, falls back to the legacy relative form
    `shrimp/workflows/{wid}/...` — useful for in-process tests that don't care
    about real filesystem persistence."""
    base = base_dir if base_dir else f"shrimp/workflows/{workflow_id}"
    return {
        "raw_requirement": f"{base}/raw_requirement.md",
        "pm_spec": f"{base}/artifacts/pm/spec.md",
        "pm_risks": f"{base}/artifacts/pm/risks.md",
        "plan": f"{base}/artifacts/plan/plan.md",
        "task_list": f"{base}/artifacts/plan/task_list.md",
        "planning_discussion": f"{base}/artifacts/plan/discussion.md",
        "dev_summary": f"{base}/artifacts/dev/summary.md",
        "dev_self_check": f"{base}/artifacts/dev/self_check.md",
        "dev_changed_files": f"{base}/artifacts/dev/changed_files.txt",
        "rev_report": f"{base}/artifacts/rev/report.md",
        "acceptance_checklist": f"{base}/artifacts/rev/acceptance_checklist.md",
    }


def _default_policies(workflow_id: str, base_dir: str = "") -> dict[str, PolicyConfig]:
    """
    Built-in per-stage permission policies. Goal: keep `ask_user` empty
    everywhere so cc-lead never hangs waiting for approval; whatever the
    workflow shouldn't allow goes into `deny` and gets silently blocked.

    Patterns are intentionally written in a tool-agnostic shape; the runner
    translates to cc-lead's settings.json (or equivalent) at spawn time.

    base_dir, when provided, makes the Write/Edit patterns absolute so the
    runner does not need to guess where artifacts live.
    """
    base = base_dir if base_dir else f"shrimp/workflows/{workflow_id}"
    return {
        Stage.PM_PHASE.value: PolicyConfig(
            allow_silent=[
                "Read(**)",
                "Glob(**)",
                "Grep(**)",
                f"Write({base}/artifacts/pm/**)",
                f"Edit({base}/artifacts/pm/**)",
            ],
            deny=[
                "Bash(**)",
                "Write(src/**)",
                "Edit(src/**)",
                "Write(/etc/**)",
                "Write(/usr/**)",
            ],
            ask_user=[],
            default_action="deny",
        ),
        Stage.PLANNING_PHASE.value: PolicyConfig(
            allow_silent=[
                "Read(**)",
                "Glob(**)",
                "Grep(**)",
                f"Write({base}/artifacts/plan/**)",
                f"Edit({base}/artifacts/plan/**)",
            ],
            deny=[
                "Bash(**)",
                "Write(src/**)",
                "Edit(src/**)",
                "Write(/etc/**)",
                "Write(/usr/**)",
            ],
            ask_user=[],
            default_action="deny",
        ),
        "CODEX_REVIEW": PolicyConfig(
            allow_silent=[
                "Read(**)",
                "Glob(**)",
                "Grep(**)",
            ],
            deny=[
                "Bash(**)",
                "Edit(**)",
                "Write(**)",
            ],
            ask_user=[],
            default_action="deny",
        ),
        "PLANNING_DEV_CONSULT": PolicyConfig(
            allow_silent=[
                "Read(**)",
                "Glob(**)",
                "Grep(**)",
            ],
            deny=[
                "Bash(**)",
                "Edit(**)",
                "Write(**)",
            ],
            ask_user=[],
            default_action="deny",
        ),
        Stage.DEV_PHASE.value: PolicyConfig(
            allow_silent=[
                "Read(**)",
                "Glob(**)",
                "Grep(**)",
                "Edit(src/**)",
                "Write(src/**)",
                "Write(tests/**)",
                "Edit(tests/**)",
                f"Write({base}/artifacts/dev/**)",
                "Bash(npm test*)",
                "Bash(pytest*)",
                "Bash(cargo test*)",
                "Bash(go test*)",
                "Bash(make test*)",
                "Bash(make build*)",
                "Bash(git add *)",
                "Bash(git commit *)",
                "Bash(git status)",
                "Bash(git diff*)",
            ],
            deny=[
                "Bash(rm -rf *)",
                "Bash(sudo *)",
                "Bash(git push --force*)",
                "Bash(git push * main*)",
                "Bash(git push * master*)",
                "Bash(curl *)",
                "Bash(wget *)",
                "Bash(pip install *)",
                "Bash(npm install *)",
                "Bash(npm publish*)",
                "Write(/etc/**)",
                "Write(/usr/**)",
                "Write(~/.ssh/**)",
            ],
            ask_user=[],
            default_action="allow_silent",
        ),
        Stage.REV_PHASE.value: PolicyConfig(
            allow_silent=[
                "Read(**)",
                "Glob(**)",
                "Grep(**)",
                f"Write({base}/artifacts/rev/**)",
                f"Edit({base}/artifacts/rev/**)",
                "Bash(git diff*)",
                "Bash(git log*)",
                "Bash(git status)",
            ],
            deny=[
                "Bash(rm -rf *)",
                "Bash(sudo *)",
                "Bash(git push*)",
                "Bash(curl *)",
                "Bash(wget *)",
                "Bash(pip install *)",
                "Bash(npm install *)",
                "Edit(src/**)",
                "Write(src/**)",
                "Edit(tests/**)",
                "Write(tests/**)",
                "Write(/etc/**)",
                "Write(/usr/**)",
            ],
            ask_user=[],
            default_action="deny",
        ),
    }


def _phase_summary_dump(state: WorkflowState, key: str) -> dict | None:
    ps = state.phase_summaries.get(key)
    return ps.model_dump() if ps else None


def _consume_subagent_result(
    state: WorkflowState,
    result: object,
    *,
    stage: Stage,
    gate_name: str,
    audit_question: str,
    audit_id_default: str,
) -> dict:
    """
    Validate + persist a stage-result/v1 payload returned by `interrupt`.

    Strategy:
      * `phase_summary` and `gate_result` are pydantic-validated and stored
        verbatim into state — no orchestrator-side paraphrase.
      * The fact that this dispatch happened (and which file it came from)
        is recorded as an audit Decision sha-pinned to the file.

    Returns a partial state-update dict with stage transitioned (caller
    sets the next stage explicitly).
    """
    if not isinstance(result, dict):
        result = {"status": str(result)}

    raw_phase_summary = result.get("phase_summary") or {}
    raw_gate_result = result.get("gate_result") or {}

    phase_summary = (
        PhaseSummary(**raw_phase_summary)
        if isinstance(raw_phase_summary, dict)
        else PhaseSummary()
    )
    gate_result = GateResult(
        name=str(raw_gate_result.get("name", gate_name)),
        passed=bool(raw_gate_result.get("passed", False)),
        reasons=list(raw_gate_result.get("reasons", [])),
    )

    decision_id = str(result.get("decision_id", audit_id_default))
    decision_file_sha = str(result.get("_decision_file_sha256", ""))
    decision_file_path = str(result.get("_decision_file_path", "")) or None
    answer = str(result.get("status", "done"))
    audit = Decision(
        decision_id=decision_id,
        question=audit_question,
        answer=answer,
        sha256=decision_file_sha or _sha256(answer),
        decided_at=now_iso(),
        file_path=decision_file_path,
    )

    return {
        "decisions": state.decisions + [audit],
        "phase_summaries": {**state.phase_summaries, stage.value: phase_summary},
        "gate_results": {**state.gate_results, gate_name: gate_result},
        "_audit_answer": answer,  # internal helper, popped before returning to LangGraph
    }


def _run_anti_drop_guard(
    result: object,
    *,
    current_stage: str,
    expected_artifacts: list[str],
    subagent_name: str,
) -> list[GuardWarning]:
    """
    Apply the Anti-Drop Guard checklist (G1/G3/G4/G5-declarative/G6) to a
    stage-result/v1 dict returned by `interrupt`. Returns a list of
    GuardWarning records — empty if everything passed.

    Severity convention (matches references/anti-drop-guard.md):
      * G1 illegal `status`              → severe (return is malformed)
      * G3 declared `stage` drift         → severe (input drift signal)
      * G4 status vs gate_result.passed  → minor  (subagent logic glitch)
      * G5 expected_artifacts missing    → minor  (declaration-level only)
      * G6 phase_summary missing/empty   → minor  (handoff info loss)

    G2 (next_action) and G7 (state writeback) from the spec are intentionally
    skipped: G2's field is not part of our schema; G7 is automatic via
    LangGraph checkpointing.
    """
    findings: list[GuardWarning] = []
    ts = now_iso()

    if not isinstance(result, dict):
        findings.append(GuardWarning(
            stage=current_stage, subagent=subagent_name,
            check_id="G1", severity="severe",
            detail=f"result is not a dict: {type(result).__name__}",
            recorded_at=ts,
        ))
        return findings

    # G1: status must exist and be in the allowed set.
    status = result.get("status")
    if status is None:
        findings.append(GuardWarning(
            stage=current_stage, subagent=subagent_name,
            check_id="G1", severity="severe",
            detail="missing required field: status",
            recorded_at=ts,
        ))
    elif status not in _VALID_STATUSES:
        findings.append(GuardWarning(
            stage=current_stage, subagent=subagent_name,
            check_id="G1", severity="severe",
            detail=f"illegal status {status!r}, must be one of {_VALID_STATUSES}",
            recorded_at=ts,
        ))

    # G3: if subagent declared a stage, it must equal the dispatch's current_stage.
    declared_stage = result.get("stage")
    if declared_stage is not None and str(declared_stage) != current_stage:
        findings.append(GuardWarning(
            stage=current_stage, subagent=subagent_name,
            check_id="G3", severity="severe",
            detail=f"declared stage {declared_stage!r} != dispatch current_stage {current_stage!r}",
            recorded_at=ts,
        ))

    # G4: status vs gate_result.passed consistency.
    gate = result.get("gate_result")
    if isinstance(gate, dict):
        passed = gate.get("passed")
        if status == "done" and passed is False:
            findings.append(GuardWarning(
                stage=current_stage, subagent=subagent_name,
                check_id="G4", severity="minor",
                detail="status=done but gate_result.passed=False",
                recorded_at=ts,
            ))
        if passed is False and status not in (None, "blocked", "needs_input"):
            findings.append(GuardWarning(
                stage=current_stage, subagent=subagent_name,
                check_id="G4", severity="minor",
                detail=f"gate_result.passed=False but status={status!r} not in (blocked, needs_input)",
                recorded_at=ts,
            ))

    # G5_DECL (declarative): every expected artifact must be claimed in
    # artifact_updates. Stays minor — subagent may simply have missed
    # naming a key; the file might still be on disk for G5_PROVE to find.
    if expected_artifacts:
        artifact_updates = result.get("artifact_updates")
        if not isinstance(artifact_updates, dict):
            artifact_updates = {}
        missing = [a for a in expected_artifacts if a not in artifact_updates]
        if missing:
            findings.append(GuardWarning(
                stage=current_stage, subagent=subagent_name,
                check_id="G5_DECL", severity="minor",
                detail=f"artifact_updates missing keys: {missing}",
                recorded_at=ts,
            ))

    # G6: phase_summary must be present, handoff_note non-empty AND
    # IM-quotable (single line, <= 280 chars). The IM contract requires
    # Hermes to verbatim-quote handoff_note; if the subagent emits a
    # multi-line or oversized handoff_note, Hermes would have to
    # paraphrase/truncate to fit IM, which reintroduces input drift.
    # We surface these as minor warnings so the workflow continues but
    # the contract violation is recorded for audit.
    HANDOFF_NOTE_MAX_CHARS = 280
    phase = result.get("phase_summary")
    if not isinstance(phase, dict):
        findings.append(GuardWarning(
            stage=current_stage, subagent=subagent_name,
            check_id="G6", severity="minor",
            detail="phase_summary missing or not a dict",
            recorded_at=ts,
        ))
    else:
        handoff_raw = str(phase.get("handoff_note", ""))
        handoff_stripped = handoff_raw.strip()
        if not handoff_stripped:
            findings.append(GuardWarning(
                stage=current_stage, subagent=subagent_name,
                check_id="G6", severity="minor",
                detail="phase_summary.handoff_note empty",
                recorded_at=ts,
            ))
        else:
            if "\n" in handoff_stripped or "\r" in handoff_stripped:
                findings.append(GuardWarning(
                    stage=current_stage, subagent=subagent_name,
                    check_id="G6", severity="minor",
                    detail=(
                        "phase_summary.handoff_note must be a single line "
                        "(no newline/CR) so Hermes can verbatim-quote it in IM"
                    ),
                    recorded_at=ts,
                ))
            if len(handoff_stripped) > HANDOFF_NOTE_MAX_CHARS:
                findings.append(GuardWarning(
                    stage=current_stage, subagent=subagent_name,
                    check_id="G6", severity="minor",
                    detail=(
                        f"phase_summary.handoff_note length "
                        f"{len(handoff_stripped)} exceeds "
                        f"{HANDOFF_NOTE_MAX_CHARS} chars; trim so Hermes can "
                        f"verbatim-quote in IM without paraphrase"
                    ),
                    recorded_at=ts,
                ))

    return findings


def _has_severe(findings: list[GuardWarning]) -> bool:
    return any(f.severity == "severe" for f in findings)


def _guard_retry_key(stage: Stage, subagent_name: str) -> str:
    return f"{stage.value}:{subagent_name}"


def _handle_guard_outcome(
    state: WorkflowState,
    findings: list[GuardWarning],
    *,
    stage: Stage,
    subagent_name: str,
) -> dict | None:
    """
    Decide what to do after Anti-Drop Guard runs against a dispatch result.

    * No severe findings → return None; caller proceeds with normal handling.
    * Severe + (attempts so far) < GUARD_RETRY_LIMIT → return a state-update
      dict that schedules a retry: pending_guard_retry is populated with the
      findings as a hint for the next attempt, and `stage` is left unchanged
      so the routing edge sends control back to the same node.
    * Severe + retry budget exhausted → return a BLOCKED state-update dict.

    The caller still owns clearing any *consumed* pending_guard_retry from
    the state going in (by adding {"pending_guard_retry": None} to its other
    returns); this helper only writes the *next* pending_guard_retry value.
    """
    if not _has_severe(findings):
        return None

    severe_ids = sorted({f.check_id for f in findings if f.severity == "severe"})
    severe_str = ",".join(severe_ids)
    key = _guard_retry_key(stage, subagent_name)
    attempts_so_far = state.guard_retries.get(key, 0)

    if attempts_so_far >= GUARD_RETRY_LIMIT:
        # No retries left → final BLOCKED.
        note = f"guard-blocked:{subagent_name}:[{severe_str}] (after {attempts_so_far} retry)"
        return {
            "stage": Stage.BLOCKED,
            "warnings": state.warnings + findings,
            "stage_log": _append_log(state, stage, note),
            "updated_at": now_iso(),
            "pending_guard_retry": None,
        }

    next_attempt = attempts_so_far + 1
    pending = GuardRetryPending(
        stage=stage.value,
        subagent=subagent_name,
        attempt=next_attempt,
        findings=findings,
    )
    note = (
        f"guard-retry-pending:{subagent_name}:[{severe_str}] "
        f"attempt={next_attempt}/{GUARD_RETRY_LIMIT}"
    )
    return {
        # stage is intentionally NOT set — leave whatever the caller had so the
        # router can detect "retry pending" and bounce back to the same node.
        "warnings": state.warnings + findings,
        "guard_retries": {**state.guard_retries, key: next_attempt},
        "pending_guard_retry": pending,
        "stage_log": _append_log(state, stage, note),
        "updated_at": now_iso(),
    }


def _sha256_file(path: str) -> str:
    """Streaming sha256 over a file's bytes."""
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def _verify_and_persist_artifacts(
    state: WorkflowState,
    result: object,
    *,
    artifact_paths: dict[str, str],
    stage: Stage,
    subagent_name: str,
) -> tuple[list[GuardWarning], dict[str, ArtifactRef]]:
    """
    Real-file half of G5: read each declared artifact at its path, sha256
    it, and compare to the subagent's claim. Findings are minor (workflow
    keeps moving). Returned ArtifactRef entries use the orch-computed sha
    as ground truth so future audit can trust state.artifacts even if a
    subagent's claim was drifted/wrong.

    Returns (findings, new_artifacts):
      * findings — list[GuardWarning] for missing files / sha mismatches /
        unknown keys
      * new_artifacts — dict[str, ArtifactRef] keyed by artifact key, ready
        to merge into state.artifacts
    """
    findings: list[GuardWarning] = []
    new_artifacts: dict[str, ArtifactRef] = {}

    if not isinstance(result, dict):
        return findings, new_artifacts
    raw = result.get("artifact_updates")
    if not isinstance(raw, dict):
        return findings, new_artifacts

    ts = now_iso()
    for key, claim in raw.items():
        if not isinstance(claim, dict):
            continue
        path = artifact_paths.get(key)
        if not path:
            # Unknown key — subagent claimed an artifact orch has no path
            # for. Stays minor: the claim is unverifiable but doesn't on
            # its own indicate a lie about a file's existence.
            findings.append(GuardWarning(
                stage=stage.value, subagent=subagent_name, check_id="G5_PROVE",
                severity="minor",
                detail=f"artifact key {key!r} not in artifact_paths — orch can't verify",
                recorded_at=ts,
            ))
            continue
        if not Path(path).exists():
            # G5_PROVE — file missing.  This is **severe** per the SKILL.md
            # contract: subagent declared the artifact (passed G5_DECL) yet
            # the file isn't on disk.  That's a direct lie about output
            # state on a par with G1/G3 — the surrounding return is no
            # longer trustworthy.  _handle_guard_outcome's existing severe
            # path will (a) write pending_guard_retry, (b) re-dispatch the
            # same subagent with guard_failure_hint, (c) BLOCK if the
            # retry still misses.  No new state field, no new mechanism —
            # just escalating an existing severity bucket.
            findings.append(GuardWarning(
                stage=stage.value, subagent=subagent_name, check_id="G5_PROVE",
                severity="severe",
                detail=(
                    f"artifact {key!r} claimed in artifact_updates but file "
                    f"missing at {path}; subagent declared output that does "
                    f"not exist on disk"
                ),
                recorded_at=ts,
            ))
            continue
        actual_sha = _sha256_file(path)
        claimed_sha = str(claim.get("sha256", ""))
        if claimed_sha and claimed_sha != actual_sha:
            # G5_PROVE — sha mismatch.  Stays minor: the file exists, orch
            # writes the orch-computed sha as ground truth, the subagent's
            # claim is silently overridden but workflow continues.
            findings.append(GuardWarning(
                stage=stage.value, subagent=subagent_name, check_id="G5_PROVE",
                severity="minor",
                detail=(
                    f"artifact {key!r} sha mismatch: claimed={claimed_sha[:12]}… "
                    f"actual={actual_sha[:12]}…; using orch-computed value"
                ),
                recorded_at=ts,
            ))
        new_artifacts[key] = ArtifactRef(
            path=path, sha256=actual_sha, written_by=stage, written_at=ts,
        )
    return findings, new_artifacts


def _run_full_guard_check(
    state: WorkflowState,
    result: object,
    *,
    current_stage: str,
    stage: Stage,
    expected_artifacts: list[str],
    subagent_name: str,
    artifact_paths: dict[str, str],
) -> tuple[list[GuardWarning], dict[str, ArtifactRef], dict | None]:
    """
    Combined guard pipeline: run declarative checks (G1/G3/G4/G5_DECL/G6)
    AND artifact-file verification (G5_PROVE) in one shot, then dispatch
    the merged findings through `_handle_guard_outcome` so that **severe
    G5_PROVE missing-file findings trigger the same retry mechanism** as
    G1/G3 severities.

    Why this exists: prior to this helper, _verify_and_persist_artifacts'
    findings were appended to state.warnings AFTER the guard outcome was
    decided, so even severe-classified verify findings (file missing per
    SKILL.md G5_PROVE) silently bypassed retry. Combining the lists into
    one _handle_guard_outcome call keeps a single severity-routing path.

    Returns (all_findings, verified_artifacts, guard_handle_extras).
    Caller flow:
        all_findings, verified_artifacts, gh = _run_full_guard_check(...)
        if gh is not None:
            return {**clear_pending, **gh}   # severe → retry or BLOCKED
        # else: proceed to persist using verified_artifacts + all_findings
    """
    guard = _run_anti_drop_guard(
        result,
        current_stage=current_stage,
        expected_artifacts=expected_artifacts,
        subagent_name=subagent_name,
    )
    verify_findings, verified_artifacts = _verify_and_persist_artifacts(
        state, result,
        artifact_paths=artifact_paths,
        stage=stage,
        subagent_name=subagent_name,
    )
    all_findings = guard + verify_findings
    gh = _handle_guard_outcome(
        state, all_findings, stage=stage, subagent_name=subagent_name
    )
    return all_findings, verified_artifacts, gh


def _consume_pending_retry(state: WorkflowState, *, stage: Stage, subagent_name: str) -> tuple[GuardRetryPending | None, dict]:
    """
    If state.pending_guard_retry targets (stage, subagent_name), return it as
    `(pending, clear_dict)` where clear_dict is `{"pending_guard_retry": None}`
    that the calling node should merge into its return value to ensure the
    flag does not persist once consumed. Otherwise return `(None, {})`.
    """
    p = state.pending_guard_retry
    if p is not None and p.stage == stage.value and p.subagent == subagent_name:
        return p, {"pending_guard_retry": None}
    return None, {}


def _gate_invalid_answer_return(
    state: WorkflowState,
    *,
    gate_stage: Stage,
    gate_label: str,
    answer: str,
    options: list[str],
    decision_rec: Decision,
) -> dict:
    """Build a state-update dict for an invalid (not-in-`options`) gate answer.

    This is distinct from `reject` — `reject` IS in every gate's options list
    and routes through the handler's own else-branch as an intentional
    BLOCK. This helper covers the **silent-catastrophe** case: user typo'd
    "appprove" / submitted Gate 2 answer to Gate 1 / left the answer field
    empty. Without this branch all of those would lump into
    `gate-N-rejected:<bad>` and look identical in stage_log to an explicit
    reject, so the user could not tell intent from accident.

    We record a `severity=severe` warning with `check_id="GATE_INVALID_ANSWER"`
    (NOT one of the G1-G6 anti-drop codes; placed in the same `state.warnings`
    list because Hermes already watches that surface, and check_id pattern
    distinguishes it from guard findings). stage_log note is
    `gate-N-invalid-answer:<raw>` so audit and Hermes IM can call this out as
    a distinct failure mode.
    """
    invalid_warning = GuardWarning(
        stage=gate_stage.value,
        subagent="user",
        check_id="GATE_INVALID_ANSWER",
        severity="severe",
        detail=(
            f"gate answer {answer!r} is not in allowed options {options}; "
            f"BLOCKING — user likely intended one of the options but "
            f"submitted a malformed/typo/cross-gate answer. This is treated "
            f"distinctly from explicit 'reject' so Hermes can surface the "
            f"discrepancy to the user instead of silently blocking."
        ),
        recorded_at=now_iso(),
    )
    return {
        "stage": Stage.BLOCKED,
        "decisions": state.decisions + [decision_rec],
        "stage_log": _append_log(
            state, gate_stage, f"{gate_label}-invalid-answer:{answer}"
        ),
        "warnings": state.warnings + [invalid_warning],
        "updated_at": now_iso(),
    }


def _consume_gate_decision(
    state: WorkflowState,
    decision: object,
    *,
    payload: dict,
    audit_id_default: str,
) -> tuple[Decision, str]:
    """Validate a gate decision file and return the audit Decision + answer."""
    if not isinstance(decision, dict):
        decision = {"answer": str(decision)}

    answer = str(decision.get("answer", ""))
    decision_id = str(decision.get("decision_id", audit_id_default))
    decision_file_sha = str(decision.get("_decision_file_sha256", ""))
    decision_file_path = str(decision.get("_decision_file_path", "")) or None

    rec = Decision(
        decision_id=decision_id,
        question=payload["question_text"],
        answer=answer,
        sha256=decision_file_sha or _sha256(answer),
        decided_at=now_iso(),
        file_path=decision_file_path,
    )
    return rec, answer


def init_node(state: WorkflowState) -> dict:
    # Only seed policies if start didn't already supply them (--policies-file).
    seeded_policies = (
        state.permission_policies
        if state.permission_policies
        else _default_policies(state.workflow_id, state.artifact_base_dir)
    )
    return {
        "stage": Stage.PM_PHASE,
        "artifact_paths": _default_artifact_paths(state.workflow_id, state.artifact_base_dir),
        "permission_policies": seeded_policies,
        "stage_log": _append_log(state, Stage.INIT, "initialized"),
        "updated_at": now_iso(),
    }


def pm_phase_node(state: WorkflowState) -> dict:
    """
    Dispatch pm_agent to the external runner via dynamic interrupt.

    On entry: pause with a subagent_dispatch payload.
    On resume: caller hands back a stage-result/v1 dict.

    If state.pending_guard_retry targets this node, dispatch_payload carries
    a `guard_failure_hint` describing what failed last time so pm_agent can
    correct its return.
    """
    pending, clear_pending = _consume_pending_retry(state, stage=Stage.PM_PHASE, subagent_name="pm_agent")
    raw_msg = state.user_msgs[0] if state.user_msgs else None
    dispatch_payload = {
        "type": "subagent_dispatch",
        "stage": "PM_PHASE",
        "subagent": "pm_agent",
        "skill_path": "shrimp/subagent/pm_agent/SKILL.md",
        "input_payload": {
            "workflow_id": state.workflow_id,
            "current_stage": "PM_PHASE",
            "raw_requirement_path": raw_msg.text_path if raw_msg else None,
            "raw_requirement_sha256": raw_msg.sha256 if raw_msg else None,
            "artifact_paths": state.artifact_paths,
            "retry_count": state.retry_count,
            "prev_phase_summary": _phase_summary_dump(state, "PM_PHASE"),
            "output_schema": "stage-result/v1",
        },
        "expected_output_schema": "stage-result/v1",
        "expected_artifacts": ["pm_spec", "pm_risks"],
        "permission_policy": (
            state.permission_policies[Stage.PM_PHASE.value].model_dump()
            if Stage.PM_PHASE.value in state.permission_policies
            else None
        ),
        "guard_failure_hint": pending.model_dump() if pending else None,
    }

    result = interrupt(dispatch_payload)
    all_findings, verified_artifacts, gh = _run_full_guard_check(
        state, result,
        current_stage=Stage.PM_PHASE.value,
        stage=Stage.PM_PHASE,
        expected_artifacts=["pm_spec", "pm_risks"],
        subagent_name="pm_agent",
        artifact_paths=state.artifact_paths,
    )
    if gh is not None:
        return {**clear_pending, **gh}

    consumed = _consume_subagent_result(
        state,
        result,
        stage=Stage.PM_PHASE,
        gate_name="Gate 1",
        audit_question="PM_PHASE subagent_dispatch result",
        audit_id_default="d_pm_dispatch",
    )
    answer = consumed.pop("_audit_answer")

    return {
        "stage": Stage.GATE_1,
        **consumed,
        "warnings": state.warnings + all_findings,
        "artifacts": {**state.artifacts, **verified_artifacts},
        "stage_log": _append_log(state, Stage.PM_PHASE, f"pm-dispatch:{answer}"),
        "updated_at": now_iso(),
        **clear_pending,
    }


def gate_1_node(state: WorkflowState) -> dict:
    """Gate 1: surface PM's claim + summary; ask user approve / rollback / reject."""
    payload = {
        "type": "gate_decision_needed",
        "gate": "Gate 1",
        "stage": "GATE_1",
        "question_id": "q_gate_1_001",
        "question_text": "PM_PHASE 已完成。Gate 1 是否通过?",
        "options": ["approve", "rollback_to_pm", "reject"],
        "subagent_gate_claim": (
            state.gate_results["Gate 1"].model_dump()
            if "Gate 1" in state.gate_results
            else None
        ),
        "phase_summary": _phase_summary_dump(state, "PM_PHASE"),
        "context_user_msg_sha256s": [m.sha256 for m in state.user_msgs],
    }

    decision = interrupt(payload)
    rec, answer = _consume_gate_decision(
        state, decision, payload=payload, audit_id_default="d_gate_1"
    )

    options_list = list(payload.get("options", []))
    if answer not in options_list:
        return _gate_invalid_answer_return(
            state, gate_stage=Stage.GATE_1, gate_label="gate-1",
            answer=answer, options=options_list, decision_rec=rec,
        )

    extras: dict = {}
    if answer == "approve":
        next_stage = Stage.PLANNING_PHASE
        note = "gate-1-approved"
    elif answer == "rollback_to_pm":
        next_stage = Stage.PM_PHASE
        note = "gate-1-rollback-to-pm"
        extras["retry_count"] = state.retry_count + 1
        extras["rollback_counts"] = {
            **state.rollback_counts,
            "PM_PHASE": state.rollback_counts.get("PM_PHASE", 0) + 1,
        }
    else:
        next_stage = Stage.BLOCKED
        note = f"gate-1-rejected:{answer}"

    return {
        "stage": next_stage,
        "decisions": state.decisions + [rec],
        "stage_log": _append_log(state, Stage.GATE_1, note),
        "updated_at": now_iso(),
        **extras,
    }


def _parse_subtask_plan(result: object) -> list[SubtaskSpec]:
    """Extract a `subtask_plan: [{task_id, title, description}, ...]` list
    from a planner stage-result, tolerating absence/malformation."""
    raw = (result if isinstance(result, dict) else {}).get("subtask_plan", []) or []
    out: list[SubtaskSpec] = []
    if isinstance(raw, list):
        for item in raw:
            if isinstance(item, dict) and "task_id" in item:
                out.append(SubtaskSpec(
                    task_id=str(item.get("task_id", "")),
                    title=str(item.get("title", "")),
                    description=str(item.get("description", "")),
                ))
    return out


def planning_phase_node(state: WorkflowState) -> dict:
    """
    Dispatch planner to the external runner.

    The planner consumes PM's spec + risks (artifacts already pinned in
    state.artifact_paths) and produces plan.md + task_list.md.

    Three outcomes after the planner returns (anti-drop guard already applied):
      * status=done & passed=true → advance to GATE_1_5 with subtask_plan
      * status=needs_input + dev_consultation present + round < MAX_PLANNING_CONSULTATIONS
         → stay in PLANNING_PHASE; routing edge sends us to planning_dev_consult
      * status=needs_input but exhausted (or planner forgot to attach
        dev_consultation) → surface to GATE_1_5 so the user can pick continue/
        rollback/reject
      * status=blocked → BLOCKED
    """
    pending, clear_pending = _consume_pending_retry(state, stage=Stage.PLANNING_PHASE, subagent_name="planner")
    latest_hint = state.user_planning_hints[-1] if state.user_planning_hints else None
    dispatch_payload = {
        "type": "subagent_dispatch",
        "stage": "PLANNING_PHASE",
        "subagent": "planner",
        "skill_path": "shrimp/subagent/pm_agent/SKILL.md",
        "input_payload": {
            "workflow_id": state.workflow_id,
            "current_stage": "PLANNING_PHASE",
            "artifact_paths": state.artifact_paths,
            "retry_count": state.retry_count,
            "prev_phase_summary": _phase_summary_dump(state, "PLANNING_PHASE"),
            "pm_phase_summary": _phase_summary_dump(state, "PM_PHASE"),
            "input_artifacts": ["pm_spec", "pm_risks"],
            "planning_consultation_round": state.planning_consultation_round,
            "planning_consultations": [c.model_dump() for c in state.planning_consultations],
            "max_planning_consultations": MAX_PLANNING_CONSULTATIONS,
            "latest_user_planning_hint": latest_hint.model_dump() if latest_hint else None,
            "all_user_planning_hints": [h.model_dump() for h in state.user_planning_hints],
            "output_schema": "stage-result/v1",
        },
        "expected_output_schema": "stage-result/v1",
        "expected_artifacts": ["plan", "task_list"],
        "permission_policy": (
            state.permission_policies[Stage.PLANNING_PHASE.value].model_dump()
            if Stage.PLANNING_PHASE.value in state.permission_policies
            else None
        ),
        "guard_failure_hint": pending.model_dump() if pending else None,
    }

    result = interrupt(dispatch_payload)
    all_findings, verified_artifacts, gh = _run_full_guard_check(
        state, result,
        current_stage=Stage.PLANNING_PHASE.value,
        stage=Stage.PLANNING_PHASE,
        expected_artifacts=["plan", "task_list"],
        subagent_name="planner",
        artifact_paths=state.artifact_paths,
    )
    if gh is not None:
        return {**clear_pending, **gh}

    consumed = _consume_subagent_result(
        state,
        result,
        stage=Stage.PLANNING_PHASE,
        gate_name="Gate 1.5",
        audit_question="PLANNING_PHASE subagent_dispatch result",
        audit_id_default="d_planning_dispatch",
    )
    answer = consumed.pop("_audit_answer")
    base_warnings = state.warnings + all_findings
    base_artifacts = {**state.artifacts, **verified_artifacts}

    # Decide which branch to take based on planner's status + dev_consultation.
    status = answer
    raw_consult = result.get("dev_consultation") if isinstance(result, dict) else None
    has_consultation = (
        isinstance(raw_consult, dict)
        and bool(str(raw_consult.get("question", "")).strip())
    )
    round_done = state.planning_consultation_round
    exhausted = round_done >= MAX_PLANNING_CONSULTATIONS

    if status == "blocked":
        return {
            "stage": Stage.BLOCKED,
            **consumed,
            "warnings": base_warnings,
            "artifacts": base_artifacts,
            "stage_log": _append_log(
                state, Stage.PLANNING_PHASE, "planning-blocked-by-planner"
            ),
            "updated_at": now_iso(),
            **clear_pending,
        }

    if status == "needs_input" and has_consultation and not exhausted:
        # Stay in PLANNING_PHASE; the conditional edge after this node will
        # route us to planning_dev_consult based on stage value.
        return {
            "stage": Stage.PLANNING_PHASE,
            **consumed,
            "warnings": base_warnings,
            "artifacts": base_artifacts,
            "stage_log": _append_log(
                state, Stage.PLANNING_PHASE,
                f"planning-needs-dev-consult:round={round_done + 1}/{MAX_PLANNING_CONSULTATIONS}",
            ),
            "updated_at": now_iso(),
            **clear_pending,
        }

    if status == "needs_input":
        # No further auto-consult — surface to GATE_1.5 for user direction.
        extra: list[GuardWarning] = []
        if exhausted:
            extra.append(GuardWarning(
                stage=Stage.PLANNING_PHASE.value,
                subagent="planner",
                check_id="PLANNING_CONSULT_LIMIT",
                severity="minor",
                detail=(
                    f"reached MAX_PLANNING_CONSULTATIONS={MAX_PLANNING_CONSULTATIONS}; "
                    "surfacing to GATE_1_5 so the user can choose continue_planning / "
                    "rollback_to_pm / reject"
                ),
                recorded_at=now_iso(),
            ))
        return {
            "stage": Stage.GATE_1_5,
            **consumed,
            "warnings": base_warnings + extra,
            "artifacts": base_artifacts,
            "subtasks": _parse_subtask_plan(result),
            "current_subtask_index": -1,
            "subtask_results": {},
            "stage_log": _append_log(
                state, Stage.PLANNING_PHASE,
                f"planning-needs-input-surface-to-gate:exhausted={exhausted}",
            ),
            "updated_at": now_iso(),
            **clear_pending,
        }

    # Default path: status=done — proceed to Gate 1.5 with subtasks.
    return {
        "stage": Stage.GATE_1_5,
        **consumed,
        "warnings": base_warnings,
        "artifacts": base_artifacts,
        "subtasks": _parse_subtask_plan(result),
        "current_subtask_index": -1,
        "subtask_results": {},
        "stage_log": _append_log(state, Stage.PLANNING_PHASE, f"planning-dispatch:{answer}"),
        "updated_at": now_iso(),
        **clear_pending,
    }


def planning_dev_consult_node(state: WorkflowState) -> dict:
    """
    Inner-PLANNING node: dispatch dev_agent in consultation mode (read-only)
    so it can answer the planner's clarification question. The planner's
    question is referenced by path+sha256 from the most recent Decision in
    state.decisions — orch never reads the verbatim text.

    On return: increments planning_consultation_round, appends a
    ConsultationRecord, leaves stage=PLANNING_PHASE so the next routing edge
    re-enters planning_phase for another planner round.
    """
    pending, clear_pending = _consume_pending_retry(
        state, stage=Stage.PLANNING_PHASE, subagent_name="dev_agent[consult]"
    )
    # The planner's question lives in the latest Decision file. We pass its
    # path + sha256 to dev_agent — orch does not parse it.
    last_decision = state.decisions[-1] if state.decisions else None
    planner_question_ref = (
        {
            "path": last_decision.file_path,
            "sha256": last_decision.sha256,
            "field": "dev_consultation",
            "stage_result_decision_id": last_decision.decision_id,
        }
        if last_decision and last_decision.file_path
        else None
    )

    next_round_index = state.planning_consultation_round + 1

    dispatch_payload = {
        "type": "subagent_dispatch",
        "stage": "PLANNING_PHASE",
        "subagent": "dev_agent",
        "skill_path": "shrimp/subagent/dev_agent/SKILL.md",
        "input_payload": {
            "workflow_id": state.workflow_id,
            "current_stage": "PLANNING_PHASE",
            "consultation_mode": True,
            "round_index": next_round_index,
            "max_rounds": MAX_PLANNING_CONSULTATIONS,
            "artifact_paths": state.artifact_paths,
            "input_artifacts": ["pm_spec", "pm_risks", "plan", "task_list"],
            "planner_question_ref": planner_question_ref,
            "prior_consultations": [c.model_dump() for c in state.planning_consultations],
            "pm_phase_summary": _phase_summary_dump(state, "PM_PHASE"),
            "planning_phase_summary": _phase_summary_dump(state, "PLANNING_PHASE"),
            "all_user_planning_hints": [h.model_dump() for h in state.user_planning_hints],
            "output_schema": "stage-result/v1",
        },
        "expected_output_schema": "stage-result/v1",
        "expected_artifacts": [],
        "permission_policy": (
            state.permission_policies["PLANNING_DEV_CONSULT"].model_dump()
            if "PLANNING_DEV_CONSULT" in state.permission_policies
            else None
        ),
        "guard_failure_hint": pending.model_dump() if pending else None,
    }

    result = interrupt(dispatch_payload)
    guard = _run_anti_drop_guard(
        result,
        current_stage=Stage.PLANNING_PHASE.value,
        expected_artifacts=[],
        subagent_name="dev_agent[consult]",
    )
    gh = _handle_guard_outcome(
        state, guard, stage=Stage.PLANNING_PHASE, subagent_name="dev_agent[consult]"
    )
    if gh is not None:
        return {**clear_pending, **gh}

    if not isinstance(result, dict):
        result = {"status": str(result)}

    answer = str(result.get("status", "done"))
    decision_file_sha = str(result.get("_decision_file_sha256", ""))
    decision_file_path = str(result.get("_decision_file_path", "")) or None
    decision_id = str(result.get("decision_id", f"d_consult_{next_round_index}"))

    audit = Decision(
        decision_id=decision_id,
        question=f"PLANNING_PHASE dev_agent consultation round {next_round_index}",
        answer=answer,
        sha256=decision_file_sha or _sha256(answer),
        decided_at=now_iso(),
        file_path=decision_file_path,
    )

    record = ConsultationRecord(
        round_index=next_round_index,
        planner_question_sha256=(planner_question_ref or {}).get("sha256", "") or "",
        dev_answer_sha256=decision_file_sha or "",
        recorded_at=now_iso(),
    )

    return {
        "stage": Stage.PLANNING_PHASE,
        "decisions": state.decisions + [audit],
        "warnings": state.warnings + guard,
        "planning_consultation_round": next_round_index,
        "planning_consultations": state.planning_consultations + [record],
        "stage_log": _append_log(
            state, Stage.PLANNING_PHASE,
            f"planning-dev-consult:round={next_round_index}/{MAX_PLANNING_CONSULTATIONS} status={answer}",
        ),
        "updated_at": now_iso(),
        **clear_pending,
    }


def gate_1_5_node(state: WorkflowState) -> dict:
    """
    Gate 1.5: surface planner's claim + summary, plus any unresolved
    consultation state. Options:
      * approve              → DEV_PHASE
      * rollback_to_pm       → PM_PHASE (retry++)
      * continue_planning    → PLANNING_PHASE re-entry, with a user-supplied
                                hint file appended to user_planning_hints
                                and planning_consultation_round reset to 0
                                so the planner gets a fresh consult quota.
                                The decision file must carry
                                  {"answer":"continue_planning",
                                   "user_hint_path":"/abs/path/to/hint.md"}
                                and the file is read here so its sha256 is
                                computed by orch (not trusted from runner).
      * reject               → BLOCKED
    """
    payload = {
        "type": "gate_decision_needed",
        "gate": "Gate 1.5",
        "stage": "GATE_1_5",
        "question_id": "q_gate_1_5_001",
        "question_text": "PLANNING_PHASE 已完成。Gate 1.5 是否通过?",
        "options": ["approve", "rollback_to_pm", "continue_planning", "reject"],
        "subagent_gate_claim": (
            state.gate_results["Gate 1.5"].model_dump()
            if "Gate 1.5" in state.gate_results
            else None
        ),
        "phase_summary": _phase_summary_dump(state, "PLANNING_PHASE"),
        "pm_phase_summary": _phase_summary_dump(state, "PM_PHASE"),
        "planning_consultation_round": state.planning_consultation_round,
        "max_planning_consultations": MAX_PLANNING_CONSULTATIONS,
        "planning_consultations": [c.model_dump() for c in state.planning_consultations],
        "context_user_msg_sha256s": [m.sha256 for m in state.user_msgs],
    }

    decision = interrupt(payload)
    rec, answer = _consume_gate_decision(
        state, decision, payload=payload, audit_id_default="d_gate_1_5"
    )

    options_list = list(payload.get("options", []))
    if answer not in options_list:
        return _gate_invalid_answer_return(
            state, gate_stage=Stage.GATE_1_5, gate_label="gate-1-5",
            answer=answer, options=options_list, decision_rec=rec,
        )

    extras: dict = {}
    if answer == "approve":
        next_stage = Stage.DEV_PHASE
        note = "gate-1-5-approved"
    elif answer == "rollback_to_pm":
        next_stage = Stage.PM_PHASE
        note = "gate-1-5-rollback-to-pm"
        extras["retry_count"] = state.retry_count + 1
        extras["rollback_counts"] = {
            **state.rollback_counts,
            "PM_PHASE": state.rollback_counts.get("PM_PHASE", 0) + 1,
        }
    elif answer == "continue_planning":
        next_stage = Stage.PLANNING_PHASE
        # Read the user-supplied hint file so orch (not runner) computes sha.
        hint_dict = decision if isinstance(decision, dict) else {}
        user_hint_path = str(hint_dict.get("user_hint_path", "")).strip()
        if not user_hint_path:
            # Missing hint file is treated as a severe block — continuing
            # without direction would just spin the same consultation.
            findings = [GuardWarning(
                stage=Stage.GATE_1_5.value, subagent="user",
                check_id="GATE_1_5_CONTINUE_NO_HINT", severity="severe",
                detail="continue_planning chosen but decision file lacks user_hint_path",
                recorded_at=now_iso(),
            )]
            return {
                "stage": Stage.BLOCKED,
                "decisions": state.decisions + [rec],
                "warnings": state.warnings + findings,
                "stage_log": _append_log(
                    state, Stage.GATE_1_5,
                    "gate-1-5-continue-rejected:no-user-hint-path",
                ),
                "updated_at": now_iso(),
            }

        # Stat first so we can reject oversize files BEFORE reading them
        # into memory.  An over-cap hint is almost certainly user error
        # (wrong path) or a wedge attempt; either way, we don't want orch
        # to OOM/hang in the middle of an active workflow.
        hint_path_obj = Path(user_hint_path)
        try:
            stat_size = hint_path_obj.stat().st_size
        except OSError as exc:
            findings = [GuardWarning(
                stage=Stage.GATE_1_5.value, subagent="user",
                check_id="GATE_1_5_CONTINUE_HINT_UNREADABLE", severity="severe",
                detail=f"unable to stat user_hint_path={user_hint_path!r}: {exc}",
                recorded_at=now_iso(),
            )]
            return {
                "stage": Stage.BLOCKED,
                "decisions": state.decisions + [rec],
                "warnings": state.warnings + findings,
                "stage_log": _append_log(
                    state, Stage.GATE_1_5,
                    "gate-1-5-continue-rejected:unreadable-hint",
                ),
                "updated_at": now_iso(),
            }

        if stat_size > MAX_USER_HINT_BYTES:
            findings = [GuardWarning(
                stage=Stage.GATE_1_5.value, subagent="user",
                check_id="GATE_1_5_CONTINUE_HINT_TOO_LARGE", severity="severe",
                detail=(
                    f"user_hint_path={user_hint_path!r} size {stat_size} bytes "
                    f"exceeds cap {MAX_USER_HINT_BYTES} bytes; refusing to read. "
                    f"continue_planning hints should be a short prose direction, "
                    f"not a binary or multi-MB document."
                ),
                recorded_at=now_iso(),
            )]
            return {
                "stage": Stage.BLOCKED,
                "decisions": state.decisions + [rec],
                "warnings": state.warnings + findings,
                "stage_log": _append_log(
                    state, Stage.GATE_1_5,
                    "gate-1-5-continue-rejected:hint-too-large",
                ),
                "updated_at": now_iso(),
            }

        try:
            hint_text = hint_path_obj.read_text(encoding="utf-8")
        except OSError as exc:
            findings = [GuardWarning(
                stage=Stage.GATE_1_5.value, subagent="user",
                check_id="GATE_1_5_CONTINUE_HINT_UNREADABLE", severity="severe",
                detail=f"unable to read user_hint_path={user_hint_path!r}: {exc}",
                recorded_at=now_iso(),
            )]
            return {
                "stage": Stage.BLOCKED,
                "decisions": state.decisions + [rec],
                "warnings": state.warnings + findings,
                "stage_log": _append_log(
                    state, Stage.GATE_1_5,
                    "gate-1-5-continue-rejected:unreadable-hint",
                ),
                "updated_at": now_iso(),
            }
        except UnicodeDecodeError as exc:
            findings = [GuardWarning(
                stage=Stage.GATE_1_5.value, subagent="user",
                check_id="GATE_1_5_CONTINUE_HINT_NOT_UTF8", severity="severe",
                detail=(
                    f"user_hint_path={user_hint_path!r} is not valid UTF-8: {exc}. "
                    f"continue_planning hints must be plain UTF-8 text."
                ),
                recorded_at=now_iso(),
            )]
            return {
                "stage": Stage.BLOCKED,
                "decisions": state.decisions + [rec],
                "warnings": state.warnings + findings,
                "stage_log": _append_log(
                    state, Stage.GATE_1_5,
                    "gate-1-5-continue-rejected:hint-not-utf8",
                ),
                "updated_at": now_iso(),
            }

        hint_ref = MsgRef(
            msg_id=f"hint_{len(state.user_planning_hints) + 1:04d}",
            sha256=_sha256(hint_text),
            received_at=now_iso(),
            text_path=str(user_hint_path),
            byte_length=len(hint_text.encode("utf-8")),
            char_length=len(hint_text),
        )
        extras["user_planning_hints"] = state.user_planning_hints + [hint_ref]
        extras["planning_consultation_round"] = 0  # fresh quota for next run
        note = f"gate-1-5-continue-planning:hint_sha={hint_ref.sha256[:12]}"
    else:
        next_stage = Stage.BLOCKED
        note = f"gate-1-5-rejected:{answer}"

    return {
        "stage": next_stage,
        "decisions": state.decisions + [rec],
        "stage_log": _append_log(state, Stage.GATE_1_5, note),
        "updated_at": now_iso(),
        **extras,
    }


def dev_loop_router_node(state: WorkflowState) -> dict:
    """
    Decide what's next inside DEV_PHASE: pop the next subtask off the queue
    (advance current_subtask_index), or — if all subtasks are accounted for —
    aggregate per-subtask results into the DEV_PHASE phase_summary + Gate 2
    gate_result and exit DEV_PHASE.

    This node is pure routing + state update. It does NOT interrupt.
    The conditional edge after it reads `state.stage` to decide where to go.
    """
    next_index = state.current_subtask_index + 1
    if next_index < len(state.subtasks):
        next_id = state.subtasks[next_index].task_id
        return {
            "current_subtask_index": next_index,
            "stage_log": _append_log(
                state, Stage.DEV_PHASE, f"dev-loop:enter-subtask-{next_id}"
            ),
            "updated_at": now_iso(),
        }

    # Queue exhausted (or empty from the start) — aggregate.
    #
    # NOTE on input-drift: DEV_PHASE has N dev_agents + N codex_reviewers,
    # so unlike PM/PLANNING/REV there is **no single subagent** that
    # produced this aggregate phase_summary verbatim. Orch is forced to
    # synthesize it. The discipline:
    #   * Closed-vocab fields (status, passed, counts) — orch composes
    #     freely using f-strings; no input drift since values are
    #     bool/int/enum not free text.
    #   * Free-text fields (review_reasons) — preserved **one entry per
    #     reason**, no joining with separators; joining with ", " loses
    #     semantic boundaries when a reason itself contains a comma.
    #   * Subagent voice (dev_handoff_note / codex_handoff_note) lives in
    #     state.subtask_results and is **the** source of truth for
    #     downstream (REV, Hermes IM); we do NOT replay it into this
    #     aggregate. The handoff_note here is a synthetic count summary,
    #     not a verbatim quote — SKILL.md documents this exception.
    all_passed = all(r.review_passed for r in state.subtask_results.values())
    open_issues_list: list[str] = []
    for sid, r in state.subtask_results.items():
        if r.review_passed:
            continue
        if not r.review_reasons:
            open_issues_list.append(
                f"subtask {sid} codex review failed (no reasons given)"
            )
            continue
        for reason in r.review_reasons:
            # one entry per reason, verbatim text — no truncation, no
            # joining; downstream (REV / Gate 2 / Hermes) iterates the
            # list to recover original boundaries
            open_issues_list.append(
                f"subtask {sid} codex review failed: {reason}"
            )
    summary = PhaseSummary(
        decisions=[
            f"subtask {sid}: dev_status={r.dev_status} review_passed={r.review_passed}"
            for sid, r in state.subtask_results.items()
        ],
        open_issues=open_issues_list,
        risks=[],
        handoff_note=(
            f"DEV done (synthetic); {len(state.subtask_results)}/{len(state.subtasks)} "
            f"subtasks reviewed, all_passed={all_passed}"
        ),
    )
    gate2 = GateResult(
        name="Gate 2",
        passed=all_passed,
        reasons=[]
        if all_passed
        else [
            f"subtask {sid} codex review failed"
            for sid, r in state.subtask_results.items()
            if not r.review_passed
        ],
    )

    return {
        "stage": Stage.GATE_2,
        "phase_summaries": {**state.phase_summaries, Stage.DEV_PHASE.value: summary},
        "gate_results": {**state.gate_results, "Gate 2": gate2},
        "current_subtask_index": -1,  # reset for potential future re-entry
        "stage_log": _append_log(
            state,
            Stage.DEV_PHASE,
            f"dev-loop:exit subtasks_done={len(state.subtask_results)}/{len(state.subtasks)} all_passed={all_passed}",
        ),
        "updated_at": now_iso(),
    }


def _current_subtask(state: WorkflowState) -> SubtaskSpec | None:
    if 0 <= state.current_subtask_index < len(state.subtasks):
        return state.subtasks[state.current_subtask_index]
    return None


def dev_subtask_dispatch_node(state: WorkflowState) -> dict:
    """
    Dispatch dev_agent for the *current* subtask only. dev_agent drives
    cc-lead to write code for this one subtask; orch records the result and
    proceeds to codex_review for the same subtask.
    """
    subtask = _current_subtask(state)
    if subtask is None:
        # Shouldn't happen — router ensures index is valid before edging here
        return {
            "stage_log": _append_log(state, Stage.DEV_PHASE, "dev-subtask-dispatch:no-current"),
            "updated_at": now_iso(),
        }

    subagent_name = f"dev_agent[{subtask.task_id}]"
    pending, clear_pending = _consume_pending_retry(state, stage=Stage.DEV_PHASE, subagent_name=subagent_name)

    dispatch_payload = {
        "type": "subagent_dispatch",
        "stage": "DEV_PHASE",
        "subagent": "dev_agent",
        "skill_path": "shrimp/subagent/dev_agent/SKILL.md",
        "input_payload": {
            "workflow_id": state.workflow_id,
            "current_stage": "DEV_PHASE",
            "artifact_paths": state.artifact_paths,
            "retry_count": state.retry_count,
            "current_subtask": subtask.model_dump(),
            "subtask_index": state.current_subtask_index,
            "subtask_total": len(state.subtasks),
            "completed_subtask_results": {
                sid: r.model_dump() for sid, r in state.subtask_results.items()
            },
            "pm_phase_summary": _phase_summary_dump(state, "PM_PHASE"),
            "planning_phase_summary": _phase_summary_dump(state, "PLANNING_PHASE"),
            "input_artifacts": ["pm_spec", "plan", "task_list"],
            "output_schema": "stage-result/v1",
        },
        "expected_output_schema": "stage-result/v1",
        "expected_artifacts": ["dev_summary", "dev_self_check", "dev_changed_files"],
        "permission_policy": (
            state.permission_policies[Stage.DEV_PHASE.value].model_dump()
            if Stage.DEV_PHASE.value in state.permission_policies
            else None
        ),
        "guard_failure_hint": pending.model_dump() if pending else None,
    }

    result = interrupt(dispatch_payload)
    all_findings, verified_artifacts, gh = _run_full_guard_check(
        state, result,
        current_stage=Stage.DEV_PHASE.value,
        stage=Stage.DEV_PHASE,
        expected_artifacts=["dev_summary", "dev_self_check", "dev_changed_files"],
        subagent_name=subagent_name,
        artifact_paths=state.artifact_paths,
    )
    if gh is not None:
        return {**clear_pending, **gh}

    if not isinstance(result, dict):
        result = {"status": str(result)}

    raw_phase = result.get("phase_summary") or {}
    handoff = ""
    if isinstance(raw_phase, dict):
        handoff = str(raw_phase.get("handoff_note", ""))

    decision_id = str(result.get("decision_id", f"d_dev_{subtask.task_id}"))
    decision_file_sha = str(result.get("_decision_file_sha256", ""))
    decision_file_path = str(result.get("_decision_file_path", "")) or None
    answer = str(result.get("status", "done"))

    audit = Decision(
        decision_id=decision_id,
        question=f"DEV_PHASE subtask {subtask.task_id} dispatch result",
        answer=answer,
        sha256=decision_file_sha or _sha256(answer),
        decided_at=now_iso(),
        file_path=decision_file_path,
    )

    # Stash partial result; codex node will fill in the review part.
    partial = SubtaskResult(
        dev_status=answer,
        dev_handoff_note=handoff,
        dev_decision_sha256=decision_file_sha,
    )

    return {
        "decisions": state.decisions + [audit],
        "warnings": state.warnings + all_findings,
        "artifacts": {**state.artifacts, **verified_artifacts},
        "subtask_results": {**state.subtask_results, subtask.task_id: partial},
        "stage_log": _append_log(
            state, Stage.DEV_PHASE, f"dev-subtask-{subtask.task_id}-dispatched:{answer}"
        ),
        "updated_at": now_iso(),
        **clear_pending,
    }


def dev_codex_review_node(state: WorkflowState) -> dict:
    """
    Dispatch codex_reviewer for the current subtask's diff. Read-only by
    permission_policy. Failure (review_passed=false) is recorded but does NOT
    abort — DEV continues to next subtask, the user sees aggregate at Gate 2.
    """
    subtask = _current_subtask(state)
    if subtask is None:
        return {
            "stage_log": _append_log(state, Stage.DEV_PHASE, "codex-review:no-current"),
            "updated_at": now_iso(),
        }

    subagent_name = f"codex_reviewer[{subtask.task_id}]"
    pending, clear_pending = _consume_pending_retry(state, stage=Stage.DEV_PHASE, subagent_name=subagent_name)
    prior_dev = state.subtask_results.get(subtask.task_id, SubtaskResult())

    dispatch_payload = {
        "type": "subagent_dispatch",
        "stage": "DEV_PHASE",
        "subagent": "codex_reviewer",
        "skill_path": "shrimp/subagent/codex_reviewer/SKILL.md",
        "input_payload": {
            "workflow_id": state.workflow_id,
            "current_stage": "DEV_PHASE",
            "review_target_subtask": subtask.model_dump(),
            "subtask_index": state.current_subtask_index,
            "subtask_total": len(state.subtasks),
            "dev_handoff_note": prior_dev.dev_handoff_note,
            "dev_decision_sha256": prior_dev.dev_decision_sha256,
            "artifact_paths": state.artifact_paths,
            "input_artifacts": ["dev_summary", "dev_self_check", "dev_changed_files"],
            "output_schema": "stage-result/v1",
        },
        "expected_output_schema": "stage-result/v1",
        "expected_artifacts": [],
        "permission_policy": (
            state.permission_policies["CODEX_REVIEW"].model_dump()
            if "CODEX_REVIEW" in state.permission_policies
            else None
        ),
        "guard_failure_hint": pending.model_dump() if pending else None,
    }

    result = interrupt(dispatch_payload)
    guard = _run_anti_drop_guard(
        result,
        current_stage=Stage.DEV_PHASE.value,
        expected_artifacts=[],
        subagent_name=subagent_name,
    )
    gh = _handle_guard_outcome(state, guard, stage=Stage.DEV_PHASE, subagent_name=subagent_name)
    if gh is not None:
        return {**clear_pending, **gh}

    if not isinstance(result, dict):
        result = {"status": str(result)}

    raw_phase = result.get("phase_summary") or {}
    handoff = ""
    if isinstance(raw_phase, dict):
        handoff = str(raw_phase.get("handoff_note", ""))

    raw_gate = result.get("gate_result") or {}
    review_passed = bool(raw_gate.get("passed", False)) if isinstance(raw_gate, dict) else False
    review_reasons = (
        list(raw_gate.get("reasons", []))
        if isinstance(raw_gate, dict) and isinstance(raw_gate.get("reasons"), list)
        else []
    )

    decision_id = str(result.get("decision_id", f"d_codex_{subtask.task_id}"))
    decision_file_sha = str(result.get("_decision_file_sha256", ""))
    decision_file_path = str(result.get("_decision_file_path", "")) or None
    answer = str(result.get("status", "done"))

    audit = Decision(
        decision_id=decision_id,
        question=f"DEV_PHASE subtask {subtask.task_id} codex_review result",
        answer=answer,
        sha256=decision_file_sha or _sha256(answer),
        decided_at=now_iso(),
        file_path=decision_file_path,
    )

    merged = SubtaskResult(
        dev_status=prior_dev.dev_status,
        dev_handoff_note=prior_dev.dev_handoff_note,
        dev_decision_sha256=prior_dev.dev_decision_sha256,
        codex_status=answer,
        codex_handoff_note=handoff,
        codex_decision_sha256=decision_file_sha,
        review_passed=review_passed,
        review_reasons=review_reasons,
    )

    return {
        "decisions": state.decisions + [audit],
        "warnings": state.warnings + guard,
        "subtask_results": {**state.subtask_results, subtask.task_id: merged},
        "stage_log": _append_log(
            state,
            Stage.DEV_PHASE,
            f"dev-subtask-{subtask.task_id}-reviewed:passed={review_passed}",
        ),
        "updated_at": now_iso(),
        **clear_pending,
    }


def gate_2_node(state: WorkflowState) -> dict:
    """
    Gate 2: surface dev's claim + summary; ask user
    approve / rollback_to_planning / rollback_to_pm / reject.

    Two-way rollback at this gate — DEV problems can be either in
    plan-level decomposition or in upstream spec, and the user picks.
    (Gate 3 widens this further with rollback_to_dev as well.)
    """
    payload = {
        "type": "gate_decision_needed",
        "gate": "Gate 2",
        "stage": "GATE_2",
        "question_id": "q_gate_2_001",
        "question_text": "DEV_PHASE 已完成。Gate 2 是否通过?",
        "options": ["approve", "rollback_to_planning", "rollback_to_pm", "reject"],
        "subagent_gate_claim": (
            state.gate_results["Gate 2"].model_dump()
            if "Gate 2" in state.gate_results
            else None
        ),
        "phase_summary": _phase_summary_dump(state, "DEV_PHASE"),
        "pm_phase_summary": _phase_summary_dump(state, "PM_PHASE"),
        "planning_phase_summary": _phase_summary_dump(state, "PLANNING_PHASE"),
        "context_user_msg_sha256s": [m.sha256 for m in state.user_msgs],
    }

    decision = interrupt(payload)
    rec, answer = _consume_gate_decision(
        state, decision, payload=payload, audit_id_default="d_gate_2"
    )

    options_list = list(payload.get("options", []))
    if answer not in options_list:
        return _gate_invalid_answer_return(
            state, gate_stage=Stage.GATE_2, gate_label="gate-2",
            answer=answer, options=options_list, decision_rec=rec,
        )

    extras: dict = {}
    if answer == "approve":
        next_stage = Stage.REV_PHASE
        note = "gate-2-approved"
    elif answer == "rollback_to_planning":
        next_stage = Stage.PLANNING_PHASE
        note = "gate-2-rollback-to-planning"
        extras["retry_count"] = state.retry_count + 1
        extras["rollback_counts"] = {
            **state.rollback_counts,
            "PLANNING_PHASE": state.rollback_counts.get("PLANNING_PHASE", 0) + 1,
        }
    elif answer == "rollback_to_pm":
        next_stage = Stage.PM_PHASE
        note = "gate-2-rollback-to-pm"
        extras["retry_count"] = state.retry_count + 1
        extras["rollback_counts"] = {
            **state.rollback_counts,
            "PM_PHASE": state.rollback_counts.get("PM_PHASE", 0) + 1,
        }
    else:
        next_stage = Stage.BLOCKED
        note = f"gate-2-rejected:{answer}"

    return {
        "stage": next_stage,
        "decisions": state.decisions + [rec],
        "stage_log": _append_log(state, Stage.GATE_2, note),
        "updated_at": now_iso(),
        **extras,
    }


def rev_dispatch_node(state: WorkflowState) -> dict:
    """
    First half of REV_PHASE: dispatch rev_agent for an end-to-end review.

    rev_agent reads PM/PLAN/DEV phase summaries + per-subtask review verdicts +
    artifact_paths, and produces a rev_report + acceptance_checklist. Its
    `phase_summary` is persisted under phase_summaries["REV_PHASE"]; its
    `gate_result` claim (if any) is captured as a self-claim under
    "Gate 3 - rev_agent" but is NOT the Gate 3 signal — that comes from
    codex_final next.
    """
    pending, clear_pending = _consume_pending_retry(state, stage=Stage.REV_PHASE, subagent_name="rev_agent")
    dispatch_payload = {
        "type": "subagent_dispatch",
        "stage": "REV_PHASE",
        "subagent": "rev_agent",
        "skill_path": "shrimp/subagent/rev_agent/SKILL.md",
        "input_payload": {
            "workflow_id": state.workflow_id,
            "current_stage": "REV_PHASE",
            "artifact_paths": state.artifact_paths,
            "retry_count": state.retry_count,
            "prev_phase_summary": _phase_summary_dump(state, "REV_PHASE"),
            "pm_phase_summary": _phase_summary_dump(state, "PM_PHASE"),
            "planning_phase_summary": _phase_summary_dump(state, "PLANNING_PHASE"),
            "dev_phase_summary": _phase_summary_dump(state, "DEV_PHASE"),
            "subtask_results": {
                sid: r.model_dump() for sid, r in state.subtask_results.items()
            },
            "input_artifacts": [
                "pm_spec",
                "pm_risks",
                "plan",
                "task_list",
                "dev_summary",
                "dev_self_check",
                "dev_changed_files",
            ],
            "output_schema": "stage-result/v1",
        },
        "expected_output_schema": "stage-result/v1",
        "expected_artifacts": ["rev_report", "acceptance_checklist"],
        "permission_policy": (
            state.permission_policies[Stage.REV_PHASE.value].model_dump()
            if Stage.REV_PHASE.value in state.permission_policies
            else None
        ),
        "guard_failure_hint": pending.model_dump() if pending else None,
    }

    result = interrupt(dispatch_payload)
    all_findings, verified_artifacts, gh = _run_full_guard_check(
        state, result,
        current_stage=Stage.REV_PHASE.value,
        stage=Stage.REV_PHASE,
        expected_artifacts=["rev_report", "acceptance_checklist"],
        subagent_name="rev_agent",
        artifact_paths=state.artifact_paths,
    )
    if gh is not None:
        return {**clear_pending, **gh}

    if not isinstance(result, dict):
        result = {"status": str(result)}

    raw_phase = result.get("phase_summary") or {}
    phase_summary = (
        PhaseSummary(**raw_phase) if isinstance(raw_phase, dict) else PhaseSummary()
    )

    raw_gate = result.get("gate_result") or {}
    gate_claim = GateResult(
        name=str(raw_gate.get("name", "Gate 3 - rev_agent")) if isinstance(raw_gate, dict) else "Gate 3 - rev_agent",
        passed=bool(raw_gate.get("passed", False)) if isinstance(raw_gate, dict) else False,
        reasons=list(raw_gate.get("reasons", [])) if isinstance(raw_gate, dict) and isinstance(raw_gate.get("reasons"), list) else [],
    )

    decision_id = str(result.get("decision_id", "d_rev_agent"))
    decision_file_sha = str(result.get("_decision_file_sha256", ""))
    decision_file_path = str(result.get("_decision_file_path", "")) or None
    answer = str(result.get("status", "done"))
    audit = Decision(
        decision_id=decision_id,
        question="REV_PHASE rev_agent dispatch result",
        answer=answer,
        sha256=decision_file_sha or _sha256(answer),
        decided_at=now_iso(),
        file_path=decision_file_path,
    )

    return {
        "decisions": state.decisions + [audit],
        "warnings": state.warnings + guard + verify_findings,
        "artifacts": {**state.artifacts, **verified_artifacts},
        "phase_summaries": {**state.phase_summaries, Stage.REV_PHASE.value: phase_summary},
        "gate_results": {**state.gate_results, "Gate 3 - rev_agent": gate_claim},
        "stage_log": _append_log(state, Stage.REV_PHASE, f"rev-agent-dispatched:{answer}"),
        "updated_at": now_iso(),
        **clear_pending,
    }


def rev_codex_final_node(state: WorkflowState) -> dict:
    """
    Second half of REV_PHASE: dispatch codex_final (codex_reviewer in final
    end-to-end mode) to vet the full work against rev_agent's checklist.

    Reuses the CODEX_REVIEW read-only policy. Its phase_summary lands under
    phase_summaries["REV_PHASE_CODEX"], and its gate_result becomes the
    Gate 3 signal that the user sees at the gate.
    """
    pending, clear_pending = _consume_pending_retry(state, stage=Stage.REV_PHASE, subagent_name="codex_final")
    dispatch_payload = {
        "type": "subagent_dispatch",
        "stage": "REV_PHASE",
        "subagent": "codex_final",
        "skill_path": "shrimp/subagent/codex_reviewer/SKILL.md",
        "input_payload": {
            "workflow_id": state.workflow_id,
            "current_stage": "REV_PHASE",
            "review_mode": "final_end_to_end",
            "artifact_paths": state.artifact_paths,
            "retry_count": state.retry_count,
            "pm_phase_summary": _phase_summary_dump(state, "PM_PHASE"),
            "planning_phase_summary": _phase_summary_dump(state, "PLANNING_PHASE"),
            "dev_phase_summary": _phase_summary_dump(state, "DEV_PHASE"),
            "rev_phase_summary": _phase_summary_dump(state, "REV_PHASE"),
            "rev_agent_gate_claim": (
                state.gate_results["Gate 3 - rev_agent"].model_dump()
                if "Gate 3 - rev_agent" in state.gate_results
                else None
            ),
            "subtask_results": {
                sid: r.model_dump() for sid, r in state.subtask_results.items()
            },
            "input_artifacts": [
                "pm_spec",
                "plan",
                "dev_summary",
                "dev_changed_files",
                "rev_report",
                "acceptance_checklist",
            ],
            "output_schema": "stage-result/v1",
        },
        "expected_output_schema": "stage-result/v1",
        "expected_artifacts": [],
        "permission_policy": (
            state.permission_policies["CODEX_REVIEW"].model_dump()
            if "CODEX_REVIEW" in state.permission_policies
            else None
        ),
        "guard_failure_hint": pending.model_dump() if pending else None,
    }

    result = interrupt(dispatch_payload)
    guard = _run_anti_drop_guard(
        result,
        current_stage=Stage.REV_PHASE.value,
        expected_artifacts=[],
        subagent_name="codex_final",
    )
    gh = _handle_guard_outcome(state, guard, stage=Stage.REV_PHASE, subagent_name="codex_final")
    if gh is not None:
        return {**clear_pending, **gh}

    if not isinstance(result, dict):
        result = {"status": str(result)}

    raw_phase = result.get("phase_summary") or {}
    phase_summary = (
        PhaseSummary(**raw_phase) if isinstance(raw_phase, dict) else PhaseSummary()
    )

    raw_gate = result.get("gate_result") or {}
    gate3 = GateResult(
        name=str(raw_gate.get("name", "Gate 3")) if isinstance(raw_gate, dict) else "Gate 3",
        passed=bool(raw_gate.get("passed", False)) if isinstance(raw_gate, dict) else False,
        reasons=list(raw_gate.get("reasons", [])) if isinstance(raw_gate, dict) and isinstance(raw_gate.get("reasons"), list) else [],
    )

    decision_id = str(result.get("decision_id", "d_codex_final"))
    decision_file_sha = str(result.get("_decision_file_sha256", ""))
    decision_file_path = str(result.get("_decision_file_path", "")) or None
    answer = str(result.get("status", "done"))
    audit = Decision(
        decision_id=decision_id,
        question="REV_PHASE codex_final dispatch result",
        answer=answer,
        sha256=decision_file_sha or _sha256(answer),
        decided_at=now_iso(),
        file_path=decision_file_path,
    )

    return {
        "stage": Stage.GATE_3,
        "decisions": state.decisions + [audit],
        "warnings": state.warnings + guard,
        "phase_summaries": {**state.phase_summaries, "REV_PHASE_CODEX": phase_summary},
        "gate_results": {**state.gate_results, "Gate 3": gate3},
        "stage_log": _append_log(
            state, Stage.REV_PHASE, f"codex-final-dispatched:{answer} passed={gate3.passed}"
        ),
        "updated_at": now_iso(),
        **clear_pending,
    }


def gate_3_node(state: WorkflowState) -> dict:
    """
    Gate 3: final acceptance gate. Three-way rollback (DEV / PLANNING / PM)
    plus approve and reject. rollback_to_dev clears subtask_results and
    resets current_subtask_index=-1 so DEV reruns from scratch.
    """
    payload = {
        "type": "gate_decision_needed",
        "gate": "Gate 3",
        "stage": "GATE_3",
        "question_id": "q_gate_3_001",
        "question_text": "REV_PHASE 已完成。Gate 3 是否通过?",
        "options": [
            "approve",
            "rollback_to_dev",
            "rollback_to_planning",
            "rollback_to_pm",
            "reject",
        ],
        "subagent_gate_claim": (
            state.gate_results["Gate 3"].model_dump()
            if "Gate 3" in state.gate_results
            else None
        ),
        "rev_agent_gate_claim": (
            state.gate_results["Gate 3 - rev_agent"].model_dump()
            if "Gate 3 - rev_agent" in state.gate_results
            else None
        ),
        "phase_summary": _phase_summary_dump(state, "REV_PHASE"),
        "codex_phase_summary": _phase_summary_dump(state, "REV_PHASE_CODEX"),
        "pm_phase_summary": _phase_summary_dump(state, "PM_PHASE"),
        "planning_phase_summary": _phase_summary_dump(state, "PLANNING_PHASE"),
        "dev_phase_summary": _phase_summary_dump(state, "DEV_PHASE"),
        "context_user_msg_sha256s": [m.sha256 for m in state.user_msgs],
    }

    decision = interrupt(payload)
    rec, answer = _consume_gate_decision(
        state, decision, payload=payload, audit_id_default="d_gate_3"
    )

    options_list = list(payload.get("options", []))
    if answer not in options_list:
        return _gate_invalid_answer_return(
            state, gate_stage=Stage.GATE_3, gate_label="gate-3",
            answer=answer, options=options_list, decision_rec=rec,
        )

    extras: dict = {}
    if answer == "approve":
        next_stage = Stage.DONE
        note = "gate-3-approved"
    elif answer == "rollback_to_dev":
        next_stage = Stage.DEV_PHASE
        note = "gate-3-rollback-to-dev"
        extras["retry_count"] = state.retry_count + 1
        extras["rollback_counts"] = {
            **state.rollback_counts,
            "DEV_PHASE": state.rollback_counts.get("DEV_PHASE", 0) + 1,
        }
        extras["subtask_results"] = {}
        extras["current_subtask_index"] = -1
    elif answer == "rollback_to_planning":
        next_stage = Stage.PLANNING_PHASE
        note = "gate-3-rollback-to-planning"
        extras["retry_count"] = state.retry_count + 1
        extras["rollback_counts"] = {
            **state.rollback_counts,
            "PLANNING_PHASE": state.rollback_counts.get("PLANNING_PHASE", 0) + 1,
        }
    elif answer == "rollback_to_pm":
        next_stage = Stage.PM_PHASE
        note = "gate-3-rollback-to-pm"
        extras["retry_count"] = state.retry_count + 1
        extras["rollback_counts"] = {
            **state.rollback_counts,
            "PM_PHASE": state.rollback_counts.get("PM_PHASE", 0) + 1,
        }
    else:
        next_stage = Stage.BLOCKED
        note = f"gate-3-rejected:{answer}"

    return {
        "stage": next_stage,
        "decisions": state.decisions + [rec],
        "stage_log": _append_log(state, Stage.GATE_3, note),
        "updated_at": now_iso(),
        **extras,
    }


def _route_after_gate_1(state: WorkflowState) -> str:
    if state.stage == Stage.PLANNING_PHASE:
        return "approved"
    if state.stage == Stage.PM_PHASE:
        return "rollback"
    return "rejected"


def _route_after_gate_1_5(state: WorkflowState) -> str:
    if state.stage == Stage.DEV_PHASE:
        return "approved"
    if state.stage == Stage.PM_PHASE:
        return "rollback"
    if state.stage == Stage.PLANNING_PHASE:
        return "continue"
    return "rejected"


def _route_after_planning(state: WorkflowState) -> str:
    """planning_phase outcome:
      * pending_guard_retry      → guard retry pending, loop back to planning_phase
      * stayed in PLANNING_PHASE → planner asked dev_agent for consultation
      * advanced to GATE_1_5     → planner gave done OR exhausted needs_input
      * BLOCKED                  → severe guard exhausted retry, or planner status=blocked
    """
    if state.pending_guard_retry is not None:
        return "retry"
    if state.stage == Stage.PLANNING_PHASE:
        return "consult"
    if state.stage == Stage.GATE_1_5:
        return "to_gate"
    return "blocked"


def _route_after_gate_2(state: WorkflowState) -> str:
    if state.stage == Stage.REV_PHASE:
        return "approved"
    if state.stage == Stage.PLANNING_PHASE:
        return "rollback_planning"
    if state.stage == Stage.PM_PHASE:
        return "rollback_pm"
    return "rejected"


def _route_after_dev_loop(state: WorkflowState) -> str:
    """After dev_loop_router runs: if it transitioned to GATE_2, exit;
    otherwise we have a current subtask to dispatch."""
    if state.stage == Stage.GATE_2:
        return "exit"
    return "next"


def _route_dispatch_outcome(state: WorkflowState) -> str:
    """Successor router for dispatch nodes:
      * pending_guard_retry set → "retry": loop back to the same dispatch node
      * stage == BLOCKED         → "blocked": short-circuit to END
      * otherwise                → "next": advance to the configured successor
    """
    if state.pending_guard_retry is not None:
        return "retry"
    if state.stage == Stage.BLOCKED:
        return "blocked"
    return "next"


def _route_after_gate_3(state: WorkflowState) -> str:
    if state.stage == Stage.DONE:
        return "approved"
    if state.stage == Stage.DEV_PHASE:
        return "rollback_dev"
    if state.stage == Stage.PLANNING_PHASE:
        return "rollback_planning"
    if state.stage == Stage.PM_PHASE:
        return "rollback_pm"
    return "rejected"


def build_graph() -> StateGraph:
    builder = StateGraph(WorkflowState)
    builder.add_node("init", init_node)
    builder.add_node("pm_phase", pm_phase_node)
    builder.add_node("gate_1", gate_1_node)
    builder.add_node("planning_phase", planning_phase_node)
    builder.add_node("planning_dev_consult", planning_dev_consult_node)
    builder.add_node("gate_1_5", gate_1_5_node)
    builder.add_node("dev_loop_router", dev_loop_router_node)
    builder.add_node("dev_subtask_dispatch", dev_subtask_dispatch_node)
    builder.add_node("dev_codex_review", dev_codex_review_node)
    builder.add_node("gate_2", gate_2_node)
    builder.add_node("rev_dispatch", rev_dispatch_node)
    builder.add_node("rev_codex_final", rev_codex_final_node)
    builder.add_node("gate_3", gate_3_node)

    builder.add_edge(START, "init")
    builder.add_edge("init", "pm_phase")
    builder.add_conditional_edges(
        "pm_phase", _route_dispatch_outcome,
        {"retry": "pm_phase", "next": "gate_1", "blocked": END},
    )
    builder.add_conditional_edges(
        "gate_1",
        _route_after_gate_1,
        {"approved": "planning_phase", "rollback": "pm_phase", "rejected": END},
    )
    builder.add_conditional_edges(
        "planning_phase",
        _route_after_planning,
        {
            "retry": "planning_phase",
            "consult": "planning_dev_consult",
            "to_gate": "gate_1_5",
            "blocked": END,
        },
    )
    builder.add_conditional_edges(
        "planning_dev_consult", _route_dispatch_outcome,
        {"retry": "planning_dev_consult", "next": "planning_phase", "blocked": END},
    )
    builder.add_conditional_edges(
        "gate_1_5",
        _route_after_gate_1_5,
        {
            "approved": "dev_loop_router",
            "rollback": "pm_phase",
            "continue": "planning_phase",
            "rejected": END,
        },
    )
    builder.add_conditional_edges(
        "dev_loop_router",
        _route_after_dev_loop,
        {"next": "dev_subtask_dispatch", "exit": "gate_2"},
    )
    builder.add_conditional_edges(
        "dev_subtask_dispatch", _route_dispatch_outcome,
        {"retry": "dev_subtask_dispatch", "next": "dev_codex_review", "blocked": END},
    )
    builder.add_conditional_edges(
        "dev_codex_review", _route_dispatch_outcome,
        {"retry": "dev_codex_review", "next": "dev_loop_router", "blocked": END},
    )
    builder.add_conditional_edges(
        "gate_2",
        _route_after_gate_2,
        {
            "approved": "rev_dispatch",
            "rollback_planning": "planning_phase",
            "rollback_pm": "pm_phase",
            "rejected": END,
        },
    )
    builder.add_conditional_edges(
        "rev_dispatch", _route_dispatch_outcome,
        {"retry": "rev_dispatch", "next": "rev_codex_final", "blocked": END},
    )
    builder.add_conditional_edges(
        "rev_codex_final", _route_dispatch_outcome,
        {"retry": "rev_codex_final", "next": "gate_3", "blocked": END},
    )
    builder.add_conditional_edges(
        "gate_3",
        _route_after_gate_3,
        {
            "approved": END,
            "rollback_dev": "dev_loop_router",
            "rollback_planning": "planning_phase",
            "rollback_pm": "pm_phase",
            "rejected": END,
        },
    )
    return builder
