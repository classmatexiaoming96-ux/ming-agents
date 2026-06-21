# Store Interface Documentation

> Generated: 2026-06-15  
> Purpose: Define the complete exported API of the `*store.Store` type for the Loop Engineering platform.

---

## Overview

`Store` is the central data-access layer wrapping a PostgreSQL connection (`*sql.DB`). All exported methods delegate to per-entity repo structs. The store does **not** hold any in-memory state — it is safe to share across goroutines provided each individual method call completes before the next one begins (i.e. sql.DB handles concurrency internally). For multi-operation atomicity, use `WithTx`.

**Thread-safety contract:** Each exported method is safe to call concurrently from multiple goroutines. The underlying `*sql.DB` maintains its own connection pool and handles race conditions internally. However, **sequences** of calls that must be atomic (e.g. check-then-update) must be wrapped in `WithTx`.

---

## Entity: Run

### `CreateRun(r *domain.Run) error`
- **Signature:** `func (s *Store) CreateRun(r *domain.Run) error`
- **Description:** Inserts a new run record. Assigns `ID`, `CreatedAt`, `UpdatedAt`, and `Version=1` automatically.
- **Parameters:** `r` — run object (ID is generated if zero). `WDLSource`, `EndedAt`, `ErrorMsg` are left to caller.
- **Returns:** `error` on DB failure or optimistic-lock error.
- **Thread-safe:** Yes (single atomic INSERT).

---

### `GetRun(id uuid.UUID) (*domain.Run, error)`
- **Signature:** `func (s *Store) GetRun(id uuid.UUID) (*domain.Run, error)`
- **Description:** Fetches a run by its UUID.
- **Parameters:** `id` — the run UUID.
- **Returns:** The run record, or `error` (including `"run not found"` if `sql.ErrNoRows`).
- **Thread-safe:** Yes.

---

### `UpdateRun(r *domain.Run) error`
- **Signature:** `func (s *Store) UpdateRun(r *domain.Run) error`
- **Description:** Updates an existing run with optimistic locking. Increments `Version` on success.
- **Parameters:** `r` — must have valid `ID` and current `Version`.
- **Returns:** `error` if DB failure or zero rows affected (optimistic lock failure).
- **Thread-safe:** Yes (atomic UPDATE with version check).

---

### `UpdateRunStatus(id uuid.UUID, from, to domain.RunStatus, version int) error`
- **Signature:** `func (s *Store) UpdateRunStatus(id uuid.UUID, from, to domain.RunStatus, version int) error`
- **Description:** Atomically transitions a run's status from `from` to `to` only if current status matches `from` and `version` matches. Used for enforced state-machine transitions (e.g. pending→running). Increments `Version` on success.
- **Parameters:**
  - `id` — run UUID
  - `from` — expected current status (must match for transition to succeed)
  - `to` — target status
  - `version` — expected version for optimistic lock
- **Returns:** `error` if transition not allowed or DB failure.
- **Thread-safe:** Yes (conditional UPDATE).
- **Status:** ✅ Exists in `runRepo` but **NOT yet exposed** via `Store`. Stub added.

---

### `ListRuns(limit, offset int) ([]*domain.Run, error)`
- **Signature:** `func (s *Store) ListRuns(limit, offset int) ([]*domain.Run, error)`
- **Description:** Returns runs ordered by `created_at DESC`. Used for API listing. Default limit is 50.
- **Parameters:** `limit` (0 = default 50), `offset`.
- **Returns:** Slice of run pointers; empty slice with no error if none found.
- **Thread-safe:** Yes.
- **Note:** No caller identified in `engine/` or `worker/` — reserved for API layer.

---

## Entity: Step

### `CreateStep(step *domain.Step) error`
- **Signature:** `func (s *Store) CreateStep(step *domain.Step) error`
- **Description:** Inserts a new step. Assigns `ID`, `CreatedAt`, `UpdatedAt`.
- **Parameters:** `step` — step object (ID generated if zero).
- **Returns:** `error` on DB failure.
- **Thread-safe:** Yes.

---

### `GetStep(id uuid.UUID) (*domain.Step, error)`
- **Signature:** `func (s *Store) GetStep(id uuid.UUID) (*domain.Step, error)`
- **Description:** Fetches a step by UUID.
- **Parameters:** `id` — step UUID.
- **Returns:** The step record, or error.
- **Thread-safe:** Yes.

