## 8. 任务状态通知机制（已实现）

### 8.1 整体架构

当 Workflow 某个 Step 状态发生变化（如 `running` → `waiting_user_input`），后端主动通过 **SSE（Server-Sent Events）** 将事件推送给前端，前端展示 Toast 通知用户。

```
┌──────────────┐     SSE /runs/:run_id/events      ┌──────────────────┐
│   Go Backend │ ────────────────────────────────→ │  React Frontend │
│  (sse.go)    │  StepStatusChange Event            │  (useRunSSE)    │
└──────────────┘                                     └────────┬────────┘
                                                               │
                                                         Toast 通知
```

### 8.2 后端实现

**文件：`server/api/sse.go`**

```go
// SSEManager 管理所有 SSE 客户端连接
type SSEManager struct {
    clients map[string]chan []byte  // run_id → event channel
    mu      sync.RWMutex
}

// Broadcast 向指定 run 的所有订阅者广播事件
func (m *SSEManager) Broadcast(runID string, event []byte)

// 添加客户端：GET /runs/:run_id/events
// 1. 创建 channel
// 2. 将 channel 注册到 clients[runID]
// 3. 返回 HTTP 分块响应流
// 4. 客户端断开时移除 channel
```

**端点：`GET /runs/:run_id/events`**

- 返回 HTTP 响应流（`Transfer-Encoding: chunked`）
- 每个事件格式：`data: {"type":"step_status_change","run_id":"...","step_id":1,"new_status":"waiting_user_input"}\n\n`
- 客户端可通过 `EventSource` API 直接订阅

**事件类型：`StepStatusChange`**

```json
{
  "type": "step_status_change",
  "run_id": "550e8400-e29b-41d4-a716-446655440000",
  "node_id": "node-1",
  "step_id": 1,
  "step_name": "步骤1",
  "old_status": "running",
  "new_status": "waiting_user_input",
  "timestamp": "2026-06-23T19:00:00Z"
}
```

**文件：`server/store/notifications.go`**

```go
// StepStatusNotifier 状态变更通知接口
type StepStatusNotifier interface {
    NotifyStepStatusChange(runID, nodeID string, step *Step, oldStatus, newStatus StepStatus)
}

// 触发点：server/store/store.go
// - UpdateStep() 末尾调用 notifier.NotifyStepStatusChange()
// - UpdateStepStatus() 末尾调用 notifier.NotifyStepStatusChange()
```

### 8.3 前端实现

**文件：`frontend/src/hooks/useRunStatusSSE.ts`**

```typescript
export function useRunStatusSSE(runId: string) {
  // 创建 EventSource 连接到 /api/runs/:run_id/events
  // 解析 data:text/event-stream
  // 按 type 分发事件
  // StepStatusChange → 返回 { runId, stepId, stepName, oldStatus, newStatus }
}
```

**文件：`frontend/src/components/NotificationToast.tsx`**

```typescript
// Toast 组件，3 秒自动消失
// props: { message: string, type: 'info'|'success'|'warning', visible: boolean }
// onClose: 关闭回调
```

**触发位置：`frontend/src/pages/WorkflowTaskPage.tsx`**

- 在 Step 状态变为 `waiting_user_input` 时显示 Toast
- `新状态：等待用户输入` 提示用户点击对话窗口补充信息

### 8.4 API 汇总

| 端点 | 方法 | 说明 |
|------|------|------|
| `/runs/:run_id/events` | GET | SSE 事件流订阅 |
| `/runs` | GET | 查询所有 run（返回 run ID、name、status、current_node_name） |

---

## 9. 常驻 Agent 对话窗口（已实现）

### 9.1 整体架构

常驻 Agent 窗口是一个独立的页面，提供与 pi-agent-core 的持续对话能力，同时展示所有 Workflow run 的全局状态列表。

