# SHRIMP MVP — Version A

> Go Daemon (Postgres task queue + `exec.Command` → Claude Code) + Next.js status console.

## 0. Background — why this shape

We analyzed **Multica**, which drives agent CLIs by spawning them directly with
`exec.Command` (no `tmux`), wiring `stdin`/`stdout`/`stderr` through goroutine
pipes for real‑time, non‑blocking I/O. MVP Version A copies that model because it
is the simplest thing that:

- starts/stops a Claude Code process per task deterministically,
- streams output live without a terminal multiplexer,
- cancels cleanly via `SIGTERM` instead of killing a `tmux` pane.

The state of the world lives in **Postgres**, not in daemon memory, so the daemon
is crash‑restartable: on boot it recovers tasks that were mid‑flight when a
previous instance died.

```
┌──────────┐    POST /api/tasks     ┌──────────────────────────────┐
│  Web UI  │ ─────────────────────▶ │            Daemon             │
│ (Next.js)│ ◀───── WS /ws ──────── │  ┌───────┐  ┌──────────────┐  │
└──────────┘   live task events     │  │ HTTP  │  │  scheduler   │  │
                                     │  │ + WS  │  │  loop        │  │
                                     │  └───┬───┘  └──────┬───────┘  │
                                     │      │  EventBus   │          │
                                     │      └─────┬───────┘          │
                                     │            ▼                  │
                                     │      ┌───────────┐            │
                                     │      │  Runner   │ exec.Cmd ──┼──▶ claude -p
                                     │      └───────────┘            │     (stdin/stdout pipe)
                                     └────────────┬──────────────────┘
                                                  │ pgx
                                                  ▼
                                          ┌───────────────┐
                                          │   Postgres    │
                                          │ agents        │
                                          │ agent_task_q  │
                                          └───────────────┘
```

## 1. Components

| Component | Path | Responsibility |
|-----------|------|----------------|
| Daemon core | `server/daemon.go` | scheduler loop, per‑agent concurrency, heartbeats |
| HTTP + WS | `server/server.go` | REST API + WebSocket push |
| Event bus | `server/events.go` | in‑process pub/sub between scheduler & WS hub |
| Config | `server/config.go` | env + JSON file loading (no hardcoded agents) |
| Queue | `server/task/queue.go` | claim/heartbeat/complete on `agent_task_queue` |
| Runner | `server/task/runner.go` | `exec.Command` + goroutine pipes + SIGTERM cancel |
| Ctx manager | `server/task/context.go` | task‑id → `context.CancelFunc` registry |
| Agent registry | `server/agent/agent.go` | agent config + upsert to DB |
| DB | `server/db` | connect + embedded SQL migrations |
| Control CLI | `server/cmd/daemon.go` | `start` / `stop` / `logs` (pidfile based) |
| Web | `web/` | task list, live updates, task submission |

## 2. Data model

```sql
agents(
  id, name UNIQUE, runtime_mode, max_concurrent_tasks,
  model, thinking_level, created_at
)

agent_task_queue(
  id, agent_id → agents.id,
  status,            -- pending|claimed|running|completed|failed|canceled
  priority SMALLINT, -- 1 high, 2 medium, 3 low (ORDER BY priority ASC)
  prompt, result, error,
  worker_id, attempts, cancel_requested,
  created_at, claimed_at, started_at, heartbeat_at, completed_at
)
```

Status machine:

```
pending ──claim──▶ claimed ──start──▶ running ─┬─ completed
   ▲                                            ├─ failed
   └──── RecoverOrphanedTasks (stale heartbeat) ┘
                       running/claimed ──cancel──▶ canceled
```

## 3. Queue protocol (claim / heartbeat / complete)

**Claim** is a single atomic `UPDATE ... WHERE id = (SELECT ... FOR UPDATE SKIP
LOCKED LIMIT 1)`. `SKIP LOCKED` lets multiple workers (or future multi‑daemon
deployments) claim concurrently without contention. Ordering is `priority ASC,
created_at ASC` so high priority and older tasks drain first.

