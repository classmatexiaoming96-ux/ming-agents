# Incremental Implementation 本地化方案

> 版本：v1.0.0
> 日期：2026-04-05
> 状态：设计稿
> 依赖：planning-and-task-breakdown（v1.0.0）+ Reviewer Agent（v4.0.0）

---

## 一、背景与核心思想

### 1.1 现有 Dev-Agent 的执行粒度问题

当前 Dev-Agent 以 **sub-task** 为最小执行单元（0.5-2h）：

| 问题 | 描述 |
|------|------|
| **粒度仍偏粗** | 一个 sub-task 涉及多个文件改动的"一篮子"变更，难以原子回滚 |
| **Review 反馈慢** | 所有 sub-task 完成后才进入 REV_PHASE，CC Lead 的错误被放大 |
| **feature flag 缺失** | 跨多个 sub-task 的大功能无法安全灰度 |
| **rollback 成本高** | sub-task 失败只能 git revert，涉及多个文件的代码回滚 |
| **CC Lead 上下文过大** | 单次 CC Lead 调用承担多个关注点，出错时难以定位 |

### 1.2 incremental-implementation 核心思想

**"将 sub-task 再拆解为原子增量，每个增量独立 commit、独立 review、独立灰度"**

类比：
- `task_breakdown` = PM-Dev 执行的结构化（Task → 可执行依赖图）
- **incremental-implementation** = Dev-CC Lead 执行的结构化（sub-task → 原子增量链）

**增量三定律**：
1. **原子性**：每个增量改动 ≤ 1 个文件（或逻辑内聚的少数文件组）
2. **可切换**：每个增量有 feature flag 控制，默认为 off（安全默认值）
3. **可独立 Review**：每个增量完成后立即触发 Reviewer Agent，无需等待 DEV_PHASE 结束

---

## 二、整体架构

### 2.1 在 Shrimp 流程中的位置

```
Orchestrator 状态机
    │
    ├── PM_PHASE ──► PLANNING_PHASE ──────────────────────────┐
    │                                                         │
    │ DEV_PHASE                                               │
    │                                                         │
    │   Dev-Agent                                             │
    │     ├── dev-n0: Task 二次拆分 + 读取 task_breakdown    │
    │     │           + 生成 increment_plan.md                │
    │     │                                                       │
    │     └── dev-n1 ~ dev-nN: 逐 sub-task 执行                 │
    │               │                                            │
    │               ├── 每个 sub-task 拆为 N 个 increment       │
    │               │     ├── implement → test → verify          │
    │               │     ├── commit（原子提交）                   │
    │               │     └── trigger Reviewer Agent（独立 Review）│
    │               │                                            │
    │               └── rollback 触发时：                        │
    │                     增量级回滚（git revert 单 commit）     │
    │                     特性开关关闭（set flag=false）          │
    │                                                         │
    │ REV_PHASE（每个 increment 独立 Review，并行汇聚）         │
    │   Reviewer Agent × N（每个 increment 一个 Review session）  │
    └─────────────────────────────────────────────────────────┘
```

### 2.2 核心概念层级关系

```
requirement_id: SHRIMP-001
  │
  ├── Task List（PM-Agent pm-n2）
  │     ├── TASK-001（粗粒度）
  │     └── TASK-002（粗粒度）
  │
  ├── task_breakdown.md（PM-Agent plan-n1）
  │     ├── TASK-001-1（sub-task）
  │     ├── TASK-001-2（sub-task）← 这里开始 incremental 拆分
  │     │     ├── INCR-001-2-1（增量：新增 DedupEngine 结构体）
  │     │     ├── INCR-001-2-2（增量：实现 Dedup 方法）
  │     │     └── INCR-001-2-3（增量：新增 UT）
  │     └── TASK-002-1（sub-task）
  │           ├── INCR-002-1-1
  │           └── INCR-002-1-2
  │
  └── DEV_PHASE 执行时：
        每个 INCR 产生 1 个 git commit
        每个 INCR 触发 1 个 Reviewer Agent session
```

---

## 三、`increment_plan.md` 格式定义

### 3.1 文档结构总览

```markdown
# Increment Plan: {需求名称}

> 版本：{version} | 状态：{draft|confirmed|in_progress|done}
> 关联需求：{requirement_id}
> 关联 task_breakdown：docs/task_breakdown.md
> 关联 SPEC：docs/SPEC.md
> 生成节点：Dev-Agent dev-n0 | 执行节点：Dev-Agent dev-nK
```

### 3.2 全局 Feature Flag 注册表

```yaml
feature_flags:
  SHRIMP_001_DEDUP_ENGINE:
    default_value: false        # 安全默认值：永远 off
    owner: "@engineer_name"
    description: "告警收敛引擎主开关"
    increments: [INCR-001-2-1, INCR-001-2-2, INCR-001-2-3]
    rollback_strategy: "set flag=false（无需 git revert）"
  
  SHRIMP_001_CRITICAL_FASTPATH:
    default_value: false
    owner: "@engineer_name"
    description: "Critical 告警快通道"
    increments: [INCR-001-2-4]
    rollback_strategy: "set flag=false"
```

