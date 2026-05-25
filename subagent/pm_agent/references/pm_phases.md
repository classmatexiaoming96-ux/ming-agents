# PM-Agent 执行步骤详解

## pm-n0（含 idea-refine）

**输入（来自 Orchestrator）**：
- `requirement_id`：需求 ID（如 SHRIMP-001）
- `prd_path`：PRD 文档路径
- `pm_agent_config`：**从 TASK_BOARD.md 读取**
- `workspace_dir`：工作目录
- `requirement_type`：（可选）传入 `rough_idea` 时强制触发 idea-refine

### pm-n0a：Idea Refinement（触发条件：需求模糊）

**触发条件判断**（满足任一）：
1. PRD 正文章节字数少于 200 字
2. 包含模糊词汇：`大概`、`想做`、`看看能不能`、`初步想法`、`可能要`、`考虑一下`、`探索`、`试试`
3. 不包含任何技术约束描述（无"必须"、"约束"、"不能"等词）
4. `requirement_type: "rough_idea"`

**执行**：
1. 读取 PRD 内容
2. 通过 `cc-start.py` 启动 CC Lead session（见 SKILL.md《CC Lead 调用规范》五步流程；**不用** 未实现的 `sessions_spawn`）
3. 在 prompt 文件中注入 `brainstorming` superpower（brainstorming 会自动做需求澄清 + 方案选型 + 用户 approval）；cc-start.py 走 tmux + `claude` stdio，brainstorming 能正常向用户提问
4. 用 `cc-capture.py` 轮询 CC Lead 完成 brainstorming HARD GATE 流程（用户 approve 设计后 CC Lead 打印 `PM_CC_LEAD_DONE:`）
5. CC Lead 产出 `docs/idea_refinement.md`（核心目标 / 约束条件 / 开放问题）

**⚠️ 硬约束 — 禁止自主加料**：

> CC Lead 在生成 `idea_refinement.md` 时，**不得**在原始 PRD / tech_review.md 需求之外自主添加新机制、新策略或新接口。
>
> 如果 CC Lead 认为需要补充内容（如"引入对账机制"、"新增接口"等），必须：
> 1. 暂停自动流程
> 2. 向用户明确提出："我理解 PRD 中需要补充 XXX，是否确认需要？"
> 3. 获得用户明确回复后再继续
>
> **已发生的错误案例**：pm-n0a 中 Q5 的 ShardingHashACK 被错误"升级"为对账触发器，导致 Layer1/Layer2 等不存在的机制被设计出来，必须回退重做。
>
> **目的**：防止 CC Lead 自主发挥导致需求蔓延和返工。
>
> **结构性防线（统一语言）**：该类"升级"的根因是术语无权威定义。pm-n0b 产出的 `{worktree}/CONTEXT.md` 为每个领域术语固定 `Not(不是)` + `锚点(代码出处)` 两字段——ShardingHashACK 若早有 `Not：不是对账触发器` 词条 + 代码锚点，引用时即可拦截升级。brainstorming 启动时：**若该仓库 `CONTEXT.md` 已存在（既往需求沉淀）→ 在 prompt 中注入其内容供 CC Lead 引用**；尚不存在时，pm-n0a 中新冒出的领域术语先记入 `CONTEXT.md` 的 `⚠️ 待澄清` 区，待 pm-n0b bootstrap 落库。写作规范与样板见 `./context_glossary.md`。

**⚠️ 为什么必须用 cc-start.py（tmux + claude）而不是 sessions_spawn**：

