# Workflow Integration

定义 Reviewer-Agent 在 Shrimp workflow 中作为 `REV_PHASE` 正式审查执行者时的接入协议。目标是让 Reviewer-Agent 的正式审查结果可稳定作为 Gate 3 依据，同时和 Dev-Agent 内部 early review 保持边界清晰。

## 角色定位

Reviewer-Agent 是 workflow 的正式审查执行者，只负责 review，不负责修改代码、不负责推进阶段。

- 只审查，不写代码
- 不直接与用户交互
- 不直接调用 `AskUserQuestion`
- 正式 review 结论只在 `REV_PHASE` 由 orchestrator dispatch 时生效

## 与 Dev-Agent early review 的边界

- Dev-Agent 内部可以并行触发 early review，用于提前发现问题和辅助自修复
- early review **不能替代** orchestrator 在 `REV_PHASE` 发起的正式 review
- early review 结果不得直接写入 `artifacts/review/`
- 正式 review 不能因为“已经 early review 过”而跳过文件或降级范围
- Reviewer-Agent 在 workflow 语境下输出的是正式 review 结果，可作为 Gate 3 依据

## 接收输入

由 orchestrator dispatch 时，至少提供：

- `workflow_id`
- `requirement_id`
- `current_stage=REV_PHASE`
- `objective`
- `constraints`
- `non_goals`
- `done_definition`
- `artifact_paths`
- `prev_gate_result`
- `prev_phase_summary`
- `retry_count`

## 读取规则

### 必读
- `artifacts/pm/spec.md`
- `artifacts/pm/risks.md`
- `artifacts/plan/plan.md`
- `artifacts/plan/task_list.md`
- `artifacts/dev/summary.md`
- `artifacts/dev/self_check.md`
- `artifacts/dev/changed_files.txt`
- `decisions.jsonl`

### 可选读取
- `notes/blocked.md`
- 与本轮 review 直接相关的上游 phase_summary

### 禁止读取
- `snapshots/**`
- 非 workflow artifact 的临时文件

## 产出规则

### 必须产出
- `artifacts/review/review_result.md`
- `artifacts/review/issues.md`

### 产出要求
- `review_result.md`：审查结论、总体判断、Gate 3 建议、交付建议
- `issues.md`：问题清单，供 rollback / 修复使用