### 3.3 增量定义

```yaml
increment_id: INCR-001-2-2                      # 格式：INCR-{parent_sub_task}-{sequence}
parent_sub_task_id: TASK-001-2
sub_task_name: "实现 DedupEngine.Dedup 接口"

name: "INCR-001-2-2: 实现 Dedup 方法主体"
description: |
  在 internal/dedup/engine.go 中实现 Dedup 方法：
  - 签名：func (e *Engine) Dedup(alerts []Alert) ([]Alert, error)
  - 使用 map[alertKey]Alert 去重
  - 返回 RootCauseAlertIDs 字段
  - 依赖 feature_flag: SHRIMP_001_DEDUP_ENGINE

files_changed:
  - internal/dedup/engine.go         # 修改

files_created: []

feature_flag:
  name: SHRIMP_001_DEDUP_ENGINE
  scope: method_level                 # method_level | struct_level | file_level
  default_value: false                # 安全默认值
  toggle_mechanism: |
    // internal/dedup/engine.go
    var dedupEngineEnabled = false   // 全局变量，默认为 false
    
    func (e *Engine) Dedup(alerts []Alert) ([]Alert, error) {
      if !dedupEngineEnabled {
        return alerts, nil           // 安全默认值：不收敛，原样返回
      }
      // ... 实际逻辑
    }

acceptance_criteria:
  - "Dedup 方法签名正确，编译通过"
  - "当 dedupEngineEnabled=false 时，输入 alerts 原样返回"
  - "当 dedupEngineEnabled=true 时，相同 (svc, type, host) 去重为 1 条"
  - "有 UT 覆盖"

incremental_test:
  unit_test: internal/dedup/engine_dedup_test.go    # 必须先写/修改 UT
  test_command: go test ./internal/dedup/... -run TestDedup -v
  pass_criteria: "UT 全部通过，覆盖率不下降"

spec_clauses:
  - CONTRACT-2.1: DedupEngine.Dedup 契约
  - EDGE-001: Critical 告警不收敛

depends_on_increments:
  - INCR-001-2-1                  # 必须先完成 INCR-001-2-1（建结构体）

blocking_increments:
  - INCR-001-2-3                  # 本增量完成后，INCR-001-2-3 才能开始

commit_message_format: |
  [INCR-001-2-2] feat(dedup): 实现 Dedup 方法主体
  
  - 实现 DedupEngine.Dedup 方法
  - 使用 map[alertKey] 实现去重逻辑
  - feature_flag: SHRIMP_001_DEDUP_ENGINE（默认 false）
  - 关联 sub-task: TASK-001-2
  - 关联 SPEC: CONTRACT-2.1

reviewer_trigger:
  trigger: true                   # 增量完成后立即触发 Reviewer
  reviewer_type: [codequality]     # 本增量只需要 CodeQuality review
  reviewers_optional: [perf]       # 可选：也欢迎 Perf review
  focus_areas:                    # 提示 Reviewer 关注点
    - "Dedup 算法的正确性"
    - "feature_flag 使用的合理性"
    - "UT 覆盖率"

status: pending                   # pending | in_progress | review | passed | blocked | rolled_back
rollback:
  git_revert: "git revert {commit_hash}"   # 单 commit 回滚
  flag_rollback: "set SHRIMP_001_DEDUP_ENGINE=false"
  impact: "仅影响本增量，不影响 INCR-001-2-1"

dev_notes: ""
cc_lead_uuid: ""                  # 执行时的 CC Lead session ID
commit_hash: ""                   # 执行后的 commit hash
review_session_id: ""             # Reviewer Agent session ID
review_result: ""                 # passed | blocked | need_fix
```

---

## 四、Dev-Agent dev-n0 修改

### 4.1 dev-n0 新增：生成 increment_plan

在原有 dev-n0（读取 task_breakdown）之后，新增 increment_plan 生成步骤：