> **历史问题**：`sessions_spawn(runtime="acp")` 从未被实现（仓库内 grep 0 命中），且即使存在，其 subagent 传输层走 MCP 协议，**无法执行 askUserQuestion**（HARD GATE 的人机交互节点会被静默跳过），导致 pm-n0a 无法收敛。过去 pm_agent 因找不到 sessions_spawn 而每次直接返回 `status: done` 空跑。
>
> **正确方式（已落地）**：用 `cc-start.py` 启动 CC Lead —— 它本质就是 `tmux + claude`，走 stdio：
> 1. brainstorming 通过终端输出向用户提问（不是 askUserQuestion），用户在 tmux 里回答
> 2. 读 `~/.claude/settings.json` 默认模型（本机 Opus），不像 `delegate_task` 那样模型不可控
> 3. CC Lead 产出 `docs/idea_refinement.md` 后，pm_agent capture 到 `PM_CC_LEAD_DONE:` 再继续 pm-n0b
>
> **标准调用**（详见 SKILL.md《CC Lead 调用规范》）：
> ```bash
> CC=/root/.hermes/skills/openclaw-imports/cc-lead/scripts
> python3 "$CC/cc-start.py" \
>   --worktree-path "{workspace_dir}" \
>   --job-id "{repo}:{branch} pm-n0a" \
>   --prompt-file "/tmp/{safe_id}_pm-n0a_prompt.md" \
>   --tmux-session "{safe_id}-pm"
> # 然后用 cc-capture.py 轮询，用 cc-send.py 追加澄清
> ```

**brainstorming 集成说明**：
- PM-Agent 不做内部结构化收敛，而是托付给 CC Lead + brainstorming superpower
- brainstorming 的 9 步流程会自动与用户多轮交互（tmux 终端交互 → 用户选择 → 深入设计）
- **逼问式纪律（grilling）**：CC Lead 按 `./grilling.md` 三规则收敛——逐题逼问走决策树、每题附推荐答案、能查代码就别问；并边问边把术语写回 `{worktree}/CONTEXT.md`（C2）。逼问只发生在 CC Lead session（pm_agent 自身不与用户对话）
- **HARD GATE**：用户在 tmux 里 approve 设计前不进入实现阶段
- CC Lead 产出 `docs/idea_refinement.md` 后，主会话继续 pm-n0b

**prompt 文件内容**（写到 `/tmp/{safe_id}_pm-n0a_prompt.md`，再用 `--prompt-file` 传给 cc-start.py）：
```markdown
## 任务：Idea Refinement（需求澄清 · 逼问式 grilling）

读取 PRD 文档：{prd_path}
（若存在 `{worktree}/CONTEXT.md`，先读它，逼问时引用其规范词）

请加载 **brainstorming** superpower，按**逼问式对齐**执行需求澄清：

### 逼问规则（grilling，见 pm_agent/references/grilling.md）
1. **一次只问一个问题**，沿决策树逐枝深入；每问一题**等用户答复后再问下一题**，不要一次抛一串
2. **每题附带你的推荐答案**（"我推荐 X，因为…"），让用户在推荐上做反应
3. **能读代码/文档查到的，先自己查**，只把需要人决策的开放问题留给用户
4. 每澄清/锐化一个术语，**立即**写回 `{worktree}/CONTEXT.md`（懒创建；与词表/代码冲突当场点出）
5. 要引入 PRD/代码之外的新机制，先停下问用户，**不自造**

### 流程
1. 读取 PRD（+ CONTEXT.md），列出关键不确定点并排序（先问影响后续分支的）
2. 展示 2-3 个方案方向（各附推荐倾向）供用户选择
3. 逐题逼问收敛 → 用户选择后深入设计
4. 产出 `docs/idea_refinement.md`：核心目标（1-3，可验证、有受益方）/ 约束条件（硬边界，标注已确认/推测）/ 开放问题（P0 阻塞立项 / P1 阻塞方案 / P2 可后续）
5. 同步更新 `{worktree}/CONTEXT.md`（本轮新增/锐化的术语）

**HARD GATE**：用户 approve 方案前不要进入任何实现阶段。
完成后在终端打印一行：`PM_CC_LEAD_DONE: docs/idea_refinement.md`
```

cc-start.py 调用见 SKILL.md《CC Lead 调用规范》；`--worktree-path={workspace_dir}`、
`--tmux-session={safe_id}-pm`、`--job-id` 用 `{repo}:{branch} pm-n0a`。

**返回（pm-n0a 完成）**：
```
tag: autopilot
line: pm-line
node: pm-n0a
goal_status: complete
next_role: pm-n0b
outputs:
  idea_refinement: docs/idea_refinement.md
```

