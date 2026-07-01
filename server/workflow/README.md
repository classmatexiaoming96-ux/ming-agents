# Workflow MVP 技术设计文档

## 1. 概述

Workflow MVP 是一个面向“对话式研发执行”的三节点工作流系统。它把用户在 Chatbot 中提出的自然语言研发需求，拆解为可持久化、可审批、可追踪、可由 Agent 执行的完整流程：

1. 需求澄清
2. 规划拆解
3. 开发执行与评审

系统解决的核心问题是：用户的一句需求通常不够精确，直接进入开发容易遗漏约束、误解范围、缺少验收标准，也很难在多 Agent 并行执行时追踪每个子任务的上下文。Workflow MVP 通过文件化契约、节点生命周期通知、人工审批门、子任务专属对话机器人，把“需求 -> 计划 -> 开发 -> 评审”的过程变成可审计、可恢复、可对话介入的工程流程。

核心价值包括：

- 将自然语言需求转换为稳定的 Markdown 和 JSON 契约。
- 在关键节点之间加入人工审批，避免错误需求继续向后扩散。
- 为 Node 3 的每个开发子任务创建独立 Agent Session，隔离上下文与对话历史。
- 将节点和子任务生命周期事件写入对应会话，便于 Chatbot 实时提示用户。
- 将所有运行状态、输入、输出、审批请求和 Agent 对话历史落盘，降低恢复和排障成本。

公共入口：

```go
workflow.Run(ctx, repoRoot, userInput)
```

CLI 入口：

```text
cmd/workflow
```

## 2. 系统架构

Workflow MVP 是一个文件驱动的三节点流水线。每个节点都会生成稳定产物，并把运行过程写入 `.workflow/runs/<run_id>/`。Chatbot 作为用户入口，负责识别用户意图、启动 workflow、读取产物、展示通知、处理审批。

```text
┌──────────────────────────┐
│ 用户 / Chatbot 对话入口   │
└─────────────┬────────────┘
              │ 触发研发任务
              v
┌──────────────────────────┐
│ workflow.Run              │
│ run_id / repoRoot / input │
└─────────────┬────────────┘
              │
              v
┌──────────────────────────┐
│ Node 1: 需求澄清          │
│ Codex + Claude Code 并行  │
│ 输出 requirements-clarity │
└─────────────┬────────────┘
              │ approval_request
              v
        人工审批通过
              │
              v
┌──────────────────────────┐
│ Node 2: 规划              │
│ 生成 docs/planning.md     │
│ 解析并校验 Plan JSON      │
└─────────────┬────────────┘
              │ approval_request
              v
        人工审批通过
              │
              v
┌─────────────────────────────────────┐
│ Node 3: 开发与评审                  │
│ 为每个 Subtask 创建 SubtaskAgent     │
│ 子任务逐个执行，每个完成后等待审批   │
│ 最后执行 Review                     │
└─────────────┬───────────────────────┘
              v
┌──────────────────────────┐
│ docs/output.md            │
│ 最终结果与评审摘要        │
└──────────────────────────┘
```

Chatbot 的职责不是直接执行开发逻辑，而是编排用户交互：

- 识别哪些消息应该启动新 workflow。
- 后台调用 `workflow.Run`。
- 读取 `.workflow/runs/<run_id>/` 下的通知、审批请求、Agent 会话历史和节点产物。
- 将 `approval_request` 转换为用户可理解的审批提示。
- 用户确认后调用 `ApproveSession` 让 workflow 继续。
- 将用户针对某个子任务的消息路由到正确的 `SubtaskAgent`。

## 3. 核心概念

### Workflow

Workflow 是完整的三节点研发流程定义。当前 MVP 固定为 Node 1、Node 2、Node 3 三个阶段，不依赖数据库，主要通过文件系统持久化状态。

### Run

Run 是一次 workflow 执行实例。每个 run 有一个 `run_id`，其所有状态和中间产物都写入：

```text
.workflow/runs/<run_id>/
```

### Node

Node 是 workflow 的阶段。当前包括：

- `node1`：需求澄清
- `node2`：规划
- `node3`：开发与评审

每个节点会发出生命周期通知：`STARTED`、`COMPLETED`、`FAILED`。

### Subtask

Subtask 是 Node 2 规划出的开发子任务。Node 3 根据 `Plan.Subtasks` 逐个执行子任务。每个子任务包含：

- `id`
- `agent_type`
- `repo_path`
- `description`
- `acceptance_criteria`

### Agent Session

Agent Session 是一个可持久化的对话上下文。它记录某个节点或某个子任务 Agent 的消息历史，历史文件为 JSON Lines 格式。

### Approval Gate

