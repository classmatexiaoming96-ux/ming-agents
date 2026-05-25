# ADR 写作规范与三要素准入门（pm_agent）

> 被 pm-n2 / pm-n0a grilling / 冲突裁决引用。
> 来源：mattpocock/skills `grill-with-docs` 的 ADR-FORMAT。落点：决策由 CC Lead 在产文档/收敛时记录（pm_agent 自己不落笔正文）。
> 背景：旧 `shrimp-pm-review` 有"决策记录表 §6"，迁移到当前 pm_agent 时丢失——本文件把 ADR 作为一等产物恢复。

## 存放位置与生命周期

- **位置**：代码仓库 worktree 的 `{worktree}/docs/adr/NNNN-slug.md`（**repo 级、跨需求**，与 CONTEXT.md 同属仓库长生命周期资产）—— **不是** shrimp 的需求级 `docs/`。
- **编号**：顺序 4 位，如 `0007-hdel-pending-delete-per-element.md`。
- **谁写**：CC Lead（读代码、做决策的一方）。

## 三要素准入门（缺一不写）

仅当**同时**满足三条才写 ADR，否则跳过：
1. **难撤销**：一旦落地反悔代价高（数据格式、对外契约、技术锁定）。
2. **反直觉**：不写下来，后人/后续 agent 会觉得意外、想推翻。
3. **真权衡**：是在多个真实选项间权衡的结果（不是唯一选择）。

> 三者缺一 → 不写。ADR 是"防止重新争论已定之事"，不是流程负担。

## 格式（可短至一段）

```markdown
# ADR-0007: {决策标题}
- 状态：accepted | superseded by ADR-XXXX
- 日期：YYYY-MM-DD

{一段话：做了什么决定、为什么。价值在于记录"做了决定"+"为什么"，不在于填满章节。}

## 备选与放弃理由（可选，有价值才写）
- 选项 B：… 放弃因 …

## 后果（可选）
- …
```

最小可用：标题 + 状态 + 日期 + 一段理由即可。

## 何时触发记录

- **pm-n0a grilling**：澄清出一个满足三要素的决策时当场记一条（grill-with-docs 的"边决策边记"）。
- **pm-n2 汇总**：tech_review/SPEC 里每个"选了 A 而非 B"的架构/选型决策，过三要素门 → 记 ADR；tech_review 决策处引用 `ADR-XXXX`。
- **冲突裁决**：`conflicts.md` 里用户裁决的冲突，若满足三要素 → 落一条 ADR，把"为什么这么裁"固化。

## 什么算 ADR-worthy（参考）

架构形态 / 集成模式 / 锁定型技术选型 / 边界与范围决策 / 有意偏离惯例 / 不可见约束 / 非显然的被否决方案。