### pm-n0b：Demand Parsing（需求解析）

收到 CC Lead + brainstorming 产出的 `docs/idea_refinement.md` 后（或需求本身已结构化时直接进入），基于已收敛的 `核心目标 / 约束条件 / 开放问题` 进行需求解析。

**执行**：
1. 读取 `docs/idea_refinement.md`
2. 基于核心目标和约束条件，进行需求解析
3. 识别关键实体、输入输出、边界条件
4. **【统一语言】产出 / 更新 `{worktree}/CONTEXT.md`**：通过 CC Lead（`cc-start.py`，读真实代码仓库）把第 3 步识别的领域术语按写作规范写入**代码仓库 worktree 根** `CONTEXT.md`（**不是 shrimp 的 `docs/`**，以便跨需求复用）。每条核心术语含 `规范词 / 别名(避免) / Not(不是) / 锚点(代码出处)` 四要素；写作规范与 realsyncer 样板见 `./context_glossary.md`。**已存在 `CONTEXT.md` 时只增量补充本需求新增/澄清的术语**，并把未消解的一词多义记入其 `⚠️ 待澄清` 区。
5. 如发现新的歧义点，通过 `questions_for_user` 反馈给 Orchestrator

**返回（pm-n0 完成）**：
```
tag: need_user
line: pm-line
node: pm-n0b
goal_status: waiting
next_role: orchestrator
outputs:
  context_glossary: {worktree}/CONTEXT.md   # 领域统一语言词汇表（repo 级，跨需求复用）
questions_for_user: [{ "type": "clarification", "question": "PRD 中 FR-003 存在歧义...", ... }]
```

---

## pm-n1：并行研究

收到 Orchestrator 的用户澄清后（或用户确认 idea-refine 结果后），启动并行研究。

**执行**：
1. 基于澄清更新需求理解
2. **并行**启动已启用的 Research Modules：
   - AIME Module：调用 AIME（当前无 sessions_send 通道 → 降级跳过，不阻塞）
   - CC_Lead_Writer Module：调用 CC Lead 生成文档草稿（cc-start.py，见《CC Lead 调用规范》）
   - TechResearch Module：技术调研
   - CompetitiveAnalysis Module：竞品分析
   - RiskAssessment Module：风险评估
3. 收集所有 Research Module 的结果
4. 处理冲突（见冲突处理策略）

**冲突处理策略**：
- 当多个 Module 对同一问题给出矛盾结论时 → 记录到 `docs/conflicts.md`
- 设置 `has_conflicts: true`，由 Orchestrator 汇总给用户裁决

**超时降级**：
- 单个 Module 超时（默认 3 分钟）→ 标记为 `degraded`，跳过该 Module
- Core 仍可基于已有结果继续汇总

**返回（pm-n1 完成）**：
```
tag: autopilot
line: pm-line
node: pm-n1
goal_status: partial
next_role: orchestrator
outputs:
  research_summary: docs/research_summary.md
  aime_discussion: docs/aime_discussion.md
  conflicts: docs/conflicts.md
has_conflicts: true
degraded_modules: []
```

---

## pm-n2：结果汇总（含 spec-generator）

**执行**：
1. 汇总所有 Research Module 的结果
2. 生成 `docs/research_summary.md`
3. 生成 `docs/tech_review.md` — **图文并茂 skeleton**：按 `subagent/doc_output/references/TEMPLATES.md §1` tech_review 模板，每个章节内部依"叙述 → mermaid → 图说明 → 表"顺序铺陈，禁止把全部 mermaid 集中在文末；禁止 ASCII art（违反 G1.5.11 中等级 gate）。骨架对照飞书标准 review 模板 `wikcnSeuhhO00BBpYwl22cL5zoh` 的"多画图，少写字"准则
4. 生成 `docs/module_plan.md` — 模块依赖必须用 mermaid `flowchart LR` + `subgraph Phase*`（D-DAG），不用 ASCII art。
5. 生成 `docs/task_list.md`
6. **【强制】** 生成 `docs/SPEC.md`（spec-driven 规范文档；数据模型节用 mermaid `classDiagram`/`erDiagram`）
7. **【决策记录 ADR】** 对 tech_review/SPEC 中满足三要素门（**难撤销 + 反直觉 + 真权衡**）的架构/选型决策，写入 `{worktree}/docs/adr/NNNN-*.md`（见 `./adr.md`）；tech_review 决策处引用 `ADR-XXXX`。三者缺一则不写。冲突裁决（conflicts.md）满足三要素的也落一条
8. **【自检】** 完成 SPEC.md 自检清单 + mermaid 自检清单 + 统一语言自检清单（CONTEXT.md 一致性）