Approval Gate 是人工审批门。workflow 在关键边界调用 `WaitForApproval` 写入审批请求并阻塞，直到 Chatbot 写入审批消息。

当前审批点：

- Node 1 完成后，进入 Node 2 前。
- Node 2 完成后，进入 Node 3 前。
- Node 3 中每个子任务完成或失败后，进入下一个子任务或 Review 前。

## 4. 三节点详解

### Node 1: 需求澄清

入口函数：

```go
RunClarification(ctx, repoRoot, userInput) (clarFile string, err error)
```

Node 1 的目标是从用户原始输入中提取更清晰的需求上下文。它会并行启动两个澄清 Agent：

- Codex
- Claude Code

每个 Agent 独立分析同一份用户输入，不互相通信。输出重点包括：

- 歧义点
- 假设
- 验收标准
- 风险
- 建议子任务

输入：

```text
userInput
repoRoot
```

主要输出：

```text
docs/requirements-clarity.md
```

Node 1 运行产物：

```text
.workflow/runs/<run_id>/node1/
  codex.prompt.md
  codex.out.md
  codex.exit
  claude-code.prompt.md
  claude-code.out.md
  claude-code.exit
  node1.messages.jsonl
```

`node1.messages.jsonl` 中包含 Node 1 的生命周期通知和审批请求。Node 1 完成后，`workflow.Run` 调用：

```go
WaitForApproval(ctx, node1SessionID, "node1")
```

Chatbot 看到 `approval_request` 后提示用户确认。用户同意后，Chatbot 调用 `ApproveSession`，workflow 才进入 Node 2。

### Node 2: 规划

入口函数：

```go
RunPlanning(ctx, repoRoot, clarFile) (plan *Plan, err error)
```

Node 2 读取 Node 1 的 `docs/requirements-clarity.md`，提取带标签的 Agent 输出：

```markdown
<!-- agent:codex begin -->
...
<!-- agent:codex end -->

<!-- agent:claude-code begin -->
...
<!-- agent:claude-code end -->
```

然后调用 Codex 生成规划文档和 JSON 执行计划。

输入：

```text
docs/requirements-clarity.md
```

输出：

```text
docs/planning.md
```

规划 JSON 结构：

```go
type Plan struct {
    TaskID   string    `json:"task_id"`
    Subtasks []Subtask `json:"subtasks"`
}
```

Node 2 会严格校验：

- `task_id` 非空。
- 至少包含一个 subtask。
- subtask ID 唯一。
- `agent_type` 必须是 `codex`。
- `repo_path` 必须是相对路径，不能逃逸仓库根目录。
- 每个 subtask 必须有描述和至少一个验收标准。

Node 2 运行产物：

```text
.workflow/runs/<run_id>/node2/
  node2.messages.jsonl
```

Node 2 完成后同样写入 `approval_request`，并在进入 Node 3 前等待人工审批。

### Node 3: 开发与评审

入口函数：

```go
RunDevelopment(ctx, repoRoot, plan) (finalState *WorkflowState, err error)
```

Node 3 根据 Node 2 的 `Plan.Subtasks` 创建子任务 Agent，并逐个执行开发子任务。每个子任务执行命令：

```text
codex exec <prompt>
```

工作目录：

```text
<repoRoot>/<subtask.repo_path>
```

Node 3 与早期并发模型不同：由于现在要求“每个子任务完成后等待人工审批，再进入下一个子任务或 Review”，当前执行顺序是串行的。

每个子任务完成或失败后：

1. 写入子任务生命周期通知。
2. 将命令输出追加到该子任务 Agent Session。
3. 调用 `WaitForApproval(ctx, sessionID, "subtask:<subtask_id>")`。
4. 审批通过后进入下一个子任务。

所有子任务完成并通过审批后，Node 3 创建 diff 快照：

```text
.workflow/runs/<run_id>/node3/review.diff
.workflow/runs/<run_id>/node3/review.status
```

然后运行 Review Agent。若 Review 报告中存在 blocking issue，MVP 允许一轮 retry。

最终输出：

```text
docs/output.md
```

## 5. 子任务对话机器人

Node 3 会为每个开发子任务创建一个独立的 `SubtaskAgent`。它代表“子任务单独对话机器人”，用于隔离该子任务的上下文、消息历史和执行产物。

结构：

```go
type SubtaskAgent struct {
    SubtaskID  string            `json:"subtask_id"`
    Session    AgentSession      `json:"session"`
    Context    map[string]string `json:"context"`
    WorkDir    string            `json:"work_dir"`
    PromptFile string            `json:"prompt_file"`
    OutFile    string            `json:"out_file"`
    ExitFile   string            `json:"exit_file"`
}
```

其中 `AgentSession` 负责保存会话状态：

