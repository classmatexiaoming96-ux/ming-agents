# mattpocock/skills × shrimp-graph 对比分析报告

> 生成日期：2026-05-25
> 对比对象：mattpocock/skills（/tmp/test-clone）× shrimp-graph（/root/.hermes/workspace/shrimp）

## 0. 一句话结论

两者**不在同一层**：shrimp-graph 是"流程框架"（owns the process — 状态机/Gate/Subagent 隔离/SPEC 驱动），mattpocock/skills 是"技术手法库"（composable techniques — 刻意反对 GSD/BMAD/Spec-Kit 这类重框架）。所以**几乎没有可"整体替换"的部分，但有大量"手法"可注入 shrimp 的薄弱环节**。最有价值的四个手法：`grill`（逼问式对齐）、`CONTEXT.md`（统一语言词汇表）、`tdd`（红绿重构+垂直切片）、`diagnose`（6 步调试）。其中**统一语言词汇表直接命中已记录在案的真实事故**（ShardingHashACK 被误"升级"成对账触发器）。

---

## 1. 现状描述：shrimp-graph skill 体系

存在**两代并存**：

| 层 | 旧代 `.agents/skills/shrimp-*.md`(v1-2) | 当前代 `subagent/*/SKILL.md`(v4-7) |
|---|---|---|
| PM | shrimp-pm-review(AIME,7步,Gate1=6条) | pm_agent v4.2(idea-refine→parse→并行研究→SPEC生成,Gate1.5=11条) |
| PLAN | 无(并入PM) | pm_agent plan-n0~n3(task_breakdown+依赖图+rollback+failure_hypothesis) |
| DEV | shrimp-dev-code(设计→拆Task→编码,**test-after**) | dev_agent v5.4(桥接CC Lead, Contract-First, 15-45min增量) |
| REVIEW | shrimp-reviewer-check(7维4级,**串行**,人工Gate) | reviewer_agent v7.1(**5轴并发 Codex**审查) |
| DEPOSIT | 无(orchestrator内联) | deposit_agent v4.0(知识沉淀) |
| 编排 | shrimp-orchestrator(串行状态机,仅PM/DEV/REV) | graph.py(可并行,+PLANNING+DEPOSIT) |
| 文档 | 无 | doc_output v2.6(并行抓取+冲突仲裁+飞书画板升级) |

**体系强项**：SPEC 驱动（CONTRACT/EDGE/ERROR/TEST 条款）、Gate 量化（rollback 覆盖≥80%、测试≥80%、dev_confirmed≥80%）、并行研究+并行 5 轴评审、Subagent 隔离、anti-scope-creep 硬约束、知识沉淀。

**已识别薄弱环节**：
1. **全程无统一语言/词汇表**（entity 只在 pm-n0b "识别"，无跨阶段共享词表）
2. **ADR 在迁移中退化**（旧 pm-review 有"决策记录表§6"，新 pm_agent 没有，决策只散落在 conflicts.md）
3. **无 TDD，test-after**（dev-code Step4 先生成代码再补测试；SPEC 有 TDD-XXX 编号但无机制保证先写/被执行）
4. **需求可追溯链断裂**（无单一 artifact 串起 PRD FR-ID→SPEC 条款→子任务→测试→评审发现）
5. **调试方法不均**（只有 plan-n1 加载 systematic-debugging；评审发现 bug 时只回退 DEV，无复现/诊断流程）
6. **Gate 机制偏"画图"**（G1.5.7~11 多数管 mermaid 存在性/禁 ASCII art/飞书可渲染，是表现质量而非需求正确性）

---

## 2. 对比分析（逐维度）

### A. 可直接替换的部分 — 基本没有，不建议整体替换

| shrimp 组件 | mattpocock 对应 | 重合度 | 替换判断 |
|---|---|---|---|
| 编排/Gate/状态机/SPEC | (无对应) | 0% | mattpocock 刻意不做这层，无可替换物 |
| reviewer_agent 5轴并发 | `review`(in-progress, 2轴并行) | 部分 | shrimp 更强，**不替换** |
| shrimp-dev-code 的 test-after 子行为 | `tdd` 红绿重构 | 高 | **唯一值得换掉子行为的点**，预计变好 |
| pm_agent 批量 `questions_for_user` | `grill-me` 逐题逼问 | 中 | 不整体替换（见 E 落点问题），作增强 |

