# Node 审批拒绝与回退机制设计

## 1. 背景

当前 Workflow MVP 已经具备人工审批门：`WaitForApproval` 写入 `role:"approval_request"` 并阻塞，`ApproveSession` 写入 `role:"approval"` 后 workflow 继续执行。但这个模型只有“通过”路径，没有“拒绝、修订、回退、重试”的协议和状态机。

这会带来几个问题：

- Node 1 需求澄清不准确时，用户只能批准继续，不能要求重新澄清。
- Node 2 规划不合理时，用户不能要求重拆任务或调整子任务。
- Node 3 子任务完成后，如果结果不满足预期，用户不能把修订指令回传给对应 Subtask Agent 并让该子任务重跑。
- Review 阶段发现问题时，缺少标准化方式回到指定子任务或 Review 前状态。

本设计目标是在现有文件化 Agent Session 和 Chatbot 编排模型之上，增加拒绝决策、修订指令下发、回退恢复点和 workflow 状态可见性。

## 2. 设计目标

人类用户，或代表用户的 Chatbot，可以在任意审批点做出两类决策：

- 通过：允许 workflow 从当前审批点继续。
- 拒绝：阻止继续，附带原因、回退类型、恢复点和修订指令。

拒绝后系统必须能够：

1. 将拒绝决策写入对应 Agent Session。
2. 将用户修订指令下发给目标 Node Agent 或 Subtask Agent。
3. 在 `state.json` 中记录当前处于等待修订或修订执行中。
4. 根据拒绝类型选择回退路径。
5. 从指定 `ResumePoint` 重新执行或继续执行。

非目标：

- 不在本设计中实现复杂分支 DAG。
- 不引入数据库。
- 不设计完整权限系统。
- 不要求 Chatbot 自己执行开发，只要求它写入消息和调用 workflow API。

## 3. ReviewDecision struct

新增结构：

```go
type ReviewDecision struct {
    Approved    bool   `json:"approved"`
    Reason      string `json:"reason"`
    RejectType  string `json:"reject_type"`  // "replan", "resubtask", "revise_subtask"
    ResumePoint string `json:"resume_point"` // "node1", "node2", "node3:subtask-2", "node3:review"
    SessionID   string `json:"session_id"`
    NodeName    string `json:"node_name"`
    Timestamp   string `json:"timestamp"`
}
```

字段语义：

- `Approved`：是否通过。拒绝时必须为 `false`。
- `Reason`：用户或 Chatbot 给出的拒绝原因，应可被目标 Agent 直接理解。
- `RejectType`：拒绝类型，用于 workflow 选择回退策略。
- `ResumePoint`：恢复点，指定 workflow 应从哪里继续或重试。
- `SessionID`：审批点对应的 Agent Session ID。
- `NodeName`：审批点名称，例如 `node1`、`node2`、`subtask:api`。
- `Timestamp`：决策写入时间，使用 RFC3339。

建议拒绝类型常量：

```go
const (
    RejectTypeReplan        = "replan"
    RejectTypeResubtask     = "resubtask"
    RejectTypeReviseSubtask = "revise_subtask"
)
```

建议消息 role：

```text
rejection
```

拒绝消息写入 Agent Session 时，`AgentMessage.Content` 为 `ReviewDecision` 的 JSON。

示例：

```json
{
  "role": "rejection",
  "content": "{\"approved\":false,\"reason\":\"需求里遗漏了灰度发布要求\",\"reject_type\":\"replan\",\"resume_point\":\"node1\",\"session_id\":\"pty-20260624-153000-node1-workflow-0\",\"node_name\":\"node1\",\"timestamp\":\"2026-06-24T15:30:00+08:00\"}",
  "timestamp": "2026-06-24T15:30:00+08:00"
}
```

## 4. RejectSession 函数签名与行为

建议新增函数：

```go
func RejectSession(sessionID, nodeName string, decision ReviewDecision) error
```

也可以提供更便于 Chatbot 调用的参数化版本：

```go
func RejectSession(ctx context.Context, sessionID, nodeName, rejectType, resumePoint, reason string) error
```

推荐 MVP 使用第一种，便于测试和协议稳定；第二种可作为 helper。

### 行为

`RejectSession` 的职责：

1. 校验 `sessionID` 是否已注册。
2. 校验 `nodeName` 非空。
3. 校验 `decision.Approved == false`。
4. 校验 `RejectType` 合法。
5. 校验 `ResumePoint` 与 `RejectType` 匹配。
6. 补齐 `SessionID`、`NodeName`、`Timestamp`。
7. 将 `role:"rejection"` 消息追加到对应 Agent Session 的 `.messages.jsonl`。

伪代码：