```go
type AgentSession struct {
    ID          string             `json:"id"`
    AgentType   string             `json:"agent_type"`
    Status      AgentSessionStatus `json:"status"`
    HistoryFile string             `json:"history_file"`
    Messages    []AgentMessage     `json:"messages,omitempty"`
}
```

每个子任务的会话历史文件：

```text
.workflow/runs/<run_id>/node3/agents/<subtask_id>.messages.jsonl
```

所有子任务 Agent 的 manifest：

```text
.workflow/runs/<run_id>/node3/agents.json
```

### 消息路由

Chatbot 在收到用户消息后，需要判断该消息属于哪个子任务。可以使用：

```go
agent, err := workflow.RouteSubtaskMessage(agents, workflow.SubtaskMessage{
    SubtaskID: "...", // 可选，显式指定子任务
    SessionID: "...", // 可选，显式指定会话
    Content:   message,
})
```

路由规则：

- 如果 `SubtaskID` 存在，优先按子任务 ID 路由。
- 如果 `SessionID` 存在，按 Agent Session 路由。
- 如果都没有，则扫描消息内容中是否唯一提到了某个 subtask ID。
- 如果没有匹配或匹配多个，返回错误，Chatbot 应向用户追问“你指的是哪个子任务？”。

### 会话历史

Chatbot 将用户消息写入子任务会话：

```go
err := workflow.AppendAgentMessage(agent, workflow.AgentMessage{
    Role:    "user",
    Content: message,
})
```

Node 3 执行时也会写入：

- `system`：开发 prompt。
- `assistant`：Codex 执行输出。
- `notification`：生命周期通知。
- `approval_request`：审批请求。
- `approval`：审批确认。

## 6. 生命周期通知

每个节点和每个子任务都会向对应 Agent Session 写入生命周期通知。

通知结构：

```go
type NodeNotification struct {
    RunID     string             `json:"run_id"`
    NodeName  string             `json:"node_name"`
    Status    NotificationStatus `json:"status"`
    Timestamp string             `json:"timestamp"`
}
```

状态枚举：

```go
const (
    NotificationStarted   NotificationStatus = "STARTED"
    NotificationCompleted NotificationStatus = "COMPLETED"
    NotificationFailed    NotificationStatus = "FAILED"
)
```

通知以 `AgentMessage` 写入：

```json
{
  "role": "notification",
  "content": "{\"run_id\":\"...\",\"node_name\":\"node2\",\"status\":\"STARTED\",\"timestamp\":\"...\"}",
  "timestamp": "..."
}
```

节点级通知路径：

```text
.workflow/runs/<run_id>/node1/node1.messages.jsonl
.workflow/runs/<run_id>/node2/node2.messages.jsonl
.workflow/runs/<run_id>/node3/node3.messages.jsonl
```

子任务通知路径：

```text
.workflow/runs/<run_id>/node3/agents/<subtask_id>.messages.jsonl
```

Chatbot 可以轮询或监听这些 JSONL 文件，并在用户对话中展示：

```text
node1 STARTED
node1 COMPLETED
node2 STARTED
subtask:api FAILED
node3 COMPLETED
```

## 7. 人工审批门

人工审批门用于阻止 workflow 在没有用户确认的情况下进入下一阶段。审批门通过文件化 Agent Session 实现，不依赖数据库或长连接。

审批请求结构：

```go
type ApprovalRequest struct {
    RunID     string `json:"run_id,omitempty"`
    SessionID string `json:"session_id"`
    NodeName  string `json:"node_name"`
    Status    string `json:"status"`
    Timestamp string `json:"timestamp"`
}
```

阻塞函数：

```go
func WaitForApproval(ctx context.Context, sessionID, nodeName string) error
```

行为：

1. 找到已注册的 `AgentSession`。
2. 写入 `role: "approval_request"` 的消息。
3. 周期性检查同一个 history 文件中是否出现对应 `nodeName` 的 `role: "approval"` 消息。
4. 如果找到 approval，函数返回 nil。
5. 如果 `ctx` 取消或超时，函数返回 context error。

恢复函数：

```go
func ApproveSession(sessionID, nodeName, message string) error
```

Chatbot 看到审批请求后，应向用户展示节点产物和审批问题。用户确认后，Chatbot 调用 `ApproveSession`，该函数会写入 `role: "approval"` 消息，`WaitForApproval` 检测到后继续执行。

当前审批点：

```text
Node 1 完成 -> approval_request(node1) -> 用户批准 -> Node 2 开始
Node 2 完成 -> approval_request(node2) -> 用户批准 -> Node 3 开始
Subtask 完成 -> approval_request(subtask:<id>) -> 用户批准 -> 下一个 Subtask 或 Review
```

