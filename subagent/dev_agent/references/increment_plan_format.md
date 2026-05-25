# increment_plan.md 格式定义

## 文档结构总览

```markdown
# Increment Plan: {需求名称}

> 版本：{version} | 状态：{draft|confirmed|in_progress|done}
> 关联需求：{requirement_id}
> 关联 task_breakdown：docs/task_breakdown.md
> 关联 SPEC：docs/SPEC.md
> 生成节点：Dev-Agent dev-n0 | 执行节点：Dev-Agent dev-nK
```

## 全局 Feature Flag 注册表

```yaml
feature_flags:
  SHRIMP_001_DEDUP_ENGINE:
    default_value: false        # 安全默认值：永远 off
    owner: "@engineer_name"
    description: "告警收敛引擎主开关"
    increments: [INCR-001-2-1, INCR-001-2-2, INCR-001-2-3]
    rollback_strategy: "set flag=false（无需 git revert）"
```

## 增量定义

```yaml
increment_id: INCR-001-2-2                      # 格式：INCR-{parent_sub_task}-{sequence}
parent_sub_task_id: TASK-001-2
sub_task_name: "实现 DedupEngine.Dedup 接口"

name: "INCR-001-2-2: 实现 Dedup 方法主体"
description: |
  在 internal/dedup/engine.go 中实现 Dedup 方法：
  - 签名：func (e *Engine) Dedup(alerts []Alert) ([]Alert, error)
  - 使用 map[alertKey]Alert 去重
  - 依赖 feature_flag: SHRIMP_001_DEDUP_ENGINE

files_changed:
  - internal/dedup/engine.go         # 修改

feature_flag:
  name: SHRIMP_001_DEDUP_ENGINE
  scope: method_level
  default_value: false

acceptance_criteria:
  - "Dedup 方法签名正确，编译通过"
  - "当 dedupEngineEnabled=false 时，输入 alerts 原样返回"
  - "当 dedupEngineEnabled=true 时，相同 (svc, type, host) 去重为 1 条"
  - "有 UT 覆盖"

incremental_test:
  unit_test: internal/dedup/engine_dedup_test.go
  test_command: go test ./internal/dedup/... -run TestDedup -v
  pass_criteria: "UT 全部通过，覆盖率不下降"

depends_on_increments:
  - INCR-001-2-1

blocking_increments:
  - INCR-001-2-3

commit_message_format: |
  [INCR-001-2-2] feat(dedup): 实现 Dedup 方法主体

  - feature_flag: SHRIMP_001_DEDUP_ENGINE（默认 false）
  - 关联 sub-task: TASK-001-2

reviewer_trigger:
  trigger: true
  reviewer_type: [codequality]
  focus_areas:
    - "Dedup 算法的正确性"
    - "feature_flag 使用的合理性"

status: pending

rollback:
  git_revert: "git revert {commit_hash}"
  flag_rollback: "set SHRIMP_001_DEDUP_ENGINE=false"
```