### Mermaid 自检清单（v2.4 起 pm-n2 必走）

```markdown
## Mermaid 图表自检
- [ ] tech_review.md `\\`\\`\\`mermaid` ≥ 2 (D-ARCH + D-SEQ 至少一对)
- [ ] module_plan.md `\\`\\`\\`mermaid\\n(flowchart|graph)` ≥ 1 (D-DAG)
- [ ] SPEC.md `\\`\\`\\`mermaid\\n(classDiagram|erDiagram|stateDiagram)` ≥ 1
- [ ] 全文 grep `┌|└|│|─|▼|►` 命中 0 (无 ASCII art 流程图)
- [ ] 每个 mermaid 块前 ≥ 1 段叙述上下文,后紧跟 `> 图说明:` caption
- [ ] 关键章节(整体架构/模块依赖/数据模型)图与文交织,不是末尾"图集" dump
```

不达标按 G1.5.7 / G1.5.8 / G1.5.11 fail-with-hint 处理（pm_agent/SKILL.md 内 Gate 表 line 168-172）。

### SPEC.md 强制字段（7 个）

| 字段 | 必须？ | 说明 |
|------|--------|------|
| GOAL（目标） | 必须 | 核心目标 + 成功指标（必须有量化指标） |
| CONTRACT（输入输出契约） | 必须 | 每个模块的接口契约：输入/输出/前置条件/后置条件 |
| EDGE_CASES（边界条件） | 必须 | 至少 3 个边界场景 + 处理策略 + 责任模块 |
| ERROR_HANDLING（错误处理） | 必须 | 错误码定义 + 错误传递规则 |
| TEST_STRATEGY（测试策略） | 必须 | UT/IT/E2E 分层 + 具体测试用例编号（TDD-XXX） |
| DATA_MODEL（数据模型） | 强烈建议 | 核心数据结构定义 |
| DEPENDENCIES（依赖约束） | 建议 | 版本、来源、备注 |

### SPEC.md 自检清单

```markdown
## SPEC 生成自检
- [ ] GOAL-1: 核心目标是否可验证（有量化指标）？
- [ ] CONTRACT: 每个模块是否都有输入/输出/前置条件/后置条件？
- [ ] CONTRACT: 边界条件是否覆盖了空输入、单条、批量、异常值？
- [ ] EDGE_CASES: 是否列举了至少 3 个边界场景？
- [ ] ERROR_HANDLING: 是否有明确的错误码定义和错误传递规则？
- [ ] TEST_STRATEGY: 是否有具体可执行的 UT 用例（函数签名级别）？
- [ ] TEST_STRATEGY: 关键路径是否都有对应的测试用例编号？
- [ ] CLAUSE-INDEX: 所有关键条款是否都有唯一的条款编号？
```

### 统一语言自检清单（CONTEXT.md 一致性 · 即 C5）

> ⚠️ **架构说明**：`graph.py` 当前只做**结构级** Gate（Anti-Drop Guard G1/G3/G4/G5/G6 + G5_PROVE 文件存在/sha），**不做内容级 Gate**（文档里的 G1.5.7~11 mermaid 门、本 CONTEXT 门均未在 graph.py 实现，且 orchestrator 不持有代码仓库 worktree 路径，无法读 `{worktree}/CONTEXT.md`）。因此统一语言一致性由 **pm_agent 自检**完成（与 Mermaid / SPEC 自检同级）。
> 若 `{worktree}/CONTEXT.md` 不存在（该仓库首个需求）→ 跳过 1~2 项，仅做第 3 项。

```markdown
## 统一语言自检
- [ ] 别名零命中：tech_review.md / SPEC.md 全文未出现 CONTEXT.md `_别名(避免)_` 列出的词（命中→替换为规范词）
- [ ] 规范词一致：CONTRACT 条款、mermaid 图节点/边标签均使用 CONTEXT.md 规范词
- [ ] 无虚构机制：tech_review / SPEC 中的"新机制名词"，要么 CONTEXT.md 有词条、要么真实代码有锚点；否则视为 anti-scope-creep —— 停下经 questions_for_user 问用户，禁止自造（ShardingHashACK→对账触发器、Layer1/Layer2 即反例）
```
不达标处理：替换别名 / 补 CONTEXT.md 词条 / 或就"新机制"向用户澄清后再产出。

**返回（pm-n2 完成）**：
```
tag: autopilot
line: pm-line
node: pm-n2
goal_status: partial
next_role: orchestrator
outputs:
  tech_review: docs/tech_review.md
  module_plan: docs/module_plan.md
  task_list: docs/task_list.md
  spec: docs/SPEC.md              # 【新增】强制输出
  research_summary: docs/research_summary.md
