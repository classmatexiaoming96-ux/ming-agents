# 建议1 落地草案：CONTEXT.md 统一语言词汇表（含写作规范 + ShardingHashACK 样板）

> 来源：mattpocock/skills 的 `grill-with-docs` + `CONTEXT-FORMAT.md`，本地化适配 shrimp（中文 / 多栈 / 飞书 / Subagent 隔离）。
> 目标薄弱环节：shrimp 全程无统一语言词汇表（薄弱环节 #1）；直接预防 ShardingHashACK 类术语"升级"事故。

---

## 1. 定位与生命周期（关键：解决"生命周期错配"风险）

| 维度 | mattpocock 原版 | shrimp 适配 |
|---|---|---|
| 存放位置 | 代码 repo 根 `CONTEXT.md` | **真实代码仓库 worktree 根**（如 `/root/realsyncer/CONTEXT.md`），**不放进 shrimp 的 `docs/`** |
| 生命周期 | repo 级、跨需求、长期演进 | 同左——**跨需求复用**，每个需求只增量更新，不重建 |
| 谁来写 | 人在 grilling 中维护 | pm-n0b 产初版、pm-n0a grilling 增量更新，均由 **CC Lead session**（能读真实代码）落笔；pm_agent 自己不写 |
| 内容边界 | 纯 glossary，无实现细节 | 同左——决策归 ADR（建议5），方案归 tech_review，CONTEXT.md 只定义"词" |

> 为什么放 worktree 根而非 shrimp docs/：词汇表的价值在于**跨需求沉淀真实领域语言**。放需求级 `docs/` 会随需求一次性销毁，下个需求又从零误解 ShardingHashACK，事故复发。

---

## 2. CONTEXT.md 写作规范

### 2.1 单条术语格式

```
**术语（规范词）**：一句话定义（它"是什么"，不是"它做什么"）。最多 2 句。
_别名(避免)_：列出口语里会混用、但本表禁止再用的写法
Not（不是）：明确它**不是**什么 —— 防止被"升级"过度解读       ← shrimp 新增字段
锚点：file:line 或代码符号 —— 术语在真实代码里的出处          ← shrimp 新增字段
```

### 2.2 规则（继承 mattpocock + shrimp 两条新增）

继承自 mattpocock：
1. **Opinionated（择一规范词）**：同一概念只保留一个规范词，其余全部进 `_别名(避免)_`。
2. **定义"是什么"，≤2 句**：写本体，不写流程；流程/方案归 tech_review。
3. **只收录项目特有术语**：排除通用编程概念（如"缓存""重试""哈希"本身），只收本领域被赋予特定含义的词。
4. **冲突即挂起**：发现一词多义、定义打架，放到文末 `## ⚠️ 待澄清` 区，逐条在 grilling 中消解。
5. **懒创建**：有内容才写；没想清楚的词先进"待澄清"，不硬编。
6. **多 context**：单领域 = 一个根 `CONTEXT.md`；多领域 = 根 `CONTEXT-MAP.md` 指向各子域 `CONTEXT.md`，并描述子域间事件流。

shrimp 新增（直接对抗两类记录在案的事故）：
7. **`Not（不是）` 字段（对抗"升级"）**：凡是"听起来像别的东西"的术语，必须写 `Not`，把边界钉死。ShardingHashACK 事故正是缺这一行——它被从"传输级确认"升级成"对账触发器"。
8. **`锚点` 字段（对抗"自主加料"）**：每个领域术语必须能指向真实代码出处。**一个没有代码锚点、CONTEXT.md 又无条目的"新机制名词"= scope-creep 红线**，CC Lead 必须停下问用户，而非自造（如虚构的 Layer1/Layer2）。

---

## 3. 空模板（CONTEXT.md）

```markdown
# CONTEXT.md — {repo} 领域统一语言词汇表

> 本表是该仓库的术语唯一权威。PM/Plan/Dev/Review 全程以本表规范词为准。
> 规则：见 shrimp pm_agent 写作规范。决策记录归 docs/adr/，方案归 tech_review，本表只定义"词"。

## 核心实体

**{规范词}**：{定义，≤2句}。
_别名(避免)_：{混用写法}
Not（不是）：{它不是什么}
锚点：{file:line / 符号}

## 关键消息 / 事件

（同上格式）

## 状态 / 角色

（同上格式）

## ⚠️ 待澄清（一词多义 / 定义冲突，grilling 中逐条消解）

- [ ] {术语}：{冲突点} —— {待用户/代码确认的问题}
```

---

## 4. 样板：`/root/realsyncer/CONTEXT.md` 节选（用真实代码填充）

