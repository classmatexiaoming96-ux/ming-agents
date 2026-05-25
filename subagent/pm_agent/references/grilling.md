# 逼问式需求对齐（grilling）：pm-n0a brainstorming 纪律

> 被 pm-n0a 的 brainstorming CC Lead prompt 引用。
> 来源：mattpocock/skills `grill-me` / `grill-with-docs`。
> **落点说明**：pm_agent 自身不与用户对话，逼问只能发生在 **CC Lead brainstorming session**（tmux + claude，可与用户多轮交互）；pm_agent 只负责把本纪律写进 pm-n0a prompt 下发。

## 三条核心规则

1. **逐题逼问、走决策树**：就方案/需求的每一处不确定，**一次只问一个问题**，沿决策树逐枝深入，逐个消解依赖（先问会影响后续分支的决策）。**每问一题，等用户答复后再问下一题**，不要一次抛一串。
2. **每题附带你的推荐答案**：每个问题都给出"我推荐 X，因为 …"，让用户在你的推荐上做反应（确认/推翻），而不是从空白起步。
3. **能查代码就别问**：凡是读代码/读文档能查到的，**先去查**，不要拿来问用户。只把真正需要人决策的开放问题留给用户。

## 与 CONTEXT.md 协同（统一语言 · 即 C2，见 ./context_glossary.md）

- **启动注入**：若该仓库已有 `{worktree}/CONTEXT.md`，brainstorm 前读入并注入 prompt，逼问时直接引用规范词。
- **边问边沉淀**：每澄清/锐化一个术语，**立即**写回 `CONTEXT.md`（懒创建，不批量囤积）：
  - 术语与词表冲突 → 当场点出："你词表里 X 定义是 A，但你这里像是 B，到底哪个？"
  - 模糊词 → 提议规范词："你说的'账号'指 Customer 还是 User？"
  - 与代码矛盾 → 摆出来："代码里取消的是整单，但你说支持部分取消，哪个对？"
- **anti-scope-creep**：要引入 PRD/代码之外的"新机制"，先停下问用户，别自造（ShardingHashACK→对账触发器、Layer1/Layer2 即反例）。
- **边决策边记 ADR**：逼问中拍板的决策若满足三要素（难撤销+反直觉+真权衡），当场落一条 `{worktree}/docs/adr/NNNN-*.md`（见 `./adr.md`）；缺一不写。

## HARD GATE

用户 approve 方案前不进入任何实现/设计落地。产出 `docs/idea_refinement.md` + 更新 `CONTEXT.md` 后，终端打印 `PM_CC_LEAD_DONE: docs/idea_refinement.md`。