## 8. Chatbot 集成

Chatbot 是用户使用 Workflow MVP 的主要入口。

### 触发 workflow

Chatbot 应将带有实现意图的消息识别为新 workflow 请求，例如：

```text
帮我实现这个功能：...
创建一个 workflow 来修复 ...
开始执行：...
Run this workflow: ...
Implement ...
Fix ...
```

Chatbot 将最新用户消息作为 `userInput`，服务端仓库路径作为 `repoRoot`，并调用：

```go
runID, err := workflow.Run(ctx, repoRoot, userInput)
```

由于 `workflow.Run` 会在审批门阻塞，推荐 Chatbot 在后台 worker 中调用它，并立即向用户返回：

```text
已启动 workflow
run_id: <run_id>
```

### 读取产物

Chatbot 根据阶段读取稳定产物：

- Node 1：`docs/requirements-clarity.md`
- Node 2：`docs/planning.md`
- Node 3：`docs/output.md`

也可以读取运行时产物：

- `.workflow/runs/<run_id>/state.json`
- `.workflow/runs/<run_id>/node*/ *.messages.jsonl`
- `.workflow/runs/<run_id>/node3/agents.json`
- `.workflow/runs/<run_id>/node3/agents/<subtask_id>.messages.jsonl`

### 展示通知

Chatbot 轮询或监听 Agent Session history，发现 `notification` 消息后，将其转为用户可读状态：

```text
Node 2 已开始规划。
子任务 api 已完成，等待你的确认。
Node 3 评审失败，请查看 blocking issues。
```

### 处理审批

当 Chatbot 发现 `approval_request`：

1. 读取对应节点或子任务产物。
2. 向用户展示摘要。
3. 询问是否继续。
4. 用户确认后调用：

```go
workflow.ApproveSession(sessionID, nodeName, "approved by user")
```

### 阶段性回复模型

建议 Chatbot 在不同阶段给出不同类型的响应：

- Start：告知 workflow 已启动，并返回 `run_id`。
- Progress：展示节点或子任务生命周期通知。
- Clarification：总结 Node 1 的歧义、假设和验收标准。
- Planning：展示 Node 2 的子任务列表、路径和验收标准。
- Approval：展示审批请求，并等待用户确认。
- Completion：总结 Node 3 评审结果，并给出最终产物路径。

## 9. 数据流与文件结构

整体数据流：

```text
userInput
  -> RunClarification
  -> docs/requirements-clarity.md
  -> approval(node1)
  -> RunPlanning
  -> docs/planning.md
  -> approval(node2)
  -> RunDevelopment
  -> per-subtask approval
  -> RunReview
  -> docs/output.md
```

完整运行目录：

```text
.workflow/runs/<run_id>/
  state.json

  node1/
    node1.messages.jsonl
    codex.prompt.md
    codex.out.md
    codex.exit
    claude-code.prompt.md
    claude-code.out.md
    claude-code.exit

  node2/
    node2.messages.jsonl

  node3/
    node3.messages.jsonl
    agents.json
    agents/
      <subtask_id>.messages.jsonl
    dev-1.prompt.md
    dev-1.out.md
    dev-1.exit
    dev-1-r1.prompt.md
    dev-1-r1.out.md
    dev-1-r1.exit
    review.prompt.md
    review.out.md
    review.exit
    review.diff
    review.status
```

稳定文档产物：

```text
docs/requirements-clarity.md
docs/planning.md
docs/output.md
```

文件说明：

- `state.json`：当前 run 的节点状态和详情。
- `*.messages.jsonl`：Agent Session 消息历史，包含通知、审批请求、审批确认和对话上下文。
- `*.prompt.md`：发送给 Agent 或 Codex CLI 的 prompt。
- `*.out.md`：Agent 或命令输出。
- `*.exit`：进程退出码。
- `agents.json`：Node 3 的 SubtaskAgent manifest。
- `review.diff`：Review 前的代码 diff 快照。
- `review.status`：Review 前的 git status 快照。

## 10. API reference

### Run

```go
func Run(ctx context.Context, repoRoot, userInput string) (runID string, err error)
```

执行完整三节点流程。该函数会在审批门阻塞，适合由后台 worker 调用。

### RunClarification

```go
func RunClarification(ctx context.Context, repoRoot, userInput string) (clarFile string, err error)
```

执行 Node 1，生成 `docs/requirements-clarity.md`。

### RunPlanning

```go
func RunPlanning(ctx context.Context, repoRoot, clarFile string) (plan *Plan, err error)
```

执行 Node 2，生成 `docs/planning.md` 并返回校验后的 `Plan`。

### RunDevelopment