结论：mattpocock 是手法库、shrimp 是流程框架，分属不同层。整体替换会丢掉 shrimp 核心价值（编排+SPEC+并行+隔离），预计变差。

### B. 可吸收借鉴的部分（增强，不替换）— 报告重点

| mattpocock 手法 | 注入 shrimp 的位置 | 解决的薄弱环节 |
|---|---|---|
| **`grill` 逼问式对齐**（逐题、每题给推荐答案、能查代码就别问） | pm-n0a brainstorming 的 CC Lead prompt | #4 需求对齐偏批量/浅 |
| **`CONTEXT.md` 统一语言词汇表**（opinionated 单一规范词+别名表、纯glossary、grilling 中增量更新） | 新增 repo 级跨阶段 artifact，pm/dev/review 全程引用 | #1 无词汇表 + 预防 ShardingHashACK 类事故 |
| **`ADR` 三要素准入门**（难撤销+反直觉+真权衡，缺一不写；可一句话） | 恢复 pm-n2 决策记录为独立 ADR 文件 | #2 ADR 退化 |
| **`tdd` 红绿重构 + 垂直切片/曳光弹**（禁"先全测后全码"，一测一码） | dev_agent 的 CC Lead dev prompt + SPEC 的 TDD-XXX 接成"先写失败测试" | #3 test-after |
| **`diagnose` 6步调试**（反馈环优先→复现→3-5可证伪假设→单变量探针→带前缀debug日志→回归+清理） | 新增 DEBUG 能力：评审/CI 发现 bug 时驱动 CC Lead | #5 调试缺方法 |
| **可证伪多假设**（3-5个排序、每个带可证伪预测） | 升级现有 plan-n1 的 `failure_hypothesis` 格式 | #5（协同既有能力） |
| **`write-a-skill` 渐进式披露**（description即契约/SKILL<100行/确定性逻辑下沉脚本） | 瘦身 pm_agent 等超长 SKILL 正文（300+行） | 维护性 + #6 |
| **AFK/HITL 标注 + 曳光弹垂直切片** | task_breakdown/增量加"端到端可演示"判据 + AFK/HITL 标 | 增量质量 |

### C. shrimp 独有且有价值（mattpocock 没有）

1. 流程编排状态机 + 不可绕过 Gate + 回退次数限制（REV↔DEV≤3, REV→PM≤1）
2. SPEC 驱动开发（CONTRACT/EDGE≥3/ERROR码/TEST分层）——比 mattpocock 的 PRD 严格得多
3. 并行研究 + 并行 5 轴 Codex 评审
4. Subagent 隔离 + CC Lead(cc-start.py)驱动本机 Opus
5. doc_output：并行抓取、冲突仲裁、飞书 docx + mermaid→可交互画板升级
6. anti-scope-creep 硬约束（带真实事故记录）
7. 知识沉淀 deposit_agent（6 类知识, 去重>80%）
8. 量化 Gate（覆盖率/通过率/确认率阈值）——mattpocock 全是定性手法

### E. 潜在风险

1. **哲学冲突**：mattpocock 明确反对"流程拥有型框架"，强调人驱动/可组合/轻量；shrimp 是自动化/Gate 驱动/Subagent 隔离。给重框架再加料有膨胀风险，与所借的"精简"理念自相矛盾。**对策：只借手法不借架构，借入时同步瘦身。**
2. **grilling 落点问题（关键兼容性）**：`grill` 依赖 agent 直接与用户逐题对话，但 pm_agent 明确不直接对话用户（走 `questions_for_user` 给 Orchestrator）。所以 grilling 不能直接塞进 pm_agent，必须落在能经 tmux 与用户多轮对话的 **CC Lead brainstorming session**（pm-n0a 已有此通道）。
3. **CONTEXT.md 生命周期错配**：mattpocock 的 CONTEXT.md 是 repo 级/跨需求/长生命周期活文档；shrimp 的 `docs/` 产物是需求级/一次性。需新增 repo 级存储位 + "懒更新"维护约定，否则词汇表无法跨需求复用。
4. **TDD 顺序冲突**：tdd 是 test-first；shrimp-dev-code 是"先设计后编码+编码后自检"。需调和成"设计→红(写失败测试)→绿(最小实现)→重构"，而非简单叠加。
5. **语言/技术栈/文化适配**：mattpocock 是英文+TS+GitHub issues；shrimp 是中文+多栈(Go等)+飞书。词汇表格式、ADR 目录、issue/triage 需本地化（triage/to-issues 强依赖 GitHub label，对飞书/Meego 价值低）。