```

---

## pm-n3：PM-Dev 协商（可选）

如果 Orchestrator 要求进行 PM-Dev 协商，执行此节点。

**执行**：
1. 读取 `docs/module_plan.md`、`docs/task_list.md` 和 `docs/SPEC.md`
2. 向 Orchestrator 发送协商请求
3. 等待 Orchestrator 返回 Dev-Agent 的评审反馈
4. 根据反馈更新模块划分、Task 列表和 SPEC 条款
5. 迭代（最多 3 轮）

**返回（pm-n3 完成）**：
```
tag: done
line: pm-line
node: pm-n3
goal_status: complete
next_role: orchestrator
outputs:
  negotiation_rounds: 2
questions_for_user: []
```

---

## Planning Phase 详解

### plan-n0：模块划分确认（轻量）

**前置条件**：pm-n2 已完成，`docs/task_list.md` 和 `docs/SPEC.md` 已产出。

**执行**：
1. 读取 `docs/task_list.md`
2. 读取 `docs/SPEC.md`
3. 确认模块划分是否与 SPEC 中的模块定义一致
4. 输出轻量评审到 `docs/planning_discussion.md`

---

### plan-n1：Task Breakdown 与依赖图生成（核心）

**前置条件**：
- `docs/task_list.md` 已产出
- `docs/SPEC.md` 已产出
- 已加载 `systematic-debugging` skill

**执行**：
1. 读取 `docs/task_list.md`
2. 读取 `docs/SPEC.md`
3. 加载 `systematic-debugging` skill
4. 将每个 Task 拆分为 0.5-2h 粒度的 sub-task
5. 标注执行相位（Phase-1 ~ Phase-5）
6. 识别并行/串行关系，标注 `execution_mode`
7. 为每个 sub-task 生成 rollback_plan
8. 为每个 sub-task 生成 **3-5 个按 likelihood 排序的可证伪 failure_hypotheses**（每个带 `prediction`：可观测的失败信号；**说不出预测就丢弃/锐化**）——与 `dev_agent/references/diagnose.md` 第 3 步同构，故障真发生时可直接复用为诊断假设
9. 建立 SPEC 条款 → sub-task 映射
10. 生成 `docs/task_breakdown.md`
11. 生成 `docs/depgraph.dot`

**task_breakdown.md 核心结构**：

```yaml
sub_tasks:
  {sub_task_id}:
    parent_task_id: string
    name: string
    phase: Phase-1 ~ Phase-5
    execution_mode: serial | parallel
    hours: float
    status: pending | confirmed | done | blocked | rolled_back
    spec_clauses: [string]
    description: string
    acceptance_criteria: [string]
    depends_on: [sub_task_id]
    blocking: [sub_task_id]
    rollback_plan:
      trigger_condition: [string]
      rollback_action:
        steps: [string]
        git_action: string
      recovery_path: [string]
      impact_scope:
        blocked_tasks: [string]
        unaffected_tasks: [string]
    failure_hypotheses:          # 3-5 个，按 likelihood 降序排列
      - hypothesis_id: string
        scenario: string
        prediction: string       # 可证伪："若本假设成立，则 <可观测失败信号>"；说不出预测则丢弃/锐化
        likelihood: high | medium | low
        spec_impact: [string]
        detection: string        # 如何观测到 prediction（对应 diagnose 第 4 步插桩）
        mitigation: string
        rollback: boolean
    dev_confirmed: boolean