```go
func RejectSession(sessionID, nodeName string, decision ReviewDecision) error {
    session, err := registeredAgentSession(sessionID)
    if err != nil {
        return err
    }
    if nodeName == "" {
        return errors.New("nodeName is required")
    }
    decision.Approved = false
    decision.SessionID = sessionID
    decision.NodeName = nodeName
    if decision.Timestamp == "" {
        decision.Timestamp = time.Now().Format(time.RFC3339)
    }
    if err := validateReviewDecision(decision); err != nil {
        return err
    }
    data, _ := json.Marshal(decision)
    return AppendAgentMessage(&SubtaskAgent{Session: session}, AgentMessage{
        Role:    "rejection",
        Content: string(data),
    })
}
```

### 与 WaitForApproval 的关系

当前 `WaitForApproval` 只检测 approval。加入拒绝后应改为检测“审批决策”：

```go
decision, err := WaitForApproval(ctx, sessionID, nodeName)
```

但为了兼容现有 API，可以保留当前签名：

```go
func WaitForApproval(ctx context.Context, sessionID, nodeName string) error
```

并约定：

- 检测到 `approval`：返回 nil。
- 检测到 `rejection`：返回 `ErrApprovalRejected`，错误中携带或可查询 `ReviewDecision`。

更推荐长期演进为：

```go
func WaitForReviewDecision(ctx context.Context, sessionID, nodeName string) (ReviewDecision, error)
```

这样 workflow 可以直接根据 `ReviewDecision` 选择回退路径。

## 5. Workflow 感知拒绝并选择回退路径

### state.json 新字段

当前 `RunState`：

```go
type RunState struct {
    RunID   string                `json:"run_id"`
    Nodes   map[string]NodeStatus `json:"nodes"`
    Details map[string]any        `json:"details,omitempty"`
}
```

建议在 `Details` 中新增标准字段，避免立即破坏结构：

```json
{
  "run_id": "20260624-153000",
  "nodes": {
    "node1": "COMPLETED",
    "node2": "WAITING_REVISION",
    "node3": "PENDING"
  },
  "details": {
    "approval": {
      "current_session_id": "pty-...",
      "current_node_name": "node2",
      "waiting_since": "2026-06-24T15:30:00+08:00"
    },
    "last_decision": {
      "approved": false,
      "reason": "子任务拆分不合理，需要合并 API 和 store 修改",
      "reject_type": "resubtask",
      "resume_point": "node2",
      "session_id": "pty-...",
      "node_name": "node2",
      "timestamp": "2026-06-24T15:31:00+08:00"
    },
    "resume_point": "node2",
    "revision_attempt": 1
  }
}
```

后续如果需要强类型化，可扩展：

```go
type RunState struct {
    RunID        string                 `json:"run_id"`
    Nodes        map[string]NodeStatus  `json:"nodes"`
    Details      map[string]any         `json:"details,omitempty"`
    LastDecision *ReviewDecision        `json:"last_decision,omitempty"`
    ResumePoint  string                 `json:"resume_point,omitempty"`
}
```

MVP 建议先用 `Details`，降低迁移成本。

### 回退状态机

审批点和回退行为：

| 审批点 | RejectType | ResumePoint | 回退行为 |
| --- | --- | --- | --- |
| Node1 完成 | `replan` | `node1` | 将修订指令追加到 Node1 session，重新执行 Node1，生成新版 `requirements-clarity.md` |
| Node2 完成 | `resubtask` | `node2` | 将修订指令追加到 Node2 session，重新执行 Node2，生成新版 `planning.md` |
| Subtask 完成 | `revise_subtask` | `node3:<subtask_id>` | 将修订指令追加到目标 SubtaskAgent，重新执行该 subtask |
| Review 完成 | `revise_subtask` | `node3:review` 或 `node3:<subtask_id>` | 根据 Review issue 或用户指定目标，回到最后一个或指定 subtask 重跑 |

### Workflow 调度策略

建议将 `Run` 的线性调用改造成显式状态机：

```text
node1 -> approve/reject
  approve -> node2
  reject(replan) -> node1

node2 -> approve/reject
  approve -> node3
  reject(resubtask) -> node2

node3 subtask[i] -> approve/reject
  approve -> subtask[i+1] or review
  reject(revise_subtask) -> subtask[i]

review -> pass/fail or approve/reject
  pass -> output
  reject(revise_subtask) -> target subtask
```

为避免无限循环，必须引入重试上限：

```go
const maxRevisionAttemptsPerPoint = 3
```

每个 `ResumePoint` 独立计数。

## 6. Chatbot → SubtaskAgent 下发完整流程

Chatbot 是拒绝与修订指令的入口。完整流程如下：