---

### `UpdateStep(step *domain.Step) error`
- **Signature:** `func (s *Store) UpdateStep(step *domain.Step) error`
- **Description:** Updates step fields including status, iteration, attempt, inputs/outputs JSON, skip reason. Sets `UpdatedAt`.
- **Parameters:** `step` — must have valid `ID`.
- **Returns:** `error` if DB failure or zero rows.
- **Thread-safe:** Yes.

---

### `UpdateStepStatus(id uuid.UUID, to domain.StepStatus) error`
- **Signature:** `func (s *Store) UpdateStepStatus(id uuid.UUID, to domain.StepStatus) error`
- **Description:** Quick single-field status update. Sets `UpdatedAt`. Does NOT do optimistic locking.
- **Parameters:** `id` — step UUID; `to` — target status.
- **Returns:** `error` if not found or DB failure.
- **Thread-safe:** Yes.
- **Status:** ✅ Exists in `stepRepo` but **NOT yet exposed** via `Store`. Stub added.

---

### `GetStepsByRun(runID uuid.UUID) ([]*domain.Step, error)`
- **Signature:** `func (s *Store) GetStepsByRun(runID uuid.UUID) ([]*domain.Step, error)`
- **Description:** Returns all steps for a run, ordered by `created_at ASC`.
- **Parameters:** `runID` — the run UUID.
- **Returns:** Slice of step pointers.
- **Thread-safe:** Yes.

---

## Entity: Task

### `CreateTask(t *domain.Task) error`
- **Signature:** `func (s *Store) CreateTask(t *domain.Task) error`
- **Description:** Inserts a task into `agent_task_queue`. Assigns `ID`, `CreatedAt`, `Version=1`.
- **Parameters:** `t` — task object (ID generated if zero).
- **Returns:** `error` on DB failure.
- **Thread-safe:** Yes.

---

### `GetTask(id uuid.UUID) (*domain.Task, error)`
- **Signature:** `func (s *Store) GetTask(id uuid.UUID) (*domain.Task, error)`
- **Description:** Fetches a task by UUID.
- **Parameters:** `id` — task UUID.
- **Returns:** The task record, or error.
- **Thread-safe:** Yes.

---

### `UpdateTask(t *domain.Task) error`
- **Signature:** `func (s *Store) UpdateTask(t *domain.Task) error`
- **Description:** Full task update with optimistic locking. Updates status, agent result, result summary, timestamps. Increments `Version`.
- **Parameters:** `t` — must have valid `ID` and current `Version`.
- **Returns:** `error` on failure or optimistic lock miss.
- **Thread-safe:** Yes.

---

### `ClaimTask() (*domain.Task, error)`
- **Signature:** `func (s *Store) ClaimTask() (*domain.Task, error)`
- **Description:** Atomically claims one pending task using `SELECT FOR UPDATE SKIP LOCKED`. Sets status to `claimed` and `claimed_at`. Returns `sql.ErrNoRows` if no pending tasks available.
- **Parameters:** None.
- **Returns:** The claimed task, or `sql.ErrNoRows` / error.
- **Thread-safe:** Yes (uses `SKIP LOCKED` to avoid contention between workers).

---

### `GetTasksByRun(runID uuid.UUID) ([]*domain.Task, error)`
- **Signature:** `func (s *Store) GetTasksByRun(runID uuid.UUID) ([]*domain.Task, error)`
- **Description:** Returns all tasks for a run, ordered by `created_at ASC`.
- **Parameters:** `runID` — the run UUID.
- **Returns:** Slice of task pointers.
- **Thread-safe:** Yes.

---

### `GetTasksByStep(stepID uuid.UUID) ([]*domain.Task, error)`
- **Signature:** `func (s *Store) GetTasksByStep(stepID uuid.UUID) ([]*domain.Task, error)`
- **Description:** Returns all tasks for a step, ordered by `created_at ASC`.
- **Parameters:** `stepID` — the step UUID.
- **Returns:** Slice of task pointers.
- **Thread-safe:** Yes.

---

### `ClaimedCount(runID uuid.UUID) (int, error)`
- **Signature:** `func (s *Store) ClaimedCount(runID uuid.UUID) (int, error)`
- **Description:** Returns the number of tasks with status `claimed` for a run.
- **Parameters:** `runID` — the run UUID.
- **Returns:** Count (≥0) and error.
- **Thread-safe:** Yes.

