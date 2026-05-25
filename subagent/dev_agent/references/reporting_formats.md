# Dev-Agent 汇报格式

## 正常进度汇报

```
tag: autopilot
line: dev-line
node: dev-nK
goal_status: partial
next_role: orchestrator

## Dev-Agent 进度汇报

### 当前 Task
- TASK-003：实现 User.List 接口

### Increment 进度
- TASK-003 / INCR-003-1 ✅ done | commit: abc1234 | reviewer: passed
- TASK-003 / INCR-003-2 🔄 in_progress | cc_lead: session-uuid
- TASK-003 / INCR-003-3 ⏳ pending | depends_on: [INCR-003-2]

### 已完成 sub-tasks
- TASK-001：User.Create 接口 ✅
- TASK-002：User.Get 接口 ✅

### Changed files
- internal/user/handler.go（修改）

### 进度
- 3/12 sub-tasks 完成（25%）
- 7/18 increments 完成

### Risk
- TASK-005 依赖外部 API，可能有集成风险
```

---

## 需要用户决策

```
tag: need_user
line: dev-line
node: dev-nK
goal_status: waiting
next_role: orchestrator

## 需要用户决策

### 问题
TASK-006 涉及第三方支付集成，有两种实现路径：

**路径A**：使用 Stripe SDK
**路径B**：自建支付模块

### CC Lead 评估
倾向于路径A，但需要您最终决策。

### questions_for_user
{
  "type": "decision",
  "task_id": "TASK-006",
  "question": "TASK-006 支付集成方案，请选择",
  "options": ["A: 路径A（Stripe SDK）", "B: 路径B（自建）"]
}
```

---

## 阻塞

```
tag: blocked
line: dev-line
node: dev-nK
goal_status: blocked
next_role: orchestrator

## 阻塞报告

### 问题
TASK-005 遇到无法解决的技术障碍：上游服务接口返回数据结构与文档不符。

### 已尝试
1. 重试 3 次，错误相同
2. 降级处理，绕过该字段，测试仍失败

### INCR rollback 记录
- INCR-005-1: git revert abc1234 ✅
- INCR-005-2: git revert def5678 ✅

### blockers
{
  "type": "technical_blocker",
  "task_id": "TASK-005",
  "description": "上游接口数据不一致",
  "impact": "阻塞 TASK-005 及后续依赖 TASK-005 的所有 Task"
}
```

---

## dev-submit：提交与 Gate 2 验证

所有 Task 完成后执行。

**执行**：
1. 确保所有代码已 commit 到 feature 分支
2. 运行最终编译检查
3. 生成 `docs/test_results.md`（测试结果汇总）
4. 汇报 Orchestrator 请求验证 Gate 2

**返回（dev-submit）**：
```
tag: done
line: dev-line
node: dev-submit
goal_status: complete
next_role: orchestrator
outputs:
  commits: [commit-id-1, commit-id-2, ...]
  test_results: docs/test_results.md
  total_tasks_completed: 12
  total_increments_completed: 18
  total_files_changed: 45
  overall_coverage: 82.5
cc_lead_sessions: [uuid-1, uuid-2, ..., uuid-N]
questions_for_user: []
```

---

## TASK_BOARD 更新（v5.0 新增 increment 追踪）

```markdown
## TASK_BOARD

### DEV_PHASE

#### sub_task: TASK-001-2
- **status**: in_progress
- **increments**:
  - INCR-001-2-1 ✅ done | commit: abc1234 | reviewer: passed | ci: mr.yaml PASS
  - INCR-001-2-2 🔄 in_progress | cc_lead: session-uuid | ci: pending
  - INCR-001-2-3 ⏳ pending | depends_on: [INCR-001-2-2]
  - INCR-001-2-4 ⏳ pending | depends_on: [INCR-001-2-2, INCR-001-2-3]
- **progress**: 1/4 increments done
```

---

## CI Pipeline 状态汇报

### CI 触发

```
tag: autopilot
line: dev-line
node: dev-nK
goal_status: partial
next_role: orchestrator

## CI Pipeline 状态

### INCR-001-2-2
- **CC Lead**: ✅ done | commit: abc1234
- **CI Pipeline**: 🔄 triggered | pipeline: mr.yaml
- **CI Jobs**: test 🔄 | lint ✅ | codecov ✅ | gofmt ✅
- **Coverage**: diff=91.2% (threshold: 90%)
```

### CI 通过

```
tag: autopilot
line: dev-line
node: dev-nK
goal_status: partial
next_role: orchestrator

## CI Pipeline 状态

### INCR-001-2-2
- **CC Lead**: ✅ done | commit: abc1234
- **CI Pipeline**: ✅ PASS | pipeline: mr.yaml
- **CI Jobs**: test ✅ | lint ✅ | codecov ✅ | gofmt ✅
- **Coverage**: line=78.5% | diff=92.1% (threshold: 90%)
- **Next**: 触发 Reviewer Agent（并行）
```

### CI 失败

```
tag: need_user
line: dev-line
node: dev-nK
goal_status: waiting
next_role: orchestrator

## CI Pipeline 失败

### INCR-001-2-2
- **CC Lead**: ✅ done | commit: abc1234
- **CI Pipeline**: ❌ FAIL | pipeline: mr.yaml

### 失败详情
| Job | Status | Error |
|-----|--------|-------|
| test | ✅ PASS | - |
| lint | ❌ FAIL | golangci-lint: unused variable `err` |
| codecov | ✅ PASS | diff=91.2% |
| gofmt | ✅ PASS | - |

### 分析
lint 失败，可修复。

### questions_for_user
{
  "type": "decision",
  "increment_id": "INCR-001-2-2",
  "question": "CI lint 失败，需要修复后重跑还是 rollback？",
  "options": ["A: 修复后重跑 CI", "B: Rollback 此 INCR"]
}
```