```
┌─────────────────────────────────────────────────────────┐
│              PermanentAgentPage                          │
│  ┌─────────────────┐  ┌─────────────────────────────┐  │
│  │  全局 Run 列表   │  │      Agent 对话区域          │  │
│  │  useGlobalRuns  │  │   pi-agent-core.ts           │  │
│  │  - Run #1 ✅    │  │   - sendMessage()            │  │
│  │  - Run #2 ⏳   │  │   - 状态: waiting_user_input │  │
│  │  - Run #3 ✅    │  │   - 支持补充用户输入          │  │
│  └─────────────────┘  └─────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

### 9.2 全局 Run 列表

**文件：`frontend/src/hooks/useGlobalRuns.ts`**

```typescript
export interface RunInfo {
  id: string
  name: string
  status: string  // 'running' | 'waiting_user_input' | 'completed' | 'failed'
  current_node_name: string
  created_at: string
}

// GET /api/runs → { runs: RunInfo[] }
// 轮询间隔：5s
```

**API 响应：`GET /api/runs`**

```json
{
  "runs": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "name": "Workflow #1",
      "status": "completed",
      "current_node_name": "结束",
      "created_at": "2026-06-23T18:00:00Z"
    },
    {
      "id": "660e8400-e29b-41d4-a716-446655440001",
      "name": "Workflow #2",
      "status": "waiting_user_input",
      "current_node_name": "用户确认节点",
      "created_at": "2026-06-23T19:00:00Z"
    }
  ]
}
```

### 9.3 pi-agent-core 对话能力

**文件：`frontend/src/lib/pi-agent-core.ts`**

```typescript
// pi-agent-core SDK 封装
class PiAgentCore {
  // 发送消息，返回流式响应
  async sendMessage(sessionId: string, message: string): Promise<string>

  // 获取当前状态
  getState(sessionId: string): AgentState

  // 补充用户输入（用于 WAITING_USER_INPUT 状态后）
  async provideInput(runId: string, stepId: number, input: string): Promise<void>
}

// 状态类型
type AgentState = 'idle' | 'running' | 'waiting_user_input' | 'completed' | 'error'
```

### 9.4 页面路由

- 常驻 Agent 页面：`/agent` 路由
- Workflow 任务详情：`/workflow/:runId` 路由
- 两个页面共享全局状态（通过 React Context）

### 9.5 BFF 层

**文件：`frontend/bff/server.js`**

```javascript
// 代理配置
const PROXY_ROUTES = {
  '/api/runs': 'http://localhost:8080/runs',
  '/api/runs/:runId/events': 'http://localhost:8080/runs/:runId/events',
}
```

---

## 10. BFF 代理层（已实现）

### 10.1 职责

BFF（Backend for Frontend）作为前端与后端之间的轻量代理层：
- 代理 REST API 和 SSE 请求到 Go 后端
- 代理 LLM 流式调用到 Minimax API
- 统一鉴权和错误处理

### 10.2 代理配置

| 前端路径 | 后端路径 | 说明 |
|---------|---------|------|
| `/api/runs` | `http://localhost:8080/runs` | 查询所有 run |
| `/api/runs/:run_id/events` | `http://localhost:8080/runs/:run_id/events` | SSE 事件流 |
| `/api/runs/:run_id/steps/:step_id/input` | `http://localhost:8080/runs/:run_id/steps/:step_id/input` | 补充用户输入 |

### 10.3 LLM 调用

**Minimax API**

```
POST https://api.minimax.chat/v1/text/chatcompletion_v2
Headers:
  Authorization: Bearer $MINIMAX_API_KEY
  Content-Type: application/json
Body:
  {
    "model": "Minimax-01",
    "messages": [...],
    "stream": true
  }
```

前端 → BFF → Minimax（流式响应）

---

## 11. 后续规划

- [ ] 支持 WebSocket 双向通信（替代 SSE）
- [ ] 飞书消息通知（当 Run 完成或失败时主动推送）
- [ ] pi-agent-core 多 Agent 协作
- [ ] 持久化对话历史（对话窗口内容存储）