**Heartbeat**: while a task runs, a ticker updates `heartbeat_at`. This is the
liveness signal.

**Complete / Fail**: terminal update with `result`/`error` + `completed_at`.

**RecoverOrphanedTasks** (run on startup): any task in `claimed`/`running` whose
`heartbeat_at` is older than `orphan_timeout` is reset to `pending` so a fresh
daemon re‑runs it. This is what makes crash recovery work.

## 4. Concurrency model

- One **scheduler loop** ticks every `poll_interval`.
- For each agent it computes free slots = `max_concurrent_tasks - inflight`, and
  claims up to that many `pending` tasks for that agent.
- Each claimed task runs in its own goroutine under a `context.WithCancel`
  derived from the daemon root context. The cancel func is registered in the
  `task.Manager` keyed by task id.
- A buffered semaphore per agent bounds in‑flight goroutines as a second guard.

## 5. Process execution (the Multica‑style core)

`task.Runner.Run`:

1. `exec.CommandContext(ctx, claudeCmd, args...)` — args templated from agent
   config (`{{model}}` substitution); **nothing hardcoded**.
2. `cmd.Cancel = SIGTERM` and `cmd.WaitDelay` for a grace period before the
   runtime escalates to `SIGKILL`. So `context cancel → SIGTERM → (grace) → kill`.
3. `StdinPipe` — the prompt is streamed in then closed (EOF).
4. `StdoutPipe`/`StderrPipe` — each drained by its own goroutine with a
   large‑buffer `bufio.Scanner`; every line is (a) appended to the result buffer
   and (b) pushed to an `onChunk` callback → EventBus → WebSocket. Draining in
   goroutines is what prevents the child from blocking on a full pipe.
5. `cmd.Wait()` after both readers finish.

## 6. Real‑time updates

`EventBus` is a tiny fan‑out: scheduler/runner `Publish` task lifecycle events
and output chunks; the WebSocket hub `Subscribe`s and forwards JSON frames to
every connected browser. No polling on the client.

Event types: `task.created`, `task.claimed`, `task.running`, `task.chunk`,
`task.completed`, `task.failed`, `task.canceled`.

## 7. Configuration (no hardcoding)

All runtime config comes from env vars, with an optional JSON file
(`SHRIMP_CONFIG`) for the agent list:

| Env | Default | Meaning |
|-----|---------|---------|
| `DATABASE_URL` | — (required) | Postgres DSN |
| `SHRIMP_HTTP_ADDR` | `:8080` | API/WS listen addr |
| `SHRIMP_WORKER_ID` | hostname+pid | claim owner id |
| `SHRIMP_POLL_INTERVAL` | `1s` | scheduler tick |
| `SHRIMP_HEARTBEAT_INTERVAL` | `5s` | heartbeat tick |
| `SHRIMP_ORPHAN_TIMEOUT` | `30s` | stale → recover |
| `SHRIMP_CLAUDE_CMD` | `claude` | child executable |
| `SHRIMP_CONFIG` | — | path to agents JSON |

`config.example.json` ships a sample agent set.

## 8. Local run

```bash
# 1. Postgres
export DATABASE_URL=postgres://localhost:5432/shrimp?sslmode=disable
# 2. daemon (auto-migrates, syncs agents, recovers orphans, serves :8080)
cd server && go run .            # or: go run ./cmd start
# 3. web
cd web && npm install && NEXT_PUBLIC_DAEMON_URL=http://localhost:8080 npm run dev
```

Set `SHRIMP_CLAUDE_CMD=./mock-claude.sh` to exercise the pipeline without a real
Claude Code install.

## 9. Out of scope for Version A (Version B+)

- multi‑daemon leader election (claim already supports it via `SKIP LOCKED`)
- tmux runtime mode (`runtime_mode` column reserved)
- streaming stdout persistence / log files
- auth on the HTTP API
