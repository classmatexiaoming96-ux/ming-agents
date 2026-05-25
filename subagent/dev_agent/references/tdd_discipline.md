# TDD 纪律：Red-Green-Refactor + 垂直切片（dev_agent / CC Lead）

> 被 dev_agent `SKILL.md` 原则 11 与 `execution_steps.md` 的 INCR prompt 引用。
> 来源：mattpocock/skills `tdd`（red-green-refactor / 垂直切片-曳光弹 / 测公开接口不测实现），本地化对接 shrimp 的 increment + SPEC `TEST_STRATEGY`（TDD-XXX）。

## 为什么需要它（修正点）

原则 11 早已声明"先写失败测试再写实现"，但执行层一度是 **test-after**：
- 增量循环写成"实现 → UT → 本地测试"；
- 拆分把 UT 当成独立的后置增量（Type-C 实现 → Type-E 测试）。

本纪律把 test-first 落到**每个实现型增量内部**，并接上 SPEC 的 `TDD-XXX` 测试编号。

## Red-Green-Refactor（每个实现型 INCR 内部）

1. **🔴 Red**：先写**一个**会失败的测试，针对本 INCR 要交付的**一个**行为；运行，确认它因"功能未实现"而失败（不是编译错/拼写错）。
2. **🟢 Green**：写**刚好够**让该测试通过的最小实现；运行，确认转绿。
3. **♻️ Refactor**：仅在绿灯下重构（去重、加深模块、SOLID）；每步重跑测试。**红灯时禁止重构 —— 先回到绿。**

## 垂直切片 / 曳光弹（禁止水平切片）

- **一测一码，逐个推进**：test1→impl1 → test2→impl2 …。**禁止**"先把所有测试写完再写所有实现"（水平切片会测想象中的行为、对真实改动不敏感）。
- 每个 INCR = 一条端到端可验证的薄切片（穿过接口的真实调用路径），完成即可独立演示/回归。
- 对接 increment 模型：一个实现型 INCR 只承载"红→绿"的**一个**行为切片；多行为拆成多个 INCR。

## 测行为，不测实现

- 测试只经**公开接口**断言可观测行为（"用户能用有效购物车结账"），不 mock 内部协作者、不测私有方法、不绕过接口直查 DB。
- 判据：**重命名一个内部函数若导致测试失败，则该测试测的是实现而非行为** —— 重写它。
- 好处：实现可整体替换而测试不动。

## 接上 SPEC TEST_STRATEGY（TDD-XXX）

- SPEC.md 的 `TEST_STRATEGY` 列出 `TDD-XXX` 测试编号；`increment_plan` 为每个实现型 INCR 标注它要让哪些 `TDD-XXX` 转绿。
- INCR prompt 把 `tdd_test_ids` 传给 CC Lead；CC Lead **先写这些编号对应的失败测试（红）**，再实现（绿）。
- 完成门：本 INCR 声明的 `tdd_test_ids` 全部由红转绿，且 `must_pass_test_before_commit` 满足后才 commit。

## 每轮自检

```
[ ] 测试描述的是行为，不是实现
[ ] 仅经公开接口断言（不 mock 内部协作者 / 不测私有方法）
[ ] 内部重构后该测试仍应通过
[ ] 实现是让测试通过的最小量，无投机性功能
[ ] 红灯期间未做任何重构
[ ] 本 INCR 声明的 tdd_test_ids 已全部由红转绿
```
