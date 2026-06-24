# Workflow MVP

This package implements the MVP dynamic workflow runner for agent-driven development work. The workflow turns one user request into a three-node pipeline:

1. Requirements clarification
2. Planning
3. Development with review

The public entrypoint is:

```go
workflow.Run(ctx, repoRoot, userInput)
```

The CLI entrypoint lives at `cmd/workflow`.

## Architecture

The workflow is intentionally file-backed. Each node writes durable markdown contracts under `docs/` and run artifacts under `.workflow/runs/<run_id>/`.

```text
user input
  |
  v
Node 1: clarification
  writes docs/requirements-clarity.md
  |
  v
Node 2: planning
  writes docs/planning.md
  |
  v
Node 3: development + review
  writes docs/output.md
```

The run state is persisted as JSON:

```text
.workflow/runs/<run_id>/state.json
```

Node status values are `PENDING`, `RUNNING`, `WAITING_REVIEW`, `COMPLETED`, and `FAILED`.

## Chatbot Integration

This package provides the workflow execution boundary that the chatbot calls; it does not receive chat messages directly and does not implement message classification. In product use, the chatbot is the user-facing entrypoint: users describe the task in the conversation, and the chatbot layer decides whether to start a new workflow run or answer against an existing run.

The chatbot should treat a message as a new workflow request when it contains an implementation intent rather than a status or feedback intent. Typical trigger messages are:

```text
帮我实现这个功能：...
创建一个 workflow 来修复 ...
开始执行：...
Run this workflow: ...
Implement ...
Fix ...
```

Follow-up messages such as `查看状态`, `读一下 node2 产物`, or `这里需要改成 ...` should be handled by the chatbot as interaction with an existing run instead of starting a new run.

When a new-run message is detected, the chatbot backend builds the workflow input from the conversation:

- The latest user message becomes `userInput`.
- The server-side repository checkout becomes `repoRoot`.
- The request context becomes `ctx`.

It then calls the public workflow package API:

```go
runID, err := workflow.Run(ctx, repoRoot, userInput)
```

The current `workflow.Run` implementation is synchronous: it returns after clarification, planning, development, and review have finished or after an error stops the run. A chatbot backend can call it synchronously for MVP usage, or run it in a background worker if it wants to acknowledge immediately. The minimum user-visible start or completion response should include:

```text
已启动 workflow
run_id: <run_id>
```

The workflow package writes artifacts that the chatbot can read back into conversation responses:

- After Node 1, it can summarize `docs/requirements-clarity.md`: ambiguities, assumptions, risks, and proposed subtasks.
- After Node 2, it can summarize `docs/planning.md`: task ID, subtask list, repo paths, and acceptance criteria.
- During or after Node 3, it can answer status questions from `.workflow/runs/<run_id>/state.json` and per-session `*.out.md` files.
- After Node 3, it reports `docs/output.md`: development session results, review status, blocking issues if any, and final output paths.

This MVP workflow does not pause for human approval between nodes. It has an automated review barrier after development and allows one retry pass for blocking review issues. If a future chatbot flow adds human confirmation, that pause/resume behavior belongs in the chatbot or API orchestration layer around this package, not inside `workflow.Run` as currently implemented.

For Feishu/Lark usage, the bot can send a notification with the `run_id` and output paths after the workflow finishes, or after a background worker observes a failed state in `.workflow/runs/<run_id>/state.json`. The existing API server has a separate notifier for `WAITING_USER_INPUT` in the older run engine, but this file-backed three-node MVP does not emit that state.

The expected chatbot response model is:

- Start response: confirm that a workflow run has started or completed and show `run_id`.
- Progress response: if the workflow is run in a background worker, report current node status and point to the relevant artifact.
- Clarification response: summarize open questions from Node 1.
- Planning response: show planned subtasks and acceptance criteria from Node 2.
- Completion response: summarize Node 3 review result and link the three stable outputs: `docs/requirements-clarity.md`, `docs/planning.md`, and `docs/output.md`.

## Node 1: Requirements Clarification

`RunClarification(ctx, repoRoot, userInput)` starts two independent clarification agents in parallel:

- Codex
- Claude Code

Each agent receives the same user input and is asked to analyze requirements without modifying files. The goal is to surface ambiguities, assumptions, acceptance criteria, risks, and likely subtasks from independent perspectives.

Per-agent artifacts are written under:

```text
.workflow/runs/<run_id>/node1/
```

Current artifacts include:

```text
codex.prompt.md
codex.out.md
codex.exit
claude-code.prompt.md
claude-code.out.md
claude-code.exit
```

The merged contract is written to:

```text
docs/requirements-clarity.md
```

The merged document wraps each agent output in tagged sections:

```markdown
<!-- agent:codex begin -->
...
<!-- agent:codex end -->

<!-- agent:claude-code begin -->
...
<!-- agent:claude-code end -->
```

If one clarification agent fails, its captured output and error are still included. The node returns an error only if all clarification agents fail or the merged output cannot be written.

## Node 2: Planning

`RunPlanning(ctx, repoRoot, clarFile)` reads `docs/requirements-clarity.md`, extracts the tagged clarification sections, and asks Codex to produce a structured plan.

The planning output is written to:

```text
docs/planning.md
```

The file contains a human-readable summary and one fenced JSON plan:

```json
{
  "task_id": "example-task-id",
  "subtasks": [
    {
      "id": "subtask-1",
      "agent_type": "codex",
      "repo_path": "workflow",
      "description": "Concrete work to perform.",
      "acceptance_criteria": [
        "Observable completion criterion"
      ]
    }
  ]
}
```

The MVP validates the plan strictly before development:

- `task_id` must be present.
- At least one subtask is required.
- Subtask IDs must be unique.
- Every subtask must use `agent_type: "codex"`.
- `repo_path` must be relative and must not escape the repository root.
- Each subtask needs a description and at least one non-empty acceptance criterion.

## Node 3: Development And Review

`RunDevelopment(ctx, repoRoot, plan)` validates the plan and runs each subtask concurrently.

Each subtask runs:

```text
codex exec <prompt>
```

The working directory is:

```text
<repoRoot>/<subtask.repo_path>
```

Node 3 writes per-subtask artifacts under:

```text
.workflow/runs/<run_id>/node3/
```

Common artifacts include:

```text
dev-1.prompt.md
dev-1.out.md
dev-1.exit
review.prompt.md
review.out.md
review.exit
review.diff
review.status
```

After all development sessions finish, the review step captures:

```text
git diff -- . ':!docs/output.md'
git status --short
```

Codex then reviews the development outputs, diff, and plan. Review issues are parsed from markdown. Any issue with severity `blocking` marks the review as needing revision.

The MVP allows one retry pass. Blocking issues are routed back to the matching subtask by `subtask_id` or `session_id`; otherwise the first subtask is used as a fallback target.

The final workflow contract is written to:

```text
docs/output.md
```

It summarizes development sessions, exit codes, review status, issue count, and review output.

## Data Flow

```text
Input string or input file
  -> RunClarification
  -> docs/requirements-clarity.md
  -> RunPlanning
  -> docs/planning.md
  -> RunDevelopment
  -> docs/output.md
```

Durable run metadata and raw agent outputs are stored separately:

```text
.workflow/runs/<run_id>/
  state.json
  node1/
    codex.prompt.md
    codex.out.md
    codex.exit
    claude-code.prompt.md
    claude-code.out.md
    claude-code.exit
  node3/
    dev-*.prompt.md
    dev-*.out.md
    dev-*.exit
    review.prompt.md
    review.out.md
    review.exit
    review.diff
    review.status
```

## Running The CLI

Build the CLI with an explicit output path. This avoids a name collision with the existing `workflow/` directory:

```bash
cd /root/repos/ming-agents/server
go build -o /tmp/ming-workflow ./cmd/workflow
```

Run with inline input:

```bash
cd /root/repos/ming-agents/server
/tmp/ming-workflow "Implement the requested feature"
```

Run with an input file:

```bash
cd /root/repos/ming-agents/server
/tmp/ming-workflow -input /path/to/request.md
```

The CLI uses `PWD` as `repoRoot`, so run it from the repository root you want the agents to edit.

On success, it prints:

```text
run_id: <run_id>
Outputs: docs/requirements-clarity.md, docs/planning.md, docs/output.md
```

## Requirements

The MVP assumes these commands are installed and authenticated on the host:

- `codex`
- `claude`
- `git`

Node 1 and Node 2 use interactive PTY-backed session managers for Codex and Claude Code where applicable. Node 3 development subtasks use `codex exec` directly.

## Verification

Useful package checks:

```bash
cd /root/repos/ming-agents/server
go build ./workflow/...
go test ./workflow/...
go vet ./workflow/...
go build -o /tmp/ming-workflow ./cmd/workflow
```