```go
func RunDevelopment(ctx context.Context, repoRoot string, plan *Plan) (finalState *WorkflowState, err error)
```

执行 Node 3，创建子任务 Agent，运行开发子任务，执行 Review，生成 `docs/output.md`。

### BuildSubtaskAgents

```go
func BuildSubtaskAgents(repoRoot, nodeDir string, plan *Plan) ([]SubtaskAgent, error)
```

为每个 subtask 创建独立 `SubtaskAgent`。

### RouteSubtaskMessage

```go
func RouteSubtaskMessage(agents []SubtaskAgent, msg SubtaskMessage) (*SubtaskAgent, error)
```

将 Chatbot 消息路由到正确的子任务 Agent。

### AppendAgentMessage

```go
func AppendAgentMessage(agent *SubtaskAgent, msg AgentMessage) error
```

将消息追加到 Agent Session 的 JSONL history。

### EmitNodeNotification

```go
func EmitNodeNotification(sessionID string, notification NodeNotification) error
```

向指定 Agent Session 写入节点或子任务生命周期通知。

### WaitForApproval

```go
func WaitForApproval(ctx context.Context, sessionID, nodeName string) error
```

写入审批请求并阻塞，直到对应 approval 出现或 context 结束。

### ApproveSession

```go
func ApproveSession(sessionID, nodeName, message string) error
```

写入审批确认，使 `WaitForApproval` 继续。

### ParseReviewReport

```go
func ParseReviewReport(markdown string) *ReviewReport
```

解析 Review markdown，识别 blocking issue。

## P1: Lineage & Failure Attribution

Phase 1 adds data contracts for attempt lineage and failure attribution while keeping the workflow DAG unchanged. The execution order remains the current MVP flow; P1 records richer metadata for later rollback orchestration, but it does not add DAG back-edges or change the `WorkflowNode` interface.

### Attempt Lineage Paths

Attempt lineage is stored under each run directory:

```text
.workflow/runs/{runID}/{nodeID}/attempts.jsonl
.workflow/runs/{runID}/{nodeID}/attempts/{safeScope}.jsonl
.workflow/runs/{runID}/attempts.index.jsonl
```

The per-node `attempts.jsonl` is the node-local stream. The `attempts/{safeScope}.jsonl` file is a per-scope shard, where unsafe filename characters are normalized. The run-level `attempts.index.jsonl` is the global index across nodes.

Scope naming conventions:

- `clarification:{agentType}` for clarification agents, for example `clarification:codex`.
- `planning` for planning node agent attempts.
- `subtask:{subtaskID}` for development subtask attempts.
- `command:{testID}` for evaluation command attempts.

Completion evidence also recognizes `attempt_lineage` when `attempts.index.jsonl` exists. This is evidence indexing only; rollback decisions are handled by later phases.

### AttemptEvent Schema

`AttemptEvent` records one initial attempt or revision attempt. Attempt `0` is the initial run; attempts `1+` are revisions or retries. The current schema records:

```text
run_id
node_id
node_kind
scope
subtask_id
role
session_id
attempt
parent_attempt
trigger
failure_class
failure_reason
rejection_reason
retry_advice
prompt_path
output_path
exit_path
artifact_refs
prompt_delta
decision
next_action
outcome
started_at
finished_at
```

Phase 1 writers use best-effort lineage recording for clarification, planning, and development so a lineage write failure does not stop the core workflow. Phase 2 also records evaluation command attempts. `RecordAttemptEvent` is the shared wrapper that validates required fields and returns contextual errors; callers decide whether to treat those errors as fatal.

## P2: Rollback Runner & Budgeted Retries

Phase 2 keeps the default DAG unchanged:

```text
clarification -> planning -> development -> evaluation
```

No DAG back-edges are added. Instead, nodes that can handle local retry/revision work implement `RollbackCapableNode`, and the shared `RollbackRunner` decides whether a rollback action is still within budget.

### RollbackRunner

`RollbackRunner` is a pure decision helper. It does not own agent sessions and does not execute prompts or commands. Callers provide:

- `RollbackContext`: run/node identity, rollback unit, budget, and optional lineage store.
- `RollbackSpec`: node default unit and action routing for contract, human rejection, and product defect failures.
- `RollbackUnit`: the retry scope, maximum attempts, and session reuse policy.
- `RollbackSignal`: the classified failure and reason.

The runner returns a `RollbackDecision` with action, target scope, next attempt number, session reuse choice, and rationale. It can also record a decision event through an `AttemptLineageStore`, but the node remains responsible for actually appending prompts, rerunning commands, and writing normal attempt artifacts.

### Rollback Units

Current Phase 2 units are:

- Clarification: `clarification:{agentType}`, max 3 human-reject revisions, same session.
- Planning: `planning` for planning lineage and contract-error retry decisions, max 3 revisions, same session.
- Development: `subtask:{subtaskID}`, max 3 human-reject revisions, reuse on human reject.
- Evaluation: `command:{testID}`, max 2 command attempts, new session/no session reuse.

The runner budget counts retry/revision attempts for the target scope. Existing loops keep a local revision-count fallback for compatibility when old tests or ad hoc sessions do not have persisted lineage.

### Failure Routing

`NextActionForFailure` centralizes phase-status routing:

- product defects -> `retry_generator`
- environment or validator issues -> `fix_environment`
- contract errors -> `retry_report`
- missing evidence, human rejection, invalid input, or inconclusive results -> `ask_user`
- transient failures -> `retry_evaluation`
- user-blocked or unsafe/out-of-scope failures -> `blocked`
- no failure -> `finish`

### Failure Attribution Fields

P1 adds strongly typed failure and attribution data:

- `FailureClass` captures routing categories such as `human_reject`, `transient`, `missing_evidence`, `contract_error`, `product_defect`, `environment_block`, and `validator_issue`.
- `Subtask.PlannedFiles` (`planned_files`) records expected files for later subtask attribution.
- `TestResult.FailureClass` records the classified failure for a test command.
- `SubtaskFailure` records `SubtaskID`, `FailureClass`, reason, evidence refs, retry advice, and next action.
- `EvaluationResult.SubtaskResults` (`subtask_results`) records per-subtask evaluation attribution even though evaluation still runs at run scope.
- `ReviewReport.SubtaskReports` (`subtask_reports`) reserves a structured place for later per-subtask review aggregation.

`ArtifactRef` and `EvidenceRef` are deliberately separate. `ArtifactRef` points to workflow-produced files such as prompts, outputs, exits, logs, diffs, review reports, and attempt lineage. `EvidenceRef` points to verification evidence such as build logs, test logs, coverage, and screenshots. Their JSON contracts should not be merged or renamed.

### Completion Evidence Compatibility

Completion check keeps the existing `document` and `code_artifacts` evidence behavior and additionally recognizes:

- `coverage` for `coverage.out`, so P3 can add a coverage gate without completion misclassifying the file.
- `review_report` for legacy `review.out.md` and Phase 3 reports such as `review-api.out.md` and `review-aggregate.out.md`.
- `attempt_lineage` for `attempts.index.jsonl`.

Completion check does not enforce 100% line coverage. Coverage pass/fail remains a P3 evaluation gate concern.

## P3: Review Before Evaluation, Coverage Gate, and Attribution

Phase 3 changes the default DAG to:

```text
clarification -> planning -> development -> review -> evaluation
```

No DAG back-edges are added. Review and evaluation emit structured failures and attempt lineage; later orchestration decides whether development should be rerun.

Development node execution now uses the development-only path. The legacy `RunDevelopment` entry point remains for compatibility, but the default DAG expects review and evaluation to run as their own nodes.

### Review Sessions and Artifacts

Each development subtask gets an isolated review session, history file, artifact directory, and attempt scope:

```text
.workflow/runs/<run_id>/review/subtasks/<safe_subtask_id>/review-<safe_subtask_id>.prompt.md
.workflow/runs/<run_id>/review/subtasks/<safe_subtask_id>/review-<safe_subtask_id>.out.md
.workflow/runs/<run_id>/review/subtasks/<safe_subtask_id>/review-<safe_subtask_id>.exit
.workflow/runs/<run_id>/review/subtasks/<safe_subtask_id>/review-<safe_subtask_id>.messages.jsonl
```

Subtask review attempts use `scope="review:subtask:<subtask_id>"` and set `SubtaskID`.

After all subtask reviews complete, aggregate review runs under:

```text
.workflow/runs/<run_id>/review/aggregate/review-aggregate.prompt.md
.workflow/runs/<run_id>/review/aggregate/review-aggregate.out.md
.workflow/runs/<run_id>/review/aggregate/review-aggregate.exit
```

Aggregate attempts use `scope="review:aggregate"` and do not set `SubtaskID`. `MergeReviewReports` keeps `ReviewReport.SubtaskReports` and fails the final report if any subtask or aggregate report has a blocking issue.

Review contract errors are classified as `contract_error`. A malformed subtask review report gets at most one same-session report revision. Human rejection can also revise one subtask review or the aggregate review once, using `failure_class=human_reject`.

#### P3 design choice: review human-reject revision does not block

Unlike the development node (which calls `waitForSubtaskApprovalAt` to block until an approval or rejection appears), the review node does **not** wait for human input. `runReviewHumanRejectRevision` performs a one-shot revision only when a `rejection` decision already exists in the review session history at the moment the review agent finishes. With no prior rejection it leaves the report unchanged.

