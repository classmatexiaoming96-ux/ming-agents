# Dev-Agent 执行步骤详解

## dev-n0：Task 分解确认 + 生成 increment_plan

> **节点性质**：**AFK** —— 读 task_breakdown → 拆 increments → 输出 increment_plan.md；见 `../../pm_agent/references/afk_hitl.md`

**输入（来自 Orchestrator）**：
- `requirement_id`：需求 ID
- `task_list_path`：Task 列表文件路径
- `workspace_dir`：工作目录
- `feature_branch`：Git feature 分支名

**执行**：
1. 读取 `docs/task_breakdown.md`
2. 解析 Task 列表，提取每个 Task 的：ID、名称、输入、输出、验收标准
3. **对每个 sub-task，拆解为 increments**
4. **确定 increment 执行顺序**（考虑 depends_on_increments）
5. **确定每个 increment 的 feature_flag**
6. **生成全局 feature_flag 注册表**
7. **生成 `docs/increment_plan.md`**
8. 生成 `docs/dev-subtask-plan.md`（确认版，含 increment 拆分）

**increment 拆分检查清单**：
```
[ ] 每个 increment 改动文件 ≤ 3 个
[ ] 每个 increment 有 feature_flag（即使复用父级 flag）
[ ] 每个 increment 的 feature_flag 默认为 false
[ ] 每个 increment 有独立 UT 或已有 UT 通过
[ ] 每个 increment 的 rollback 策略 ≤ 1 行 git revert
[ ] increment 之间无循环依赖
[ ] 每个 increment 有对应文档更新（README/CHANGELOG/API Schema）
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
    "increment_plan": "docs/increment_plan.md",
    "feature_flag_registry": "docs/increment_plan.md#feature_flags",
    "total_increments": 18,
    "total_sub_tasks": 12,
    "critical_path_increments": ["INCR-001-2-1", "INCR-001-2-2"],
    "parallelizable_increments": ["INCR-002-1-1", "INCR-002-1-2"]
  }
}
```

---

## dev-n1 ~ dev-nN：逐 Task 执行（increment 循环）

> **节点性质**：**Mixed** —— 主体 AFK（CC Lead 红绿重构 + 自动触发 Reviewer + 自动 commit）；`status=error` 一次直改未中转 diagnose（仍 AFK），重试 2 次仍失败 → **HITL**（blocked，上报 Orchestrator）

```
Dev-Agent dev-nK 接收 sub-task TASK-001-2（共 N 个 increments）
  │
  ▼
for each INCR in sub-task.increments:
  │
  ├── 等待 depends_on_increments 完成
  │
  ├── cc-start.py（首个 INCR）/ cc-send.py（后续）驱动 CC Lead（prompt 含 increment 上下文）
  │      见 SKILL.md《CC Lead 调用规范》；不用未实现的 sessions_spawn
  │
  ├── CC Lead 红(先写失败 UT)→绿(最小实现转绿)→重构(仅绿灯下) → 本地全测
  │      （见 ./tdd_discipline.md；禁止"先全测后全码"，一测一码）
  │
  ├── CC Lead 返回 {status, files_changed, test_results, commit_hash}
  │
  ├── **CI Pipeline 动态选择**（见下方详述）
  │
  ├── 触发 CI pipeline
  │
  ├── 等待 CI 结果（timeout=10min）
  │
  ├── CI 通过？ → 继续
  │     NO
  │     ├── 分析 CI 失败原因
  │     ├── 可修复 → 修复后重跑 CI
  │     └── 不可修复 → 汇报 Orchestrator（blocked）
  │
  ├── status=success?
  │     YES
  │     ├── git commit（INCR-XXX-Y）
  │     ├── 更新 increment_plan status=review
  │     ├── Orchestrator dispatch codex_reviewer（parallel，不阻塞后续 INCR）
  │     └── continue next INCR
  │
  │     NO（status=error）
  │     ├── 执行 rollback（git revert）
  │     ├── 分析错误类型
  │     │     ├── 一次直改未中 → 走 diagnose 6 步（见 ./diagnose.md，禁盲目重试）→ 修复（max 2 轮）
  │     │     └── 不可修复 → 汇报 Orchestrator（blocked）
  │     └── continue 或 blocked
  │
  ▼
sub-task TASK-001-2 所有 increments 完成
  ├── 更新 TASK_BOARD sub-task status=done
  └── 汇报 Orchestrator（sub-task 完成）
```