```

---

### plan-n2：Dev-Agent 技术可行性评审

Dev-Agent 读取 `docs/task_breakdown.md`，验证每个 sub-task 的 rollback_plan 可行性，评审结果反馈给 Orchestrator。

---

### plan-n3：PM-Dev 协商与最终确认

1. PM-Agent 根据 Dev-Agent 反馈更新 task_breakdown.md
2. Dev-Agent 为每个 sub-task 填写 `dev_notes` 并置 `dev_confirmed: true`
3. 确认 `dev_confirmed` 比例 ≥ 80% 后，PM-Agent 上报 Orchestrator 进入 DEV_PHASE

**返回（plan-n3 完成）**：
```
tag: done
line: planning-line
node: plan-n3
goal_status: complete
next_role: orchestrator
outputs:
  task_breakdown: docs/task_breakdown.md
  dev_confirmed_rate: 0.85
```

---

## CC_Lead_Writer Module

### 功能定位
CC_Lead_Writer 是 PM-Agent 的 Research Module 之一，通过 `cc-start.py`（tmux + 本机 `claude`，见 SKILL.md《CC Lead 调用规范》）启动本地 CC Lead，让 CC Lead 基于实际代码仓库生成技术评审文档草稿。

CC_Lead_Writer 与 AIME Module 是**并行关系**，各自独立生成一份技术评审文档草稿，PM-Agent 最后做双视角融合对比。

### 与 AIME 的区别

| 维度 | AIME Module | CC_Lead_Writer Module |
|------|-------------|----------------------|
| 数据来源 | AIME 知识库 | 实际代码仓库（实时读取） |
| 输出风格 | 偏理论分析 | 偏落地实现 |

### 调用流程

1. **准备上下文**：整理 PRD 摘要 + 已澄清的需求点
2. **构造 prompt**：给 CC Lead 的 prompt 应包含：
   - PRD 原文
   - 已确认的需求理解
   - 输出格式要求（tech_review.md 结构）
   - 工作目录路径
   - **加载 superpower skills**（systematic-debugging、brainstorming 等）
3. **调用 CC Lead**：把 prompt 写到 `/tmp/{safe_id}_cc-writer_prompt.md`，再 `python3 $CC/cc-start.py --worktree-path {workspace_dir} --job-id "{repo}:{branch} pm-cc-lead-writer" --prompt-file /tmp/{safe_id}_cc-writer_prompt.md --tmux-session {safe_id}-pm-writer`
4. **处理返回**：用 `cc-capture.py` 轮询到 `PM_CC_LEAD_DONE:`，CC Lead 已把文档写到 `docs/cc_lead_tech_review.md`，pm_agent 读回校验

### 与 AIME 结果融合

PM-Agent 在 pm-n2 汇总时，需要对比两份文档：
- `docs/tech_review.md`（AIME 生成）
- `docs/cc_lead_tech_review.md`（CC Lead 生成）

如果两份文档对同一问题结论不同，记录到 `docs/conflicts.md`，由用户裁决。

---

## PM-Agent 错误处理

| 错误类型 | 处理方式 |
|----------|----------|
| AIME 调用失败 | 降级为无 AIME 分析，继续执行 |
| Module 超时 | 标记 `degraded`，继续执行 |
| SPEC.md 自检未通过 | 返回 `error` tag，blocker 记录未通过的检查项 |
| task_breakdown.md rollback_plan 覆盖率 < 80% | 返回 `error` tag |
| 文件写入失败 | `error` tag 返回，记录到 blockers |
| PRD 读取失败 | `error` tag 返回，等待 Orchestrator 重新传入 |
