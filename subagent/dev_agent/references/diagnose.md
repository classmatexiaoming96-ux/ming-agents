# diagnose：硬 bug / 性能回归的 6 步诊断（dev_agent / CC Lead）

> 被 dev_agent `execution_steps.md` 的修复回路（INCR-XXX-FIX）与错误重试路径引用。
> 来源：mattpocock/skills `diagnose`。与 CC Lead 的 `systematic-debugging` superpower 同向；本文件给出 shrimp 修复回路**必须强制走**的 6 步与门。
> **触发**：Reviewer 返回 Blocker / CI 失败 / INCR `status=error`（UT 失败）且一次直改未中。**禁止盲目重试**——一次直改未中即转本流程。

## 6 步（每步有门，未过不进下一步）

1. **建反馈环（核心，其余都是机械动作）**：先搞出一个**快、确定**的"通过/失败"信号——失败测试 > curl/HTTP 脚本 > CLI+fixture diff > headless 浏览器 > 重放 trace > 一次性 harness > 属性/fuzz > `git bisect run` > 差分(新旧对比) > 人在环 bash 脚本。把环当产品打磨（"2 秒确定性环 = 调试超能力"）。非确定性 bug：目标不是干净复现而是**提高复现率**（循环 100×/并行/注入 sleep）。**门：没有可信反馈环，不进第 2 步**（建不出就停下，列已尝试项，向用户要环境/制品/instrument 权限）。
2. **复现**：跑反馈环，亲眼看它失败；确认是**用户报的那个**失败模式、可重复、症状已捕获。**门：未复现不进第 3 步**（错的 bug = 错的修）。
3. **假设（3-5 个、排序、可证伪）**：先列 **3-5 个**带预测的假设，再去验任何一个（单假设会锚定第一个看似合理的想法）。每个写成"若 X 是因，则改 Y 会让 bug 消失"。**说不出预测的假设是 vibe，丢掉或锐化**。排序清单给用户看（便宜 checkpoint，AFK 不阻塞）。
4. **插桩（一次一个变量）**：每个探针对应第 3 步的一个预测；**一次只动一个变量**。优先 debugger/REPL > 边界处定点日志 > 绝不"全量打日志再 grep"。**每条调试日志打唯一前缀 `[DEBUG-xxxx]`**（清理时一条 grep 搞定）。性能：日志不可信，先立 baseline 再 bisect——**先测量后修**。
5. **修 + 回归测试**：**先写回归测试再修**——但仅当存在**正确的 seam**（在真实 bug 模式的调用点上断言）。seam 太浅会给假信心；**无正确 seam 本身就是发现**（记下来，标注架构妨碍锁定）。有 seam：复现转失败测试→看它失败→修→看它通过→在未最小化场景重跑反馈环。
6. **清理 + 复盘**：原始复现不再复现；回归测试通过（或记录其缺失原因）；`grep [DEBUG-` **全部删除**；一次性 prototype 删掉；**把正确假设写进 commit/PR**（让下一个调试者受益）。再问"什么能预防这个 bug"——若属架构问题，修进去后再处理。

## 与 shrimp 修复回路对接

- **错误重试**：INCR `status=error` 一次直改未中 → 不再盲目重试，按本 6 步驱动 CC Lead（fix prompt 注入本文件）。
- **Reviewer need_fix**：新建 `INCR-XXX-FIX`，其 prompt 走本 6 步；第 5 步的回归测试用 SPEC `TDD-XXX` 编号（见 `tdd_discipline.md`），转绿后 commit。
- **清理门并入完成判据**：`[DEBUG-` 残留 = 未完成，不得 commit / 交 Review。