```text
用户查看审批请求
  |
  v
用户拒绝并输入修订原因
  |
  v
Chatbot 识别目标节点 / 子任务
  |
  v
RouteSubtaskMessage 或定位节点 Agent Session
  |
  v
AppendAgentMessage(role:"user", content:"修订指令")
  |
  v
RejectSession(role:"rejection", ReviewDecision)
  |
  v
Workflow 检测 rejection
  |
  v
更新 state.json: WAITING_REVISION / REVISION_IN_PROGRESS
  |
  v
按 ResumePoint 重跑对应节点或子任务
```

### P1 lineage 视角

Phase 1 makes rejection decisions visible to later rollback orchestration by writing attempt lineage:

- Clarification rejection writes a `clarification` attempt event with `scope=agent:{agentType}`.
- Planning rejection writes a `planning` attempt event with `scope=node-agent`.
- Development subtask rejection writes a `development` attempt event with `scope=subtask:{subtaskID}`.

Each rejection attempt records `failure_class=human_reject`, `rejection_reason`, prompt/output/exit paths when available, and appears in both the node-local `attempts.jsonl` and run-level `attempts.index.jsonl`. Later orchestrator work can read this lineage to decide rollback targets without scraping session history.

P1 lineage writes are best-effort: workflow execution should continue if `RecordAttemptEvent` cannot persist an event. P2 introduces the rollback runner and budgeted rollback decisions; that runner may choose stricter error handling for core rollback state, but P1 keeps lineage as an observable side channel rather than a hard dependency.

### 针对节点 Agent

Node1 或 Node2 的拒绝不走 `RouteSubtaskMessage`，因为它们不是 SubtaskAgent，而是 workflow-level node session。

Chatbot 应直接定位：

```text
.workflow/runs/<run_id>/node1/node1.messages.jsonl
.workflow/runs/<run_id>/node2/node2.messages.jsonl
```

然后写入：

```json
{
  "role": "user",
  "content": "请补充灰度发布、回滚策略和权限边界。",
  "timestamp": "..."
}
```

再调用：

```go
RejectSession(nodeSessionID, "node1", ReviewDecision{
    Approved:    false,
    Reason:      "需求澄清不完整",
    RejectType:  "replan",
    ResumePoint: "node1",
})
```

### 针对子任务 Agent

Chatbot 读取：

```text
.workflow/runs/<run_id>/node3/agents.json
```

然后路由：

```go
agent, err := RouteSubtaskMessage(agents, SubtaskMessage{
    SubtaskID: "subtask-2",
    Content:   userMessage,
})
```

下发修订指令：

```go
AppendAgentMessage(agent, AgentMessage{
    Role:    "user",
    Content: "接口实现缺少失败重试，请补充并更新测试。",
})
```

写入拒绝：

```go
RejectSession(agent.Session.ID, "subtask:subtask-2", ReviewDecision{
    Approved:    false,
    Reason:      "缺少失败重试和测试",
    RejectType:  "revise_subtask",
    ResumePoint: "node3:subtask-2",
})
```

## 7. 生命周期状态扩展

当前 `NodeStatus` 包含：

```go
PENDING
RUNNING
WAITING_REVIEW
COMPLETED
FAILED
```

建议扩展：

```go
const (
    NodeWaitingApproval   NodeStatus = "WAITING_APPROVAL"
    NodeWaitingRevision   NodeStatus = "WAITING_REVISION"
    NodeRevisionInProgress NodeStatus = "REVISION_IN_PROGRESS"
)
```

状态含义：

- `WAITING_APPROVAL`：节点或子任务已完成，正在等待用户审批。
- `WAITING_REVISION`：审批被拒绝，已收到拒绝决策，等待修订指令或等待进入重试。
- `REVISION_IN_PROGRESS`：对应节点或子任务正在根据修订指令重跑。

推荐状态流：

```text
RUNNING
  -> COMPLETED
  -> WAITING_APPROVAL
  -> approval  -> next node
  -> rejection -> WAITING_REVISION
  -> retry     -> REVISION_IN_PROGRESS
  -> COMPLETED
```

对于子任务，可以在 `RunState.Details` 中记录：

```json
{
  "subtasks": {
    "subtask-2": {
      "status": "WAITING_REVISION",
      "session_id": "pty-...",
      "revision_attempt": 1,
      "resume_point": "node3:subtask-2"
    }
  }
}
```

## 8. API 变更清单

### 新增类型

```go
type ReviewDecision struct {
    Approved    bool   `json:"approved"`
    Reason      string `json:"reason"`
    RejectType  string `json:"reject_type"`
    ResumePoint string `json:"resume_point"`
    SessionID   string `json:"session_id"`
    NodeName    string `json:"node_name"`
    Timestamp   string `json:"timestamp"`
}
```