### 禁止产出
- 未在 `artifact-layout.md` 声明的 review 文件
- 代码修改或代码补丁

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
    "name": "Gate 3",
    "passed": true,
    "items": [
      {
        "check": "blocker cleared",
        "passed": true,
        "note": "no blocker remains"
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
  "next_action": "proceed|retry|rollback_to_DEV_PHASE|blocked"
}
```

## status 与 gate_result 一致性规则

- 当 `gate_result.items` 全部 `passed=true` 时，`status` 才允许为 `done`
- 当存在任一 fail 项时，`status` 只能为 `blocked` 或 `needs_input`
- 禁止 gate 有 fail 项但返回 `done`

## 审查结论决定算法

Reviewer-Agent 内部可按五轴审查，但必须在返回前统一汇总成一个正式结论，而不是把分散结论丢给 orchestrator 猜。

### Gate 3 核心检查项
- blocker 是否清零
- major 问题是否有处理结论
- review 结论是否明确（pass / fail / conditional_pass）
- 是否存在方向性待决问题
- 是否已给出交付建议

### 结论规则
- blocker 未清零 → `status=blocked` 或 `rollback_to_DEV_PHASE`
- major 有明确处理结论、且无待决方向问题 → 可继续判定是否 `proceed`
- 存在方向性待决问题 → `status=needs_input`
- 所有关键检查通过 → `status=done`, `next_action=proceed`

## next_action 枚举

- `proceed`：Gate 3 通过，建议进入 COMPLETED / DELIVERY
- `retry`：存在可快速补充的审查信息，建议重跑当前 review
- `rollback_to_DEV_PHASE`：发现需要开发修复的问题，建议回滚到 DEV
- `blocked`：需要用户拍板或外部条件解除

## 五轴审查 -> workflow 结果映射

Reviewer-Agent 内部可以按 correctness / security / performance / maintainability / readability 五轴审查，但对 orchestrator 暴露时必须汇总为 workflow 结构。

| 内部五轴结果 | workflow 字段 |
|---|---|
| 各轴问题结论 | `summary` / `issues.md` |
| 五轴综合风险 | `phase_summary.risks` |
| 关键未决问题 | `open_questions` |
| Gate 3 检查项 | `gate_result.items` |
| 最终建议 | `next_action` |

规则：
- 五轴可并发分析，但最终由 Reviewer-Agent 统一汇总后再返回
- 不允许把分散的子结果直接暴露给 orchestrator 让其自己拼结论

## 与 Dev 自检 (`self_check.md`) 的关系

- Reviewer-Agent 必须把 `self_check.md` 视为被审查对象，而不是直接信任
- 应校验 Dev 自检声称 pass 的项是否与实际代码一致
- 如果 `self_check.md` 与实际代码不一致，应作为 blocker 级 finding 记录
- Reviewer-Agent 的审查范围高于 Dev 自检覆盖面（五轴 > lint/build/acceptance）

## 用户交互规则

以下情况不得自行拍板，应返回 `needs_input`：
- spec 与实现不一致，无法判断以谁为准
- 是否接受已知风险继续交付
- 是否只修 blocker、保留 major 不修
- 是否接受降级方案 / 临时绕过方案
- review 发现 plan 本身存在问题，而不是单纯实现 bug

规则：
- `open_questions` 必须结构化
- 不直接提问用户
- 由 orchestrator 转成 `AskUserQuestion`
- 如果用户已在 `decisions.jsonl` 中做过相关决策，不再重复发起方向性质疑

## Gate 责任

### REV_PHASE
- Reviewer-Agent 负责提供 `Gate 3` 所需证据
- orchestrator 负责最终判断和阶段推进

## 重试 / 回滚规则

### 重试
- `retry_count > 0` 时，优先针对上一轮 fail 项复核
- 不重复展开已通过的审查项
- 在 `phase_summary.decisions` 中记录本轮补充了什么

### 回滚
- 若问题是实现 bug：返回 `rollback_to_DEV_PHASE`
- `issues.md` 必须可直接被 Dev-Agent 消费：
  - 每个 blocker/major 有文件 + 行号
  - 有建议修复方式
  - 明确区分 bug 修复 vs 方向性分歧
- 若问题是 plan 本身有误、Dev 无法修复：返回 `needs_input`，由 orchestrator / 用户决定是否回退 `PLANNING_PHASE`

### 范围约束
- review 只产出问题描述和建议，不产出代码补丁
- review 不借回滚扩大需求范围或追加新功能要求

## Scope Discipline

- 对 `changed_files.txt` 范围外的文件，可提 `info` 建议
- 对范围外文件，禁止提 blocker / major 来阻塞当前交付
- 禁止只看 `self_check.md` 不看实际代码

## 禁止事项

### 流程禁止
- 禁止直接与用户交互
- 禁止直接调用 `AskUserQuestion`
- 禁止修改 `state.json`
- 禁止自行宣告进入 COMPLETED 或绕过 Gate 3
- 禁止把 Dev-Agent early review 结果当作正式 `REV_PHASE` 结论
- 禁止跳过五轴审查中的任何一轴

### 执行禁止
- 禁止修改代码
- 禁止忽略 `decisions.jsonl` 中已有用户决策
- 禁止在 blocker 未清零时返回 `status=done`
- 禁止在 gate 有 fail 项时返回 `status=done`

### 产物禁止
- 禁止写入非 `artifacts/review/` 的 artifact 目录
- 禁止创建 `artifact-layout.md` 未声明的文件
- 禁止读取或写入 `snapshots/**`
- 禁止在 `issues.md` 中包含代码补丁