This is intentional for P3: review-time human gating (presenting the report, blocking on a human decision, and routing the rejection) is the responsibility of the P4 orchestrator, not the review node itself. The review node only owns the mechanical "revise once if a rejection is already recorded" path. The P4 orchestrator will drive the wait and write the rejection before re-entering review. See the "P3 to P4 handoff" section below.

### Evaluation Coverage Gate

Evaluation discovers changed files with `git diff --name-only` and `git diff --cached --name-only`. When changed files include Go code, it runs one run-level coverage command:

```bash
go test -cover -coverprofile=.workflow/runs/<run_id>/coverage.out ./...
go tool cover -func=.workflow/runs/<run_id>/coverage.out
```

The required total coverage is exactly `100.0%`. Lower coverage produces a blocking `product_defect` failure and a `coverage.out` evidence ref. Pure documentation or non-Go changes skip the coverage gate.

The coverage commands run in the nearest Go module directory at or below the git top-level: if the top-level itself has a `go.mod` it is used directly, otherwise the shallowest `go.mod` found beneath it is used as the working directory (e.g. `server/go.mod`). The `-coverprofile` is written with an absolute path so it always lands under `.workflow/runs/<run_id>/coverage.out` regardless of the module directory. Repositories with multiple sibling submodules below the top-level are not fully supported (only the shallowest module is covered).

#### Implicit contracts of changed-file detection and the coverage gate

Two implicit contracts govern evaluation's git interaction:

1. **`ChangedFiles` only inspects the working tree and index** (`git diff --name-only` plus `git diff --cached --name-only`). It does not look at commit history (no `HEAD~`/merge-base baseline). Development artifacts must therefore remain uncommitted in the working tree; if changes are committed, `ChangedFiles` returns empty and both the coverage gate and attribution become no-ops.
2. **The coverage gate depends on a working git environment.** When `ChangedFiles` fails (e.g. git is unavailable, or `repoRoot` is not the git top-level), evaluation records a blocking `coverage` test result classified as `environment_block` and marks the run failed. The git error is **not** silently downgraded to "no Go changes".

### Evaluation Attribution

Evaluation failure attribution uses the current plan when available:

1. Changed files matching exactly one `Subtask.PlannedFiles` entry attribute the failure to that subtask.
2. Otherwise, changed files under exactly one `Subtask.RepoPath` attribute the failure to that subtask.
3. Otherwise, an existing `TestResult.SubtaskID` is used as a fallback.
4. Ambiguous matches remain run-level with an empty subtask id.

### P3 to P4 handoff

Phase 3 deliberately leaves two parallel paths around review rollback that Phase 4 will converge:

- **Inline revision path (live in P3).** Review report revisions—both contract-error revision and the one-shot human-reject revision—run inline inside `RunSubtaskReview` / `RunAggregateReview`. This is the path actually exercised today.
- **`PrepareRollback` interface path (declared, not yet driven).** `reviewNode` implements `RollbackCapableNode` (`PrepareRollback` / `RollbackArtifacts`) with a compile-time assertion, mirroring the development/evaluation nodes. It has no production caller in P3; the inline path does not route through it. The same is true for the other rollback-capable nodes.

Phase 4 adds the executor side of this handoff, but it does not delete the P3 inline review path. The final P4 state is:

- `reviewNode.PrepareRollback` remains the declared `RollbackCapableNode` capability.
- `RunSubtaskReview` / `RunAggregateReview` still own the existing inline contract-error and one-shot human-reject revision behavior.
- `NodeExecutor` now calls `PrepareRollback` only after the runtime node implements `RollbackCapableNode` and `NodeSpec.Rollback` is non-zero/enabled.
- Executor rollback handling records a structured decision and handoff; it does not add extra review revisions on top of the inline path.

This is an intentional intermediate state. Review-time human gating still belongs to the run-level orchestrator, and P4 still does not route review/evaluation `product_defect` failures back to development by itself.

## P4: Node-Level Retry and Structured Handoff

Phase 4 adds current-node retry behavior to `NodeExecutor`. It keeps the Phase 3 DAG:

```text
clarification -> planning -> development -> review -> evaluation
```

No DAG back-edges are added. P4 does not implement a run-level orchestrator and does not automatically rerun upstream nodes. If review or evaluation finds a `product_defect`, the executor returns structured failure data for a future orchestrator or user; it does not jump back to development.

### Interface Isolation

`WorkflowNode` stays the minimal execution interface:

```go
type WorkflowNode interface {
    Kind() NodeKind
    Execute(ctx context.Context, req NodeRequest) (*NodeResult, error)
}
```

Nodes that can make rollback decisions opt into a separate interface:

```go
type RollbackCapableNode interface {
    WorkflowNode
    PrepareRollback(ctx context.Context, rctx RollbackContext, signal RollbackSignal) (*RollbackDecision, error)
    RollbackArtifacts(rctx RollbackContext) []ArtifactRef
}
```

This keeps ordinary workflow nodes from being forced to know about rollback. Capability is discovered at runtime by type assertion.

### Rollback Gate

Executor rollback is gated by two conditions:

1. The concrete node must implement `RollbackCapableNode`.
2. The node's `NodeSpec.Rollback` must be enabled. A zero-value `RollbackSpec` disables executor rollback even when the node type has the methods.

Only when both conditions are true does `NodeExecutor` call `PrepareRollback`. The rollback decision can populate `FailureClass`, `RetryAdvice`, `NextAction`, `RetryExhausted`, and `ArtifactRefs` on the final `NodeResult`.

### Two Retry Budgets

P4 has two distinct budgets:

- `RollbackSpec` controls node-internal unit attempts. Examples: `review:subtask:<id>`, `review:aggregate`, `subtask:<id>`, `planning`, or `command:<test_id>`. These attempts are decided by `RollbackRunner` and are scoped to the failed unit.
- `NodeSpec.MaxRetries` controls whole-node reruns by `NodeExecutor`. This reruns the current node only; it does not rerun completed upstream nodes.

`NodeSpec.RetryOn` decides which `FailureClass` values may consume whole-node retry budget. If the failure class is not listed, the executor returns the failure immediately. If node retry budget is exhausted, `RetryExhausted` is exposed on the result for UI/API/orchestrator consumption.

Default whole-node retry policy:

| Node | MaxRetries | RetryOn |
| --- | ---: | --- |
| clarification | 1 | `transient`, `missing_evidence`, `inconclusive` |
| planning | 2 | `transient`, `contract_error`, `missing_evidence`, `inconclusive` |
| development | 1 | `transient`, `validator_issue`, `missing_evidence` |
| review | 1 | `transient`, `contract_error`, `missing_evidence`, `inconclusive` |
| evaluation | 2 | `transient`, `validator_issue`, `missing_evidence`, `inconclusive` |

`product_defect` is intentionally absent from default `RetryOn`; executor retry cannot fix generator/development defects by looping the same review or evaluation node. `environment_block` is also not retried by default and maps to `fix_environment`.

### Structured Node Results

`NodeResult` now carries machine-readable failure handoff fields in addition to legacy `Error`, `BlockedItems`, and `OutputPaths`:

- `FailureClass`
- `RetryAdvice`
- `NextAction`
- `RetryExhausted`
- `ArtifactRefs`
- `AttemptCount`

Nodes fill business-specific reason, advice, and artifact refs when they know them. `NodeExecutor` fills runtime fields such as attempt count, default failure class, retry exhaustion, and default next action when fields are empty.

The executor writes these fields into state details and writes `PhaseStatus` for exhausted failures when a status writer is available. This is the P4 handoff point for later orchestration: the next component can inspect structured state instead of scraping text errors.

## 11. CLI 使用方法

构建 CLI。由于仓库根目录已有 `workflow/` 目录，建议显式指定输出文件：

```bash
cd /root/repos/ming-agents/server
go build -o /tmp/ming-workflow ./cmd/workflow
```

使用内联输入：

```bash
cd /root/repos/ming-agents/server
/tmp/ming-workflow "实现一个新的工作流功能"
```

使用文件输入：

```bash
cd /root/repos/ming-agents/server
/tmp/ming-workflow -input /path/to/request.md
```

CLI 使用当前 `PWD` 作为 `repoRoot`，因此应从要被 Agent 修改的仓库根目录运行。

注意：当前 workflow 带审批门。直接运行 CLI 时，如果没有外部 Chatbot 或脚本写入 approval，流程会在审批点等待，直到 context 结束或收到审批消息。

## 12. 环境依赖

运行 Workflow MVP 需要宿主机具备以下命令，并完成必要鉴权：

- `codex`
- `claude`
- `git`

Node 1 使用 Codex 和 Claude Code 的交互式 session manager。Node 2 使用 Codex 生成规划。Node 3 使用 `codex exec` 执行开发子任务，并使用 `git diff`、`git status` 构造 Review 输入。

## 13. 验证命令

常用验证命令：

```bash
cd /root/repos/ming-agents/server
go test -count=1 ./workflow/...
go build ./workflow/...
go vet ./workflow/...
go build -o /tmp/ming-workflow ./cmd/workflow
```

这些命令分别验证：

- 单元测试和行为测试。
- workflow 包编译。
- Go 静态检查。
- CLI 入口编译。