> **TDD 纪律（强制，见 `./tdd_discipline.md`）**：实现型 INCR 一律 **Red→Green→Refactor**——
> CC Lead 先写 `tdd_test_ids` 对应的失败测试（红），再写最小实现使其转绿，最后仅在绿灯下重构。
> 测试只经**公开接口**断言行为（重命名内部函数不应使其失败）。`tdd_test_ids` 取自 SPEC `TEST_STRATEGY`
> 的 `TDD-XXX` 编号，由 increment_plan 分配给本 INCR；声明的 `tdd_test_ids` 全部转绿后才 commit。

**CC Lead 调用格式（increment 版本）**：

```json
{
  "increment": {
    "increment_id": "INCR-001-2-2",
    "parent_sub_task_id": "TASK-001-2",
    "name": "实现 Dedup 方法主体",
    "feature_flag": {
      "name": "SHRIMP_001_DEDUP_ENGINE",
      "scope": "method_level",
      "default_value": false
    },
    "acceptance_criteria": ["..."],
    "files_affected": ["internal/dedup/engine.go"],
    "incremental_test": {
      "cycle": "red-green-refactor (先写失败 UT → 最小实现转绿 → 仅绿灯下重构)",
      "write_failing_test_first": true,
      "must_pass_before_commit": true,
      "test_file": "internal/dedup/engine_dedup_test.go",
      "test_command": "go test ./internal/dedup/... -run TestDedup -v"
    },
    "spec_clauses": ["CONTRACT-2.1"],
    "tdd_test_ids": ["TDD-012", "TDD-013"],
    "depends_on_increments": ["INCR-001-2-1"]
  },

  "constraints": {
    "max_files_changed": 3,
    "max_output_tokens": 15000,
    "must_use_feature_flag": true,
    "must_write_ut_first": true,
    "must_pass_test_before_commit": true,
    "no_cross_increment_changes": true,
    "security_check": {
      "no_hardcoded_secrets": "禁止硬编码凭证",
      "no_secrets_in_logs": "日志禁止打印 password/token/ak/sk",
      "parameterized_sql": "参数化查询",
      "xss_prevention": "输入输出转义"
    },
    "performance_check": {
      "timeout_required": "所有 RPC/HTTP 调用必须设置 timeout",
      "connection_pool_limit": "连接池必须配置上限",
      "no_defer_in_loop": "禁止在循环中使用 defer",
      "goroutine_lifecycle": "Goroutine 必须有受控生命周期",
      "no_n_plus_one": "避免 N+1 查询"
    }
  }
}
```

**CC Lead 返回格式（期望）**：

```json
{
  "status": "success | error | need_decision",
  "files_changed": ["internal/dedup/engine.go"],
  "test_results": { "passed": 5, "failed": 0, "coverage": 82.1 },
  "commits": ["abc1234"],
  "errors": [],
  "warnings": []
}
```

---

## CI Pipeline 动态选择机制

Dev-Agent 在每次 INCR 完成后，触发 CI 前，先读取 `.codebase/pipelines/` 目录选择合适的 pipeline。

### Pipeline 选择规则

| 条件 | 选择 Pipeline | 额外检查 |
|------|--------------|---------|
| 有未合并的 MR | `mr.yaml` | test + lint + codecov + gofmt |
| 无 MR 或直接 push | `push.yaml` | test + lint + codecov |
| 包含 RPC/接口变更 | 额外触发 `log_analysis.yaml` | 变更文件日志分析 |

### Pipeline 选择流程