```markdown
### dev-n0：Task 分解确认 + CI 配置探测 + 生成 increment_plan

**执行**：
1. 读取 `docs/task_breakdown.md`
2. **CI 配置探测**（必须执行，结果写入 `.shrimp/ci_discovery.json`）：
   - 优先级：`Makefile` → `.gitlab-ci.yml` → `.github/workflows/*.yml` → fallback
   - **Makefile 存在**：提取 `test`/`lint`/`build`/`ci` 等 target
   - **`.gitlab-ci.yml` 存在**：提取 `stages` 和 `script` commands
   - **`.github/workflows/*.yml` 存在**：提取 `jobs[].steps[].run`
   - **均不存在**：使用语言 fallback（Go: `go test ./... && go vet ./...`）
   - 探测结果写入 `workspace/.shrimp/ci_discovery.json`：
     ```json
     {
       "ci_config": {
         "type": "makefile",
         "test_command": "make test",
         "lint_command": "make lint",
         "build_command": "make build",
         "fallback": "go test ./..."
       }
     }
     ```
   - **置信度标注**：`confidence: high`（Makefile）、`medium`（其他 CI 文件）、`low`（fallback）
3. 对每个 sub-task，拆解为 increments
4. 确定 increment 执行顺序（考虑 depends_on_increments）
5. 确定每个 increment 的 feature_flag
6. 生成全局 feature_flag 注册表
7. 生成 `docs/increment_plan.md`
8. 确认所有 increments 的 rollback 策略
9. 输出：`docs/dev_subtask_plan.md`（确认版，含 increment 拆分）

> **CI 探测注入规则**：每次 CC Lead 调用时，必须将 `ci_discovery.json` 的 `ci_config` 注入到 `constraints` 中：
> ```json
> "build_and_test": {
>   "required": true,
>   "test_command": "make test",
>   "lint_command": "make lint",
>   "must_pass_before_commit": true
> }
> ```
> 若 `ci_config.type` 为 `fallback`，则 `test_command` 使用 fallback 值。

**increment 拆分粒度规则**：
- 单个 increment 改动 ≤ 1 个文件（最佳）或 ≤ 3 个文件（上限）
- 每个 increment 有独立 feature_flag（或复用父 sub_task 的 flag）
- 每个 increment 满足：实现 → UT 写完 → 本地测试通过 → commit
- token 消耗：CC Lead 单次调用不超过 15k output tokens
- 预估时长：单个 increment 15-45 分钟

**increment 拆分模式**：
| 模式 | 适用场景 | 示例 |
|------|---------|------|
| Type-A（文件新建） | 新增文件 | INCR-001-2-1: 新建 engine.go |
| Type-B（字段添加） | 现有结构体增字段 | INCR-001-2-X: 给 Alert 结构体加 RootCauseAlertIDs |
| Type-C（方法实现） | 已有接口的实现 | INCR-001-2-2: 实现 Dedup 方法 |
| Type-D（逻辑替换） | 用 feature_flag 包裹新逻辑 | INCR-001-2-Y: 用 flag 切换新旧 Dedup 算法 |
| Type-E（测试追加） | 为现有代码加 UT | INCR-001-2-3: 新增 Dedup UT |
| Type-F（配置变更） | 纯配置变更 | INCR-002-1-1: 更新 dedup.yaml |

**increment 拆分检查清单**：
```
[ ] 每个 increment 改动文件 ≤ 3 个
[ ] 每个 increment 有 feature_flag（即使复用父级 flag）
[ ] 每个 increment 的 feature_flag 默认为 false
[ ] 每个 increment 有独立 UT 或已有 UT 通过
[ ] 每个 increment 的 rollback 策略 ≤ 1 行 git revert
[ ] increment 之间无循环依赖
```

**返回（dev-n0 完成）**：
```json
{
  "tag": "autopilot",
  "line": "dev-line",
  "node": "dev-n0",
  "goal_status": "partial",
  "next_role": "orchestrator",
  "outputs": {
    "ci_discovery": ".shrimp/ci_discovery.json",
    "ci_type": "makefile",
    "ci_confidence": "high",
    "increment_plan": "docs/increment_plan.md",
    "feature_flag_registry": "docs/increment_plan.md#feature_flags",
    "total_increments": 18,
    "total_sub_tasks": 12,
    "increments_per_subtask": {"TASK-001-2": 4, "TASK-001-3": 2, ...},
    "critical_path_increments": ["INCR-001-2-1", "INCR-001-2-2", "INCR-001-2-3"],
    "parallelizable_increments": ["INCR-002-1-1", "INCR-002-1-2"],
    "feature_flag_count": 6
  }
}
```
```

---

## 五、Dev-Agent dev-nK 修改：increment 执行循环

### 5.1 核心变化：sub-task → increment 循环

**旧架构**：
```
dev-nK: 对一个 sub-task 调用一次 CC Lead
  → CC Lead 一次性实现整个 sub-task（多文件）
  → 返回 → review（整个 sub-task 完成后）
```

**新架构**：
```
dev-nK: 对一个 sub-task 调用多次 CC Lead（每次一个 increment）
  → CC Lead 实现 increment-1（单文件）
  → 本地测试 → commit → trigger Reviewer
  → CC Lead 实现 increment-2（单文件）
  → 本地测试 → commit → trigger Reviewer
  → ...
  → sub-task 完成（所有 increments 完成）
```

### 5.2 CC Lead prompt 修改（increment 版本）

```json
{
  "increment": {
    "increment_id": "INCR-001-2-2",
    "parent_sub_task_id": "TASK-001-2",
    "name": "实现 Dedup 方法主体",
    
    "feature_flag": {
      "name": "SHRIMP_001_DEDUP_ENGINE",
      "scope": "method_level",
      "default_value": false,
      "implementation": "var dedupEngineEnabled = false",
      "guard_pattern": "if !dedupEngineEnabled { return alerts, nil }"
    },
    
    "description": "在 internal/dedup/engine.go 中实现 Dedup 方法...",
    "acceptance_criteria": ["..."],
    "files_affected": ["internal/dedup/engine.go"],
    
    "incremental_test": {
      "must_pass_before_commit": true,
      "test_file": "internal/dedup/engine_dedup_test.go",
      "test_command": "go test ./internal/dedup/... -run TestDedup -v",
      "pass_threshold": "所有 TestDedup 测试通过"
    },
    
    "spec_clauses": ["CONTRACT-2.1"],
    "depends_on_increments": ["INCR-001-2-1"],
    "blocking_increments": ["INCR-001-2-3"],
    
    "commit_message": "[INCR-001-2-2] feat(dedup): 实现 Dedup 方法主体\n\nfeature_flag: SHRIMP_001_DEDUP_ENGINE（默认 false）"
  },
  
  "workspace_dir": "/path/to/workspace",
  "feature_branch": "feature/shrimp-SHRIMP-001",
  
  "superpower_skills": [
    "test-driven-development（必须先写 UT，再实现）",
    "verification-before-completion（UT 全部通过才能 commit）",
    "incremental-implementation（本 increment 只做本 increment 的事）"
  ],
  
  "constraints": {
    "max_files_changed": 3,
    "max_output_tokens": 15000,
    "must_use_feature_flag": true,
    "must_write_ut_first": true,
    "must_pass_test_before_commit": true,
    "no_跨_increment改动": true
  }
}
```

### 5.3 increment 执行流程

```
Dev-Agent dev-nK 接收 sub-task TASK-001-2（共 4 个 increments）
  │
  ▼
for each INCR in sub-task.increments:
  │
  │ [INCR-001-2-1] Type-A：新建 engine.go
  │
  ├── 等待 depends_on_increments 完成
  │
  ├── cc-start.py（首个）/ cc-send.py（后续）驱动 CC Lead（prompt 含 increment 上下文）
  │
  ├── CC Lead 实现 → UT → 本地测试
  │
  ├── CC Lead 返回 {status, files_changed, test_results, commit_hash}
  │
  ├── status=success?
  │     │
  │     YES
  │     ├── git commit（INCR-001-2-1）
  │     ├── 更新 increment_plan status=review
  │     ├── Orchestrator dispatch codex_reviewer（parallel，不阻塞后续 INCR）
  │     ├── 更新 increment_plan status=done
  │     └── continue next INCR
  │
  │     NO（status=error）
  │     ├── 执行 rollback（git revert）
  │     ├── 更新 increment_plan status=rolled_back
  │     ├── 分析错误类型（可重试？不可重试？）
  │     │     │
  │     │     ├── 可重试 → 重试 CC Lead（max 2次）
  │     │     └── 不可重试 → 汇报 Orchestrator（blocked）
  │     └── continue 或 blocked
  │
  ▼
sub-task TASK-001-2 所有 increments 完成
  │
  ├── 更新 TASK_BOARD sub-task status=done
  └── 汇报 Orchestrator（sub-task 完成）
```

### 5.4 Reviewer Agent 并行触发（不阻塞 CC Lead）

```python
# Dev-Agent 在 CC Lead 成功 commit 后：
#   1. 不等待 Reviewer 结果，立即继续下一个 INCR
#   2. Reviewer 在 graph 模型里由 Orchestrator dispatch codex_reviewer 独立执行
#      （不用未实现的 sessions_spawn；dev_agent 只需汇报 commit_hash 给 Orchestrator）

dispatch_codex_reviewer(   # 由 Orchestrator 执行，非 dev_agent 自身
  {
    "review_type": "codequality",  # 可按 INCR 类型选择 reviewer type
    "commit_hash": "abc1234",       # Review 特定 commit
    "scope": "INCR-001-2-1",        # 只 review 本 increment 改动
    "focus_areas": [...],
    "feature_flag": "SHRIMP_001_DEDUP_ENGINE"
  }
)
```

### 5.5 跨 increment 的 feature flag 传递

```
INCR-001-2-1: 新建 engine.go
  └── 定义 var dedupEngineEnabled = false  ← 全局 flag 变量

INCR-001-2-2: 实现 Dedup 方法
  └── 引用 dedupEngineEnabled（不重新定义）
  └── 方法内部有 if !dedupEngineEnabled { return alerts, nil }

INCR-001-2-3: 新增 UT
  └── 测试用例需要测 flag=true 和 flag=false 两种情况

INCR-001-2-4: 集成 DedupEngine 到 Dispatcher（跨 sub-task）
  └── 引用 dedupEngineEnabled
  └── rollback 时只改 flag，不改集成代码
```

---

## 六、Rollback 机制

### 6.1 增量级 Rollback（INCR 内）

```yaml
rollback_strategy: "单 increment 回滚"

trigger:
  - CC Lead 返回 status=error（编译失败）
  - CC Lead 返回 status=error（UT 失败且重试 2 次仍失败）
  - Reviewer Agent 返回 Blocker finding

action:
  git_rollback: "git revert {commit_hash} --no-edit"
  flag_rollback: "确保 flag=默认值（通常是 false）"
  notification: "记录到 blockers，上报 Orchestrator"

impact:
  blocked_increments: ["INCR-001-2-3", "INCR-001-2-4"]  # 依赖本 INCR 的
  unaffected_increments: ["INCR-001-2-1", "INCR-002-1-1"]  # 不依赖的
```

### 6.2 sub-task 级 Rollback（所有 increments 一起回滚）

```yaml
rollback_strategy: "sub-task 级别回滚（多个 increments）"

trigger:
  - 超过 50% increments 需要 rollback
  - Critical Path 上的 increment 失败
  - Reviewer Agent 返回多个 Blocker finding

action:
  git_rollback: |
    # 回滚从第一个 increment 到最后一个 increment 的所有 commits
    git revert {first_incr_commit}~1..{last_incr_commit}~1 --no-edit
  flag_rollback: "set all SHRIMP_XXX flags = false"
  notification: "通知 Orchestrator + 用户，暂停 DEV_PHASE"

impact:
  blocked_sub_tasks: ["TASK-001-2", "TASK-001-3"]
  unaffected_sub_tasks: ["TASK-001-1"]
```

### 6.3 feature flag 热回滚（无需代码回滚）

```yaml
rollback_strategy: "热回滚（只关 flag，不改代码）"

适用场景:
  - 代码逻辑正确，但需要快速关闭功能
  - 线上发现 bug，需要临时降级

action:
  flag_rollback: |
    # 修改 internal/dedup/engine.go
    var dedupEngineEnabled = false  // 改为 false，即时生效
    # 无需 git revert，无需重新部署
    # 下次 CI/CD pipeline 自动使用 false

review:
  - Reviewer Agent 仍会通过（代码没改）
  - 人工验证 flag=false 时系统行为
  - 决定是修代码还是保持 flag=false
```

---

## 七、与 Reviewer Agent 的联动

### 7.1 Reviewer Agent per Increment

每个 increment 完成后，触发 Reviewer Agent，scope 限制在本次 commit 的增量范围内：

```json
{
  "requirement_id": "SHRIMP-001",
  "reviewer_type": "codequality",
  "review_scope": "increment",
  "commit_hash": "abc1234",
  "increment_id": "INCR-001-2-2",
  "files_changed": ["internal/dedup/engine.go"],
  "feature_flag": "SHRIMP_001_DEDUP_ENGINE",
  "focus_areas": [
    "Dedup 算法实现的正确性",
    "feature_flag 使用的合理性（off=安全降级）",
    "UT 覆盖率"
  ]
}
```

### 7.2 Reviewer 结论处理

| Reviewer 结论 | Dev-Agent 动作 |
|--------------|---------------|
| passed | 继续下一个 increment |
| need_fix | 修复（新增一个 INCR-XXX-FIX）→ Review 再次 |
| blocked | rollback increment → 汇报 Orchestrator |

### 7.3 跨 Increment 的 Review 汇总

Reviewer Agent 每次只 review 1 个 increment，但 Orchestrator 在 Gate 3 做全局汇总：

```markdown
## Review 汇总：SHRIMP-001

### Increment Review 结果

| Increment | Reviewer | 结论 | Findings |
|-----------|---------|------|----------|
| INCR-001-2-1 | CodeQuality | ✅ passed | 0 Blocker |
| INCR-001-2-2 | CodeQuality | ✅ passed | 0 Blocker |
| INCR-001-2-3 | Perf | ⚠️ need_fix | 1 Major（性能热点）|
| INCR-001-2-4 | Arch | ✅ passed | 0 Blocker |
| ... | ... | ... | ... |

### 全局 Findings（去重后）

| ID | Severity | 描述 | Increment |
|----|----------|------|-----------|
| PERF-001 | Major | Dedup 算法 O(n^2) | INCR-001-2-2 |
```

---

## 八、与 planning-and-task-breakdown 的衔接

### 8.1 task_breakdown.md → increment_plan.md 的映射

| task_breakdown 字段 | increment_plan 对应 |
|--------------------|--------------------|
| sub_task.hours | sum(increments.hours) |
| sub_task.rollback_plan | increment 级 rollback（更细粒度）|
| sub_task.failure_hypothesis | 在每个 increment 中单独处理 |
| sub_task.acceptance_criteria | 拆分为每个 increment 的 acceptance_criteria |
| sub_task.blocking | increment 的 blocking_increments |

### 8.2 字段继承关系

```
task_breakdown sub_task (TASK-001-2)
  ├── spec_clauses: [CONTRACT-2.1, EDGE-001, ...]
  ├── rollback_plan: {trigger, steps, impact_scope}
  ├── failure_hypotheses: [FH-001, FH-002, ...]
  │
  └── increment_plan sub-tasks:
        ├── INCR-001-2-1
        │     ├── inherits: [CONTRACT-2.1]
        │     ├── rollback: 单 commit git revert
        │     └── fh: FH-001 映射（高并发竞态 → detection: race condition）
        ├── INCR-001-2-2
        │     ├── inherits: [CONTRACT-2.1, EDGE-001]
        │     ├── rollback: 单 commit git revert
        │     └── fh: FH-002 映射（Critical 告警误丢弃 → TDD-001 失败）
        └── INCR-001-2-3
              ├── inherits: [CONTRACT-2.1, TEST-001]
              ├── rollback: 单 commit git revert
              └── fh: 无（测试类 increment）
```

---

## 九、完整执行流程（改造后）

```
Orchestrator
    │
    ├── PLANNING_PHASE
    │     └── plan-n1: PM-Agent 生成 task_breakdown.md（含 rollback_plan）
    │
    │ Gate 1.5
    │
    ▼
DEV_PHASE
    │
    ├── dev-n0: Dev-Agent 读取 task_breakdown
    │       └── 新增：生成 increment_plan.md
    │           ├── 拆分 sub-task → increments
    │           ├── 确定 feature_flag 注册表
    │           └── 确定每个 increment 的 rollback 策略
    │
    ├── dev-n1: 第一个 sub-task 开始
    │     │
    │     └── INCR-001-2-1（Type-A: 新建文件）
    │           ├── CC Lead 实现
    │           ├── 本地 UT 通过
    │           ├── git commit
    │           ├── trigger Reviewer Agent（并行，不阻塞）
    │           └── 继续 INCR-001-2-2
    │
    │     └── INCR-001-2-2（Type-C: 实现方法）
    │           ├── CC Lead 实现
    │           ├── 本地 UT 通过
    │           ├── git commit
    │           ├── trigger Reviewer Agent（并行）
    │           └── 继续 INCR-001-2-3
    │
    │     └── INCR-001-2-3（Type-E: 新增 UT）
    │           ├── ...
    │
    │     └── INCR-001-2-N（最后一个 increment 完成）
    │           └── sub-task TASK-001-2 done → 汇报 Orchestrator
    │
    ├── dev-n2: 下一个 sub-task ...
    │
    │ REVIEW_PHASE（并行，汇聚所有 increment 的 review 结果）
    │
    │ Gate 3 验证
    │
    └── dev-submit: 提交
```

---

## 十、Dev-Agent SKILL.md 改造清单

### 10.1 dev-n0 新增步骤

```markdown
### dev-n0 新增：CI 配置探测 + increment_plan 生成

在原有 dev-n0 读取 task_breakdown.md 后，新增：

**Step 1：CI 配置探测（必须先执行）**

```markdown
CI 配置探测优先级：Makefile → .gitlab-ci.yml → .github/workflows/*.yml → fallback

1. 检查 Makefile → 提取 test/lint/build target
2. 检查 .gitlab-ci.yml → 提取 stages/script
3. 检查 .github/workflows/*.yml → 提取 jobs[].steps
4. 均无 → fallback（Go: go test ./...）

输出：workspace/.shrimp/ci_discovery.json
注入：后续 CC Lead 调用时将 ci_config 注入 constraints.build_and_test
```

**Step 2：increment_plan 生成规则**：

1. **拆分粒度**
   - 每个 increment 改动 ≤ 3 个文件
   - 每个 increment 预估 15-45 分钟
   - 每个 increment 独立 UT 可验证

2. **feature_flag 分配**
   - 共享 flag：一个 sub_task 内的多个 increment 共用一个 flag
   - 独立 flag：跨 sub_task 的大功能独立 flag
   - 默认值：永远 false（安全降级）

3. **顺序编排**
   - Type-A（新建）→ Type-C（实现）→ Type-E（UT）→ Type-D（集成）
   - 依赖关系通过 depends_on_increments 表达

4. **rollback 预判**
   - 每个 increment 的 rollback = 单 commit git revert
   - flag rollback = set flag=false（热回滚）

**输出文件**：`docs/increment_plan.md`
```

### 10.2 dev-nK 循环修改

```markdown
### dev-nK 修改：sub-task 执行循环（increment 版）

**旧**：
- 对一个 sub-task 调用一次 CC Lead（整个 sub-task 的所有改动一次完成）
- 返回后进入下一个 sub-task

**新**：
- 对一个 sub-task 调用多次 CC Lead（每个 increment 一次调用）
- 每个 increment 完成后：
  1. 本地测试必须通过（go test）
  2. git commit（原子提交）
  3. trigger Reviewer Agent（并行，不等待结果）
  4. 立即继续下一个 increment
- 所有 increments 完成 → sub-task 完成 → 汇报 Orchestrator
```

### 10.3 新增 rollback 触发（increment 级）

```markdown
### rollback 触发（increment 级）

**触发条件**：
- CC Lead status=error（编译失败 / UT 失败）
- Reviewer Agent 返回 Blocker finding
- CC Lead 检测到 failure_hypothesis 中的症状

**执行**：
1. git revert {commit_hash} --no-edit
2. set feature_flag = false（如有）
3. 更新 increment_plan status=rolled_back
4. blocked_increments 标记
5. 上报 Orchestrator

**决策分支**：
- 可修复（逻辑错误）→ 新增 INCR-XXX-FIX，重试（max 2次）
- 不可修复（设计问题）→ 上报 Orchestrator，blocked
```

---

## 十一、与 Reviewer Agent 的接口

### 11.1 Reviewer Agent 触发格式

```json
{
  "action": "dispatch",
  "note": "由 Orchestrator dispatch codex_reviewer；不用未实现的 sessions_spawn",
  "target": "reviewer_agent",
  "params": {
    "reviewer_type": "codequality",
    "commit_hash": "{cc_lead_commit_hash}",
    "requirement_id": "SHRIMP-001",
    "feature_branch": "feature/shrimp-SHRIMP-001",
    "scope": "increment",
    "increment_id": "INCR-001-2-2",
    "files_to_review": ["internal/dedup/engine.go"],
    "feature_flag": "SHRIMP_001_DEDUP_ENGINE",
    "focus_areas": [
      "Dedup 算法正确性",
      "feature_flag 降级路径",
      "UT 覆盖率"
    ]
  }
}
```

### 11.2 Reviewer Agent 接收修改

Reviewer Agent 需要支持 `scope: increment` 模式：
- 只 review 本 increment 涉及的 commit
- 限制 review 范围在 `files_to_review` 列表
- Review 完成后回调 Orchestrator（或写入 memory）

---

## 十二、Feature Flag 实现规范

### 12.1 命名规范

```
SHRIMP_{requirement_id}_{feature_name}
示例：SHRIMP_001_DEDUP_ENGINE
```

### 12.2 代码实现模式

```go
// 1. 全局变量声明（package 级别，默认 false）
var dedupEngineEnabled = false

// 2. 功能入口检查（guard pattern）
func (e *Engine) Dedup(alerts []Alert) ([]Alert, error) {
  if !dedupEngineEnabled {
    return alerts, nil  // 安全降级：不收敛，原样返回
  }
  // ... 实际逻辑
}

// 3. 测试支持（测试文件可设置 true）
// internal/dedup/engine_test.go
func TestDedupEnabled(t *testing.T) {
  old := dedupEngineEnabled
  dedupEngineEnabled = true
  defer func() { dedupEngineEnabled = old }()
  // ... test with flag on
}

func TestDedupDisabled(t *testing.T) {
  // dedupEngineEnabled 默认 false
  // ... test with flag off
}
```

### 12.3 热回滚验证

```
验证热回滚成功：
1. 检查代码中 dedupEngineEnabled = false
2. 重新运行测试套件
3. 验证 alerts 原样返回（不收敛）
4. 无需代码回滚，无需重新部署
```

---

## 十三、TASK_BOARD 更新

### 13.1 新增 increment 追踪字段

```markdown
## TASK_BOARD

### DEV_PHASE

#### sub_task: TASK-001-2
- **status**: in_progress
- **increments**:
  - INCR-001-2-1 ✅ done | commit: abc1234 | reviewer: passed
  - INCR-001-2-2 🔄 in_progress | cc_lead: session-uuid
  - INCR-001-2-3 ⏳ pending | depends_on: [INCR-001-2-2]
  - INCR-001-2-4 ⏳ pending | depends_on: [INCR-001-2-2, INCR-001-2-3]
- **progress**: 1/4 increments done
```

---

## 十五、CI 配置探测机制

### 15.1 背景

不同代码库的 CI 配置位置和命令各不相同：
- Makefile（`make test`, `make lint`）
- `.gitlab-ci.yml`（提取 stages 和 script）
- `.github/workflows/*.yml`（提取 jobs 和 steps）
- `Makefile` 缺失时，使用语言通用 fallback

Dev Agent 在 `dev-n0` 初始化阶段必须自动探测仓库的 CI 配置，并将结果注入到后续所有 CC Lead 调用中。

### 15.2 探测流程

```
dev-n0 初始化阶段
  │
  ▼
Step 1：探测 CI 配置文件（优先级顺序）
  │
  ├── Makefile 存在？
  │     └── YES → 提取 test/lint/build 等 target
  │
  ├── .gitlab-ci.yml 存在？
  │     └── YES → 提取 stages 和 commands
  │
  ├── .github/workflows/*.yml 存在？
  │     └── YES → 提取 jobs.steps
  │
  └── 无 CI 配置？
        └── 使用语言通用 fallback
            Go:   go test ./... && go vet ./...
            Rust: cargo test && cargo clippy

  ▼
Step 2：记录 ci_discovery.json
  │
  └── 写入 .shrimp/ci_discovery.json
        {
          "detected_at": "2026-04-05T19:00:00+08:00",
          "ci_type": "makefile",
          "source_file": "Makefile",
          "commands": {
            "test": "make test",
            "lint": "make lint",
            "build": "make build"
          },
          "raw_content_preview": "..."  # 前 20 行供人工确认
        }

  ▼
Step 3：注入到 CC Lead prompt
  │
  └── 所有 dev-nK 的 CC Lead 调用中注入：
        "build_and_test": {
          "required": true,
          "test_command": "make test",    # 来自 ci_discovery.json
          "lint_command": "make lint",
          "must_pass_before_commit": true
        }
```

### 15.3 ci_discovery.json Schema

```json
{
  "detected_at": "ISO8601 时间戳",
  "ci_type": "makefile | gitlab-ci | github-workflows | fallback",
  "source_file": "Makefile | .gitlab-ci.yml | .github/workflows/ci.yml | null",
  "commands": {
    "test": "make test | go test ./... | cargo test | ...",
    "lint": "make lint | go vet ./... | cargo clippy | ...",
    "build": "make build | go build ./... | cargo build | ..."
  },
  "fallback_reason": "无 Makefile，使用 Go 通用 fallback（可选字段）",
  "raw_content_preview": "FROM ... # 前 20 行",
  "confidence": "high | medium | low"
}
```

### 15.4 CC Lead prompt 注入格式

```json
{
  "increment": { ... },

  "workspace_dir": "/path/to/workspace",
  "feature_branch": "feature/shrimp-SHRIMP-001",

  "ci_config": {
    "type": "makefile",
    "source_file": "Makefile",
    "test_command": "make test",
    "lint_command": "make lint",
    "build_command": "make build"
  },

  "constraints": {
    "must_pass_before_commit": true,
    "build_and_test": {
      "command": "make test && make lint",
      "description": "编译 + 测试 + Lint 必须全部通过才能 commit"
    }
  }
}
```

### 15.5 CI 探测实现伪代码

```python
def discover_ci_config(workspace_dir):
    """探测仓库 CI 配置，返回 ci_discovery.json 内容"""

    # 优先级：Makefile > .gitlab-ci.yml > .github/workflows/*.yml
    makefile = os.path.join(workspace_dir, "Makefile")
    if os.path.exists(makefile):
        content = read_file(makefile)
        targets = extract_makefile_targets(content)  # 提取 test/lint/build
        return {
            "ci_type": "makefile",
            "source_file": "Makefile",
            "commands": targets,
            "confidence": "high"
        }

    gitlab_ci = os.path.join(workspace_dir, ".gitlab-ci.yml")
    if os.path.exists(gitlab_ci):
        content = read_file(gitlab_ci)
        stages_scripts = parse_gitlab_ci(content)
        return {
            "ci_type": "gitlab-ci",
            "source_file": ".gitlab-ci.yml",
            "commands": stages_scripts,
            "confidence": "medium"
        }

    github_workflows = os.path.join(workspace_dir, ".github", "workflows")
    if os.path.isdir(github_workflows):
        yml_files = glob.glob(f"{github_workflows}/*.yml")
        if yml_files:
            content = read_file(yml_files[0])
            jobs = parse_github_workflow(content)
            return {
                "ci_type": "github-workflows",
                "source_file": yml_files[0],
                "commands": jobs,
                "confidence": "medium"
            }

    # 无 CI 配置 → 使用语言 fallback
    return language_fallback(workspace_dir)
```

### 15.6 CI 配置变更检测

Dev Agent 在 `dev-n0` 时记录 CI 配置，后续 CC Lead 调用时如果检测到 CI 配置文件被修改（如 Makefile 变动），应重新探测并更新 `ci_discovery.json`。

---

## 十六、版本更新规划

| 版本 | 变更 |
|------|------|
| v1.0.0 | 初稿：定义 increment_plan.md Schema、dev-n0 increment 拆分、dev-nK increment 执行循环、Reviewer per-INCR 并行触发、feature_flag 规范、increment 级 rollback |
| v1.1.0 | 新增：CI 配置探测机制（dev-n0 初始化时自动探测，注入 CC Lead prompt） |