---

### `PendingCount(runID uuid.UUID) (int, error)`
- **Signature:** `func (s *Store) PendingCount(runID uuid.UUID) (int, error)`
- **Description:** Returns the number of tasks with status `pending` for a run.
- **Parameters:** `runID` — the run UUID.
- **Returns:** Count (≥0) and error.
- **Thread-safe:** Yes.

---

### `UpdateTaskStatus(id uuid.UUID, status domain.TaskStatus) error`
- **Signature:** `func (s *Store) UpdateTaskStatus(id uuid.UUID, status domain.TaskStatus) error`
- **Description:** Lightweight status-only update for workers. Handles `claimed_at` / `completed_at` timestamp injection based on target status. Does NOT use optimistic locking (workers need fire-and-forget updates).
- **Parameters:** `id` — task UUID; `status` — target task status.
- **Returns:** `error` on DB failure.
- **Thread-safe:** Yes (but note: not atomic with other task updates).
- **Status:** ✅ Exists as an ad-hoc method in `worker/worker.go` (lines 122–124) but **NOT yet in `store.go`**. Stub added.

---

### `SetTaskResult(id uuid.UUID, result json.RawMessage, summary string) error`
- **Signature:** `func (s *Store) SetTaskResult(id uuid.UUID, result json.RawMessage, summary string) error`
- **Description:** Writes agent result data to a task after completion. Sets status to `completed`, `agent_result`, `result_summary`, and `completed_at`. Used by workers to record adapter output.
- **Parameters:**
  - `id` — task UUID
  - `result` — raw JSON from the agent adapter
  - `summary` — short human-readable summary (truncated to 200 chars by caller)
- **Returns:** `error` on DB failure.
- **Thread-safe:** Yes.
- **Status:** ✅ Exists as an ad-hoc method in `worker/worker.go` (lines 127–129) but **NOT yet in `store.go`**. Stub added.

---

## Entity: Artifact

### `CreateArtifact(a *Artifact) error`
- **Signature:** `func (s *Store) CreateArtifact(a *Artifact) error`
- **Description:** Inserts a named artifact produced or consumed by a step. Assigns `ID`, `CreatedAt`.
- **Parameters:** `a` — artifact (ID generated if zero). `Type` must be `"json"`, `"text"`, or `"file"`.
- **Returns:** `error` on DB failure.
- **Thread-safe:** Yes.

---

### `GetArtifact(id uuid.UUID) (*Artifact, error)`
- **Signature:** `func (s *Store) GetArtifact(id uuid.UUID) (*Artifact, error)`
- **Description:** Fetches an artifact by UUID.
- **Parameters:** `id` — artifact UUID.
- **Returns:** The artifact record, or error.
- **Thread-safe:** Yes.
- **Note:** No caller identified in `engine/` or `worker/` — available for future use.

---

### `GetArtifactsByRun(runID uuid.UUID) ([]*Artifact, error)`
- **Signature:** `func (s *Store) GetArtifactsByRun(runID uuid.UUID) ([]*Artifact, error)`
- **Description:** Returns all artifacts for a run, ordered by `created_at ASC`.
- **Parameters:** `runID` — the run UUID.
- **Returns:** Slice of artifact pointers.
- **Thread-safe:** Yes.

---

## Entity: LoopIteration

### `CreateLoopIteration(li *domain.LoopIteration) error`
- **Signature:** `func (s *Store) CreateLoopIteration(li *domain.LoopIteration) error`
- **Description:** Inserts a loop iteration record. Assigns `ID`, `CreatedAt`.
- **Parameters:** `li` — loop iteration object.
- **Returns:** `error` on DB failure.
- **Thread-safe:** Yes.

---

### `UpdateLoopIteration(li *domain.LoopIteration) error`
- **Signature:** `func (s *Store) UpdateLoopIteration(li *domain.LoopIteration) error`
- **Description:** Updates iteration status, eval score, eval details, and converged flag.
- **Parameters:** `li` — must have valid `ID`.
- **Returns:** `error` on DB failure.
- **Thread-safe:** Yes.

---

### `GetLoopIteration(runID, stepID uuid.UUID, iteration int) (*domain.LoopIteration, error)`
- **Signature:** `func (s *Store) GetLoopIteration(runID, stepID uuid.UUID, iteration int) (*domain.LoopIteration, error)`
- **Description:** Fetches a specific loop iteration by run, step, and iteration number.
- **Parameters:** `runID`, `stepID`, `iteration`.
- **Returns:** The iteration record, or error.
- **Thread-safe:** Yes.