```markdown
# CONTEXT.md — realsyncer 领域统一语言词汇表

## 核心实体

**ShardKey**：分片的全局唯一标识，格式 `shard:::{stream_info_name}:transmitUnit_{endpoint}:3`；其中嵌入的 source endpoint 是创建分片时**静态写入**的，之后不再更新。
_别名(避免)_：shard id、分片键
Not（不是）：不是动态路由表——它内嵌的 transmitUnit 不随节点扩缩容更新（这正是多 ACK 混乱的根源）。
锚点：`internal/domain/shardmanager/manager.go` parseShardSourceFromKey

**ShardingHash**：Edge 节点对其持有的分片数据计算出的一致性哈希，发给 Center 做比对；新接入、未同步的节点该值为 0。
_别名(避免)_：分片哈希、数据指纹
锚点：`internal/application/consistency/consistency.go:156` OnShardingHashReceived

**pending_delete**：记录某 ShardKey 下"等待删除"的 element 的 redis hash。
Not（不是）：不是单 element 标记——当前 ClearPendingDeleteByShardKey 用 `Del` 删整个 key（bug 根因，见 docs/adr）。
锚点：`internal/repository/redis/center/pending_delete.go:54`

## 关键消息 / 事件

**ShardingHashACK**：接收方处理完**一条** ShardingHash 后回发的**传输级确认**；其唯一副作用是清除该 ShardKey 的 pending_delete 标记 + 删除对应发送任务。
_别名(避免)_：哈希确认、对账确认
Not（不是）：**不是对账（reconciliation）触发器**；不触发任何 Layer1/Layer2 多级机制；不代表分片数据已最终一致。
锚点：`internal/center/syncer.go:714` OnShardingHashACKHandler → `pending_delete.go:54` ClearPendingDeleteByShardKey

**ShardingResync**：Center → transmitUnit 的重同步消息，携带差异 ElementList；diff 为空（`ElementList: []`）表示 Center 认为需同步但无具体差异元素。
锚点：`internal/application/consistency/consistency.go:132` OnShardingResyncReceived

**ShardRecovery**：本地与远端 ShardingHash 不一致时触发的分片恢复任务。
_别名(避免)_：分片修复、recovery
锚点：consistency.go createShardRecoveryTask

## 状态 / 角色

**Center / Edge**：节点角色。Center（如 maliva）汇聚多个 Edge（fr.*）的同步；数据流向 Edge → Center → 其他 Edge。
**transmitUnit**：一种 Endpoint 类型，代表边缘传输单元（edge pod），如 `fr.au-sg920g1`。
_别名(避免)_：edge node（须与 EndpointEdge 类型区分）

**stale (sharding hash) message**：来源 != expectedUpstream 的 ShardingHash（典型为切机房/扩缩容后过时节点发来）；处理上直接回 ACK、不做一致性校验。
锚点：consistency.go:156 中 `expectedUpstream.Equal(param.Source)` 分支

## ⚠️ 待澄清

- [ ] ShardKey 中 `transmitUnit_{endpoint}` 到底用于**路由 ACK** 还是**仅标识 Shard**？（多个不同 transmitUnit 向同一 ShardKey 发 ACK 是否预期？）—— 见 bugfix 任务卡 Q1/Q3
```

---

## 5. ShardingHashACK 事故复盘：词汇表如何拦截

| | 无 CONTEXT.md（实际发生） | 有 CONTEXT.md（本草案） |
|---|---|---|
| pm-n0a Q5 提及 ShardingHashACK | agent 凭字面"ACK + Hash"联想，**"升级"**为"哈希对账确认 → 触发对账流程" | 引用 CONTEXT.md 条目，读到 `Not：不是对账触发器` + 锚点 syncer.go:714 → 不升级 |
| 设计阶段 | 据误读设计出 **Layer1/Layer2 对账机制（代码不存在）** | 若仍想引入"对账触发"，发现 CONTEXT.md 写了 Not 且新词无代码锚点 → 命中 anti-scope-creep 红线 → **停下问用户**，不自造 |
| 结果 | Gate 后才发现虚构，**回退重做**（成本最高） | 在 pm-n0a 即被拦截，零返工 |

---

## 6. 集成改造点（待批准后实施）

> 以下需改 shrimp 现有文件；本草案先不动它们，等确认。

| # | 文件 | 改动 |
|---|---|---|
| C1 | `subagent/pm_agent/references/pm_phases.md` · pm-n0b | 新增步骤："CC Lead 读真实仓库 → 产出/更新 `{worktree}/CONTEXT.md` 初版"，附本写作规范引用 |
| C2 | `pm_phases.md` · pm-n0a brainstorming prompt | grilling 中"每澄清一个术语即时写回 CONTEXT.md（懒创建）"（与建议2 合并实施最自然） |
| C3 | `subagent/pm_agent/SKILL.md` 核心原则 | 新增："术语以 `{worktree}/CONTEXT.md` 为准；任何 PRD/代码之外的新机制名词，若 CONTEXT.md 无条目且代码无锚点 → 触发 anti-scope-creep，停下经 `questions_for_user` 问用户" |
| C4 | `pm_phases.md` · pm-n2 | tech_review / SPEC 的 CONTRACT 与 mermaid 图标签统一用 CONTEXT.md 规范词 |
| C5 | `orchestrator/scripts/graph.py` · Gate1.5 | 新增软门 G1.5.12（warning→fail-with-hint）：tech_review/SPEC 出现 CONTEXT.md `别名(避免)` 词、或出现无锚点且无 CONTEXT 条目的"新机制名词" → 带 hint 重试 |
| C6 | `subagent/reviewer_agent` Readability/Maintainability 轴 | 增加检查项："命名是否符合 CONTEXT.md 规范词" |

> 落地节奏建议：C1+C3 先行（最小可用：初版词汇表 + anti-scope-creep 红线）；C2 与建议2 grilling 一起；C5 软门最后加，避免 Gate 再变重。
