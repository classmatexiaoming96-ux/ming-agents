# AFK / HITL 节点分类（pm / dev / reviewer）

> 被 `pm_phases.md` / `dev_agent/references/execution_steps.md` / `reviewer_agent/references/reviewer_phases.md` 引用。
> 来源：mattpocock/skills `triage` / `to-issues` 的 AFK/HITL 区分。
> 落点：让 Orchestrator 据此决定是否要等用户、Reviewer 是否给 "AFK-mergeable" 自动放行。

## 三类节点

- **AFK**（Away From Keyboard，无人值守）：可在用户不在时跑完；CC Lead session 内部完成，无 `askUserQuestion`、无 brainstorming HARD GATE；产物自检通过即过。
- **HITL**（Human In The Loop，需人工介入）：必须有用户实时回答；包含 brainstorming HARD GATE / 冲突裁决 / 需求澄清 / 关键设计审批。
- **Mixed**（混合）：主体 AFK，但触发条件下转 HITL（如 `questions_for_user` 非空、错误重试 2 次仍失败、Reviewer Blocker、Major 决策超过用户授权阈值）。

## 标注约定

- **节点级**：每个 phase 节点小节开头加一行 `> **节点性质**：AFK | HITL | Mixed（一句话理由）`。
- **task_breakdown schema**（可选增强）：sub_task 增加 `interaction_mode: afk | hitl | mixed` 字段（与既有 `execution_mode: serial | parallel` 正交：前者是"要不要人"，后者是"能否并行"）。

## 推荐用法

- **Orchestrator 路由**：HITL 节点 dispatch 前确认用户在场；AFK 节点可后台执行（夜里跑也行）。
- **Reviewer 自动放行**：增量审查路径全部 AFK 节点 + need_fix=0 + Blocker=0 → 可直接放行 merge；任一 HITL 必留 user signoff。
- **AFK 不等于免自检**：所有自检清单仍跑（Mermaid / SPEC / 统一语言 / TDD），自检失败即转 Mixed（升级请求人）。
