# Workflow Integration

定义 Dev-Agent 在 Shrimp workflow 中作为 `DEV_PHASE` 执行者时的接入协议。目标是让 Dev-Agent 在不直接写代码的前提下，通过 CC Lead 稳定完成实现、自检、回滚和结果回传。

## 角色定位

Dev-Agent 是 workflow 的阶段执行者和 CC Lead 的桥接层，不是代码直接执行者，也不是阶段调度者。

- 不直接写代码
- 所有代码操作通过 CC Lead 执行
- 不直接与用户交互
- 不直接调用 `AskUserQuestion`
- 通过结构化返回结果，把状态、风险和问题交回 orchestrator

## 接收输入

由 orchestrator dispatch 时，至少提供：

- `workflow_id`
- `requirement_id`
- `current_stage=DEV_PHASE`
- `objective`
- `constraints`
- `non_goals`
- `done_definition`
- `artifact_paths`
- `prev_gate_result`
- `prev_phase_summary`
- `retry_count`
- 如回滚修复：`rollback_artifact_path`、`rollback_reason`、`source_phase_summary`

## 读取规则

### 必读
- `artifacts/pm/spec.md`
- `artifacts/pm/risks.md`
- `artifacts/plan/plan.md`
- `artifacts/plan/task_list.md`
- `decisions.jsonl`

### 回滚场景额外读取
- `artifacts/review/issues.md`
- 回滚来源阶段的 `phase_summary`

### 禁止读取
- `snapshots/**`
- 与当前 increment / task 无关的临时文件

## 内部产物 / CC Lead 输出 -> workflow artifact 映射

Dev-Agent 内部或 CC Lead 可能产出多种执行记录，workflow 只认固定 artifact：

| 内部/中间产物 | workflow artifact |
|---|---|
| increment 执行总结 / 开发总结 | `artifacts/dev/summary.md` |
| lint / build / acceptance 检查 | `artifacts/dev/self_check.md` |
| 改动文件清单 | `artifacts/dev/changed_files.txt` |

规则：
- orchestrator 只认 workflow artifact 路径
- CC Lead 或 Dev-Agent 的中间输出如仍保留，只能作为过程日志，不能替代 workflow artifact

## 产出规则

### 必须产出
- `artifacts/dev/summary.md`
- `artifacts/dev/self_check.md`
- `artifacts/dev/changed_files.txt`

### 额外要求
- `self_check.md` 必须能支撑 Gate 2 的硬检查
- `changed_files.txt` 必须非空且与本轮实际改动一致
- 若本轮无可交付代码变更，不得伪造 artifact

## 统一返回结构

完成后必须返回与 `dispatch-prompt.md` 一致的结构：

```json
{
  "status": "done|blocked|needs_input",
  "summary": "...",
  "phase_summary": {
    "decisions": [],
    "open_issues": [],
    "risks": [],
    "handoff_note": "..."
  },
  "artifact_updates": {},
  "gate_result": {
    "name": "Gate 2",
    "passed": true,
    "items": [
      {
        "check": "lint pass",
        "passed": true,
        "note": "golangci-lint clean"
      }
    ]
  },
  "open_questions": [
    {
      "question": "...",
      "options": [
        {
          "label": "A",
          "description": "...",
          "impact": "..."
        }
      ],
      "impact": "...",
      "default": null
    }
  ],
  "next_action": "proceed|retry|rollback_to_PLANNING_PHASE|blocked"
}
```

## status 与 gate_result 一致性规则

- 当 `gate_result.items` 全部 `passed=true` 时，`status` 才允许为 `done`
- 当存在任一 fail 项时，`status` 只能为 `blocked` 或 `needs_input`
- 禁止 gate 有 fail 项但返回 `done`

## next_action 枚举

- `proceed`：所有检查通过，建议进入 REV_PHASE
- `retry`：存在可自动修复失败项，建议重试
- `rollback_to_PLANNING_PHASE`：发现 plan 不可行，建议回滚
- `blocked`：需要用户决策或外部条件解除

## CC Lead increment 结果 -> workflow 结果聚合规则

Dev-Agent 通过 CC Lead 执行代码后，必须把 increment 级结果聚合为 workflow 返回结构。

### 单个 increment 的典型结果