### 新增函数

```go
func RejectSession(sessionID, nodeName string, decision ReviewDecision) error
```

推荐后续新增：

```go
func WaitForReviewDecision(ctx context.Context, sessionID, nodeName string) (ReviewDecision, error)
func LoadSubtaskAgents(path string) ([]SubtaskAgent, error)
func WriteRunDecision(repoRoot, runID string, decision ReviewDecision) error
func ResumeFromDecision(ctx context.Context, repoRoot string, decision ReviewDecision) error
```

### 修改函数

当前：

```go
func WaitForApproval(ctx context.Context, sessionID, nodeName string) error
```

建议保留兼容，但内部改为：

- 识别 `approval`。
- 识别 `rejection`。
- approval 返回 nil。
- rejection 返回可识别错误，例如 `ErrApprovalRejected`。

长期建议替换为：

```go
func WaitForReviewDecision(ctx context.Context, sessionID, nodeName string) (ReviewDecision, error)
```

### Chatbot API

如果未来给 HTTP API 暴露审批能力，建议新增：

```text
POST /workflow/runs/{run_id}/sessions/{session_id}/approve
POST /workflow/runs/{run_id}/sessions/{session_id}/reject
POST /workflow/runs/{run_id}/agents/route-message
```

`reject` 请求体：

```json
{
  "node_name": "subtask:subtask-2",
  "reason": "缺少失败重试",
  "reject_type": "revise_subtask",
  "resume_point": "node3:subtask-2",
  "message": "请补充失败重试和对应测试"
}
```

## 9. 边界情况处理

### 重复审批

同一个 `approval_request` 可能收到多个 approval 或 rejection。处理策略：

- 以第一条有效决策为准。
- 后续决策写入历史，但不改变 workflow 状态。
- `state.json.details.last_decision` 记录实际生效的决策。

### 同时出现 approval 和 rejection

按时间顺序读取 JSONL：

- 第一条匹配 `nodeName` 的有效决策生效。
- 如果 timestamp 缺失，以文件顺序为准。

### RejectType 与 ResumePoint 不匹配

应返回校验错误，不写入 rejection：

- `replan` 只能回到 `node1`。
- `resubtask` 通常只能回到 `node2`。
- `revise_subtask` 必须回到 `node3:<subtask_id>` 或 `node3:review`。

### 找不到 Session

`RejectSession` 返回错误：

```text
agent session "<id>" is not registered
```

Chatbot 应重新加载 `agents.json` 或节点 session 信息。

### 找不到 SubtaskAgent

`RouteSubtaskMessage` 返回错误。Chatbot 应要求用户明确目标子任务。

### 修订次数过多

每个 `ResumePoint` 应有最大重试次数。超过后：

- workflow 标记为 `FAILED`。
- `state.json.details.failure_reason` 写入原因。
- Chatbot 提示用户需要人工介入。

### 用户拒绝但没有指令

允许写入 rejection，但状态保持 `WAITING_REVISION`。Chatbot 应继续追问：

```text
请说明希望如何修改，或指定要回退到哪个子任务。
```

### Review 阶段拒绝但未指定子任务

默认回退到最后一个失败或最后一个执行过的 subtask。更推荐由 Chatbot 结合 Review issue 自动选择目标，并在消息中向用户确认。

### Workflow 进程重启

因为决策和消息都在 JSONL 中，重启后可以恢复：

1. 读取 `state.json`。
2. 读取 `last_decision` 和 `resume_point`。
3. 读取对应 Agent Session history。
4. 从 `ResumePoint` 重新进入状态机。

### 文件损坏或 JSONL 部分写入

读取 JSONL 时应逐行解析：

- 单行解析失败时跳过并记录 warning。
- 不应因为一行损坏导致整个 run 无法恢复。

### 安全边界

用户修订指令会进入 Agent prompt 上下文，因此需要保留现有约束：

- 不允许 `repo_path` 逃逸仓库根目录。
- 不允许 rejection 修改 `session_id` 指向无关 run。
- Chatbot 写入文件时应只写当前 run 的 session history。

## 10. 推荐落地顺序

1. 增加 `ReviewDecision` 和 `RejectSession`。
2. 将 `WaitForApproval` 内部升级为同时识别 approval/rejection。
3. 在 `state.json.details` 中记录 `last_decision`、`resume_point`、`revision_attempt`。
4. Node1 拒绝后支持重新执行 Node1。
5. Node2 拒绝后支持重新执行 Node2 并覆盖 planning。
6. Subtask 拒绝后支持追加 user 指令并重跑该 subtask。
7. Review 拒绝后支持回到指定 subtask。
8. 为重复决策、非法 ResumePoint、重试上限补测试。