---

## 3. 改进建议清单（带优先级）

| # | 优先级 | 建议 | 落点 | 预计效果 |
|---|---|---|---|---|
| 1 | **P0** | 引入 `CONTEXT.md` 统一语言词汇表，repo 级跨阶段 artifact；pm-n0b 产初版，grilling 增量更新，dev/review 引用 | pm_agent + 新存储位 | 预防 ShardingHashACK 类术语事故；降思考 token、统一命名 |
| 2 | **P0** | 把 `grill` 三规则（逐题/每题带推荐答案/能查代码就查）注入 pm-n0a brainstorming 的 CC Lead prompt | pm-n0a prompt | 需求对齐由"批量浅问"→"逼问式收敛" |
| 3 | **P0** | 把 `tdd` 红绿重构+垂直切片纪律写进 dev CC Lead prompt，SPEC 的 TDD-XXX 接成"先写失败测试" | dev_agent prompt + SPEC 衔接 | test-after→test-first，测试测行为而非实现 |
| 4 | **P1** | 新增 `diagnose` 6步调试能力：评审 Blocker/CI 失败时驱动 CC Lead 走"反馈环优先→可证伪假设→单变量→带前缀日志→回归" | 新 DEBUG 节点/review-fix 回路 | "盲目回退 DEV"→有方法的诊断 |
| 5 | **P1** | 用 `ADR` 三要素准入门恢复决策记录为独立 `docs/adr/NNNN-*.md` | pm-n2 | 补回迁移丢失的 ADR，不过度官僚 |
| 6 | **P1** | 升级 plan-n1 的 `failure_hypothesis` 为"3-5个排序、每个带可证伪预测"格式 | pm_phases plan-n1 | 与既有能力协同，假设质量提升 |
| 7 | **P2** | 按 `write-a-skill` 渐进式披露给超长 SKILL 正文瘦身（<100行主干+references） | 各 SKILL.md | 维护性；缓解 #6 |
| 8 | **P2** | task_breakdown/增量加"端到端可演示(曳光弹)"判据 + AFK/HITL 标注 | plan-n1/增量拆分 | 增量更可独立验收 |
| 9 | **P2** | 重新平衡 PM Gate：把部分"画图存在性"门(G1.5.7~11)降权，让位给需求正确性门 | graph.py Gate1.5 | 纠正"画图门多于正确性门" |

---

## 4. 推荐行动计划

**Wave 1（P0，主攻"需求对齐"这一记录在案的头号失败）**
- 建议 1 + 2 一起做（词汇表 + 逼问式对齐都打需求对齐）。用 ShardingHashACK 事故做词汇表样板，说服力最强。
- 建议 3：tdd 注入 dev prompt（改动小、收益大，SPEC 已有 TDD-XXX 锚点）。

**Wave 2（P1，补"调试+决策"短板）**
- 建议 4（diagnose 调试能力）+ 5（ADR 恢复）+ 6（假设升级）。三者增强既有流程，不动架构。

**Wave 3（P2，体系卫生）**
- 建议 7/8/9：SKILL 瘦身、增量垂直切片/AFK标注、Gate 再平衡。

**不建议做**：直接搬 `triage`/`to-issues`/`to-prd`（强依赖 GitHub label，与飞书/Meego 错配）；不用 mattpocock 整体替换任何 shrimp 编排/SPEC/评审组件。