```json
{
  "status": "success|error|need_decision",
  "files_changed": ["internal/foo/bar.go"],
  "test_results": {
    "passed": 5,
    "failed": 0,
    "coverage": 82.1
  },
  "commits": ["abc1234"],
  "errors": [],
  "warnings": []
}
```

### 聚合规则

| 聚合维度 | 聚合方式 | 写入位置 |
|---|---|---|
| increment status | 全部 success → `done`；任一 error 未修复 → `blocked`；任一 need_decision → `needs_input` | `status` |
| files_changed | 去重合并 | `artifacts/dev/changed_files.txt` |
| test_results | 汇总通过/失败/覆盖率 | `artifacts/dev/self_check.md` |
| commits | 列出所有 commit | `artifacts/dev/summary.md` |
| errors / warnings | 汇总为已知风险与问题 | `phase_summary.risks` |
| need_decision | 转为结构化问题 | `open_questions` |

## CC Lead / 内部协议 -> workflow 返回结构映射

Dev-Agent 可内部继续使用既有协议，但对 orchestrator 暴露时必须统一转换：

| 内部结果 | workflow 字段 |
|---|---|
| 开发状态（成功/失败/需决策） | `status` |
| 本轮开发总结 | `summary` |
| 风险 / 未决问题 / handoff 信息 | `phase_summary` |
| 实际写入 artifact 路径 | `artifact_updates` |
| 自检结果 / Gate 2 检查项 | `gate_result.items` |
| 需要用户拍板的问题 | `open_questions` |
| 下一步建议 | `next_action` |

规则：
- 对 orchestrator 暴露的最终结果必须以 workflow 返回结构为准
- 不允许把 CC Lead 原始输出直接当最终返回结果

## 用户交互规则

以下情况不得自行拍板，应返回 `needs_input`：
- 技术路线存在多个可行实现方案
- 需要接受明确风险继续开发
- 回滚代价高，需要用户确认
- 需求范围变化或与 spec 冲突
- review 提出方向性调整而非单纯 bug 修复
- CC Lead 返回 `need_decision`

规则：
- `open_questions` 必须结构化
- 不直接提问用户
- 由 orchestrator 转成 `AskUserQuestion`

## Gate 责任

### DEV_PHASE
- Dev-Agent 负责提供 `Gate 2` 所需证据
- orchestrator 负责最终判断和阶段推进

Gate 2 的硬检查至少应覆盖：
- lint 结果
- build 结果
- acceptance criteria 对照结果
- changed files 非空
- commit / push 状态
- 实现范围未超出 plan

## 重试 / 回滚规则

### 重试
- `retry_count > 0` 时，优先修复上一轮失败项
- 不从零重做已通过部分
- 若可通过精确失败项修复，则不要重跑全部开发流程

### 回滚
- 回滚修正只解决 review/issues 或上游指出的问题
- 不借机做全面重构
- 不得扩大需求范围或重定义 spec / plan
- 如果回滚修正本身需要用户取舍，返回 `needs_input`

## 与 Reviewer Agent 的关系边界

Dev-Agent 内部可以并行触发 Reviewer Agent 做早期 review，但这只用于提前发现问题和辅助自修复，**不能替代** orchestrator 在 `REV_PHASE` 发起的正式 review。

规则：
- 内部 increment review = 自检优化，不是正式 `REV_PHASE`
- Dev-Agent 返回时只能携带自检结论，不能把内部 review 结论当作 Gate 3 证据
- 是否进入 `REV_PHASE` 只能由 orchestrator 决定
- 最终正式 review 结论只能来自 orchestrator dispatch 的 `REV_PHASE`

## 禁止事项

### 流程禁止
- 禁止直接与用户交互
- 禁止直接调用 `AskUserQuestion`
- 禁止修改 `state.json`
- 禁止自行宣告进入下一阶段或绕过 Gate

### 执行禁止
- 禁止 Dev-Agent 自己写代码
- 禁止绕过 CC Lead 直接修改代码文件
- 禁止忽略 `decisions.jsonl` 中已有决策
- 禁止在未完成自检时返回 `status=done`

### 产物禁止
- 禁止写入非 `artifacts/dev/` 的 artifact 目录
- 禁止创建 `artifact-layout.md` 未声明的文件
- 禁止读取或写入 `snapshots/**`
