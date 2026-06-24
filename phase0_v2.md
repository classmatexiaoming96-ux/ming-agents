## Phase 0 用户交互层 — 功能实现总结

### 1. 背景与目标

ming-agents 工作流引擎目前缺少人工介入机制：节点完成后无法暂停等待确认，用户无法对节点输出提供自然语言反馈。为实现「人机协作」评审能力，Phase 0 引入用户交互层。

### 2. 核心设计

**交互模型**：节点完成 → 进入 WAITING_USER_INPUT 状态 → pi-agent 前端展示产物 + 对话 → 用户 confirm/retry/反馈 → 引擎继续或重跑

**职责划分**：
- pi-agent：前端意图识别（理解用户是想确认还是修改，调用对应后端 API）
- ming-agents 后端：维护工作流状态，提供 REST API
- BFF：代理 MiniMax LLM 请求

### 3. 后端新增能力

**步骤状态机**：domain/step.go 新增 StepStatusWaitingUserInput = waiting_user_input

**REST API**（server/api/server.go）：
- GET /runs/:run_id/status — 返回当前运行状态和所有节点列表
- GET /runs/:run_id/nodes/:node/artifact — 返回该节点的最新产物（类型、内容、格式）
- POST /runs/:run_id/nodes/:node/feedback — 接收用户反馈文本，写入节点历史记录
- POST /runs/:run_id/nodes/:node/approve — 用户确认节点，状态从 waiting_user_input 推进到 completed
- POST /runs/:run_id/nodes/:node/retry — 用户要求重跑，清除节点状态并触发重新执行
- GET /runs/:run_id/nodes/:node/history — 返回该节点所有历史记录（每次 feedback/approve/retry 的时间、内容、结果）

### 4. 前端交互层

**页面结构**：
- WorkflowStatusBar — 顶部状态栏，显示运行 ID、当前节点、整体进度
- ArtifactPanel — 左侧面板，展示选中节点的产物内容（代码高亮、Markdown 渲染、图片预览）
- ConversationPanel — 右侧/中间对话面板，用户和 pi-agent 在此对话

**交互流程**：
1. 节点进入 waiting_user_input，前端切换到交互模式（工作流暂停）
2. pi-agent 分析节点产物，生成摘要主动呈现给用户
3. 用户可以在对话中提问产物细节，或提出修改意见
4. pi-agent 调用 approve（用户确认）或 retry（用户要求修改）+ feedback（携带修改意见）
5. 后端处理后，工作流继续或重跑，前端刷新状态

### 5. BFF 层

Express 服务（frontend/bff/server.js），路由 /api/llm/stream：
- 接收前端 pi-agent 的流式 LLM 请求
- 附加 MINIMAX_API_KEY（不暴露给浏览器）
- 代理到 https://api.minimax.chat/v1/text/chatcompletion_v2
- 流式返回模型输出给前端

### 6. 数据流

用户输入 → pi-agent（前端意图识别） → 后端 API（feedback/approve/retry）
                                         ↓
                                   需要 LLM 推理
                                         ↓
                              BFF → MiniMax → 流式返回

节点产物 ← GET /runs/:run_id/nodes/:node/artifact ← 前端 ArtifactPanel

### 7. Commit 信息
- Commit: 2617060
- 27 files changed, +2447 -5 lines
- Phase 0 MVP 完成