---

### `GetLoopIterationsByStep(runID, stepID uuid.UUID) ([]*domain.LoopIteration, error)`
- **Signature:** `func (s *Store) GetLoopIterationsByStep(runID, stepID uuid.UUID) ([]*domain.LoopIteration, error)`
- **Description:** Returns all iterations for a loop step, ordered by `iteration ASC`.
- **Parameters:** `runID`, `stepID`.
- **Returns:** Slice of iteration pointers.
- **Thread-safe:** Yes.
- **Note:** No caller identified in `engine/` or `worker/` — available for loop control logic.

---

## Infrastructure

### `BeginTx() (*sql.Tx, error)`
- **Signature:** `func (s *Store) BeginTx() (*sql.Tx, error)`
- **Description:** Starts a new DB transaction.
- **Thread-safe:** Yes.

---

### `DB() *sql.DB`
- **Signature:** `func (s *Store) DB() *sql.DB`
- **Description:** Returns the underlying DB connection. Exposed for advanced use (migrations, health checks).
- **Thread-safe:** Yes (read-only access to the connection pool reference).

---

### `WithTx(fn func(*Tx) error) error`
- **Signature:** `func (s *Store) WithTx(fn func(*Tx) error) error`
- **Description:** Runs `fn` within a transaction. Commits on success, rolls back on error or panic. This is the primary way to group multiple store operations atomically.
- **Parameters:** `fn` — function receiving a `*Tx` wrapper.
- **Returns:** `error` from `fn` or from rollback.
- **Thread-safe:** Yes (transaction isolation).

---

### `Now() time.Time`
- **Signature:** `func Now() time.Time`
- **Description:** Package-level helper returning `time.Now().UTC()`. Used by all repo methods for consistent timestamps.
- **Thread-safe:** Yes (reads wall clock).

---

## Callers Summary

| Caller File | Methods Called |
|---|---|
| `engine/engine.go` | `CreateRun`, `CreateStep` |
| `engine/driver.go` | `GetRun`, `UpdateRun`, `GetStepsByRun`, `ClaimedCount`, `PendingCount`, `CreateTask`, `UpdateStep`, `GetStep`, `GetTasksByStep`, `GetTasksByRun`, `UpdateTask` |
| `engine/translator.go` | `UpdateStep` |
| `engine/context.go` | `CreateArtifact` |
| `engine/persistence.go` | `CreateArtifact`, `GetRun`, `GetStepsByRun`, `GetTasksByRun`, `GetArtifactsByRun`, `UpdateStep`, `UpdateRun` |
| `worker/worker.go` | `ClaimTask`, `UpdateTaskStatus`, `SetTaskResult` |

---

## Missing Methods (exist at caller, not in Store)

| Method | Defined In | Needs Stub |
|---|---|---|
| `UpdateRunStatus` | `runRepo` (not exposed) | ✅ Added to store.go |
| `UpdateStepStatus` | `stepRepo` (not exposed) | ✅ Added to store.go |
| `UpdateTaskStatus` | `worker/worker.go` line 122 | ✅ Added to store.go |
| `SetTaskResult` | `worker/worker.go` line 127 | ✅ Added to store.go |

## Extra Methods (in Store, no caller in engine/worker)

| Method | Reason |
|---|---|
| `ListRuns` | Reserved for API layer (not engine/worker) |
| `GetArtifact` | Available for future use (no current caller) |
| `GetLoopIterationsByStep` | Available for loop control logic (no current caller) |

---

## Thread-Safety Summary

All methods are **thread-safe** at the individual call level because they delegate to `*sql.DB`, which manages its own connection pool and guarantees that each query/exec runs atomically at the DB level.

The following are **NOT atomic as a sequence** and require external synchronization (e.g. `WithTx`) if combined:

- `GetRun` + `UpdateRun` (check-then-act)
- `GetStep` + `UpdateStep` (check-then-act)
- `GetTasksByStep` + `UpdateTask` (check-then-act)
- `ClaimedCount` + `PendingCount` + `CreateTask` (scheduler dispatch loop — the `RunDriver.dispatchLoop` mutex protects the ready-set, but the store calls themselves are each independent queries)