```python
def select_ci_pipeline(context):
    # 1. 读取 .codebase/pipelines/ 目录
    available_pipelines = list_files(".codebase/pipelines/")

    # 2. 检查是否有未合并的 MR
    has_open_mr = check_mr_status()

    # 3. 选择 pipeline
    if has_open_mr and "mr.yaml" in available_pipelines:
        pipeline = "mr.yaml"
    elif "push.yaml" in available_pipelines:
        pipeline = "push.yaml"
    else:
        pipeline = available_pipelines[0]  # fallback

    # 4. 检查是否需要额外触发 log_analysis
    changed_files = get_changed_files()
    if any(is_rpc_related(f) for f in changed_files):
        extra_pipeline = "log_analysis.yaml"

    return pipeline, extra_pipeline  # extra_pipeline 可能为 None
```

### CI 触发与结果处理

```python
def trigger_and_wait_ci(pipeline, changed_files):
    # 1. 触发 CI
    ci_run = trigger_codebase_ci(
        pipeline=pipeline,
        files=changed_files,
        timeout=600  # 10min
    )

    # 2. 等待结果
    result = ci_run.wait()

    # 3. 写入 CI 结果到 artifacts/dev/ci_result.json
    write_ci_result(result)

    # 4. 返回结果
    return result
```

### CI 结果格式

写入 `artifacts/dev/ci_result.json`：

```json
{
  "pipeline": "mr.yaml",
  "triggered_at": "2026-04-15T11:00:00+08:00",
  "increment_id": "INCR-001-2-2",
  "jobs": {
    "test": "PASS",
    "lint": "PASS",
    "codecov": "PASS",
    "gofmt": "PASS"
  },
  "coverage": {
    "line": 78.5,
    "diff": 92.1,
    "threshold": { "line": 70, "diff": 90 }
  },
  "exit_code": 0,
  "failed_jobs": []
}
```

### Gate 2 CI 检查项

- `ci_result.exit_code == 0`
- `ci_result.jobs.test == "PASS"`
- `ci_result.jobs.lint == "PASS"`
- `ci_result.coverage.diff >= 90`

---

## Rollback 机制

### 增量级 Rollback（INCR 内）

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
```

### sub-task 级 Rollback（所有 increments 一起回滚）

```yaml
rollback_strategy: "sub-task 级别回滚"

trigger:
  - 超过 50% increments 需要 rollback
  - Critical Path 上的 increment 失败

action:
  git_rollback: |
    git revert {first_incr_commit}~1..{last_incr_commit}~1 --no-edit
  flag_rollback: "set all SHRIMP_XXX flags = false"
```

### feature flag 热回滚（无需代码回滚）

```yaml
rollback_strategy: "热回滚（只关 flag，不改代码）"

适用场景:
  - 代码逻辑正确，但需要快速关闭功能
  - 线上发现 bug，需要临时降级

action:
  flag_rollback: |
    var dedupEngineEnabled = false  # 即时生效，无需部署
```

---

## 与 Reviewer Agent 的联动

### 触发时机

每个 increment commit 成功后，**立即并行触发** Reviewer Agent，**不等待结果继续下一个 INCR**。

```json
{
  "action": "dispatch",
  "note": "在 graph 模型里由 Orchestrator dispatch codex_reviewer；不用未实现的 sessions_spawn",
  "target": "reviewer_agent",
  "params": {
    "reviewer_type": "codequality",
    "commit_hash": "{cc_lead_commit_hash}",
    "scope": "increment",
    "increment_id": "INCR-001-2-2",
    "files_to_review": ["internal/dedup/engine.go"],
    "focus_areas": ["Dedup 算法正确性", "feature_flag 降级路径"]
  }
}
```

### Reviewer 结论处理

| Reviewer 结论 | Dev-Agent 动作 |
|--------------|---------------|
| passed | 继续下一个 increment |
| need_fix | 新增 `INCR-XXX-FIX`，prompt 走 **diagnose 6 步**（见 `./diagnose.md`），第 5 步回归测试用 SPEC `TDD-XXX` → Review 再次 |
| blocked | rollback increment → 汇报 Orchestrator |

> **Blocker / CI 失败 / status=error 的修复一律走 `./diagnose.md` 6 步**（建反馈环 → 复现 → 3-5 可证伪假设 → 单变量插桩 → 修+回归测试 → 清理）。完成判据含"`grep [DEBUG-` 零残留"。
