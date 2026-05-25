# CONTEXT.md 写作规范与样板（领域统一语言词汇表）

> 被 pm-n0b（产初版）与 pm-n0a grilling（增量更新）引用。
> 来源：mattpocock/skills `grill-with-docs` / `CONTEXT-FORMAT.md`，本地化适配 shrimp（中文 / 多栈 / Subagent 隔离）。

## 定位与生命周期

- **存放位置**：真实代码仓库 worktree 根 `{worktree}/CONTEXT.md` —— **不放进 shrimp 的 `docs/`**。
- **生命周期**：repo 级、跨需求复用；每个需求只**增量补充**本次新增/澄清的术语，不重建。
- **谁写**：由 CC Lead session（能读真实代码）落笔；pm_agent 自己不写正文。
- **内容边界**：纯 glossary。决策记录归 `docs/adr/`，技术方案归 `tech_review.md`，本表**只定义"词"**。

> 为何放 worktree 根而非 `docs/`：词汇表价值在跨需求沉淀真实领域语言。放需求级 `docs/` 会随需求一次性销毁，下个需求又从零误解同一术语（如 ShardingHashACK），事故复发。

## 单条术语格式

```
**术语（规范词）**：一句话定义（"是什么"，非"做什么"），≤2 句。
_别名(避免)_：会被混用、但本表禁止再用的写法
Not（不是）：明确它不是什么 —— 防止被"升级"过度解读
锚点：file:line 或代码符号 —— 术语在真实代码里的出处
```

> `_别名(避免)_` / `Not` / `锚点` 三行按需出现：领域核心术语建议齐全；普通术语至少有 `锚点`。

## 规则

继承 mattpocock：
1. **Opinionated**：一个概念只留一个规范词，其余进 `_别名(避免)_`。
2. **≤2 句**，定义"是什么"不是"做什么"。
3. **只收项目特有术语**，排除通用编程概念（缓存、重试、哈希本身不收）。
4. **冲突即挂** `## ⚠️ 待澄清`，grilling 中逐条消解。
5. **懒创建**：没想清的词先进待澄清，不硬编。
6. **多领域**用根 `CONTEXT-MAP.md` 指向各子域 `CONTEXT.md`，并描述子域间事件流。

shrimp 新增（对抗两类记录在案的事故）：
7. **`Not（不是）`（对抗"升级"）**：凡"听起来像别的东西"的术语必须写 `Not`，把边界钉死。ShardingHashACK 事故正缺这一行 —— 它被从"传输级确认"升级成"对账触发器"。
8. **`锚点`（对抗"自主加料"）**：每个领域术语必须能指向真实代码。**无锚点、表中又无条目的"新机制名词" = scope-creep 红线**，CC Lead 必须停下经 `questions_for_user` 问用户，禁止自造（如虚构的 Layer1/Layer2）。

## 空模板

```markdown
# CONTEXT.md — {repo} 领域统一语言词汇表

> 本表是该仓库术语的唯一权威。PM/Plan/Dev/Review 全程以本表规范词为准。
> 决策记录归 docs/adr/，方案归 tech_review，本表只定义"词"。

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

## 样板：`/root/realsyncer/CONTEXT.md` 节选（用真实代码填充）

```markdown
# CONTEXT.md — realsyncer 领域统一语言词汇表

## 核心实体

**ShardKey**：分片的全局唯一标识，格式 `shard:::{stream_info_name}:transmitUnit_{endpoint}:3`；其中嵌入的 source endpoint 是创建分片时**静态写入**的，之后不再更新。
_别名(避免)_：shard id、分片键
Not（不是）：不是动态路由表 —— 内嵌的 transmitUnit 不随节点扩缩容更新（多 ACK 混乱的根源）。
锚点：internal/domain/shardmanager/manager.go parseShardSourceFromKey

**ShardingHash**：Edge 节点对其持有分片数据计算出的一致性哈希，发给 Center 做比对；新接入、未同步的节点该值为 0。
_别名(避免)_：分片哈希、数据指纹
锚点：internal/application/consistency/consistency.go:156 OnShardingHashReceived

**pending_delete**：记录某 ShardKey 下"等待删除"的 element 的 redis hash。
Not（不是）：不是单 element 标记 —— 当前 ClearPendingDeleteByShardKey 用 Del 删整个 key（bug 根因）。
锚点：internal/repository/redis/center/pending_delete.go:54

## 关键消息 / 事件

**ShardingHashACK**：接收方处理完**一条** ShardingHash 后回发的**传输级确认**；其唯一副作用是清除该 ShardKey 的 pending_delete 标记 + 删除对应发送任务。
_别名(避免)_：哈希确认、对账确认
Not（不是）：**不是对账(reconciliation)触发器**；不触发任何 Layer1/Layer2 多级机制；不代表分片数据已最终一致。
锚点：internal/center/syncer.go:714 OnShardingHashACKHandler → pending_delete.go:54 ClearPendingDeleteByShardKey

**ShardingResync**：Center → transmitUnit 的重同步消息，携带差异 ElementList；diff 为空（ElementList: []）表示 Center 认为需同步但无具体差异元素。
锚点：internal/application/consistency/consistency.go:132 OnShardingResyncReceived

**ShardRecovery**：本地与远端 ShardingHash 不一致时触发的分片恢复任务。
_别名(避免)_：分片修复、recovery
锚点：consistency.go createShardRecoveryTask

## 状态 / 角色

**Center / Edge**：节点角色。Center（如 maliva）汇聚多个 Edge（fr.*）的同步；数据流向 Edge → Center → 其他 Edge。
**transmitUnit**：一种 Endpoint 类型，代表边缘传输单元（edge pod），如 fr.au-sg920g1。
_别名(避免)_：edge node（须与 EndpointEdge 类型区分）

**stale (sharding hash) message**：来源 != expectedUpstream 的 ShardingHash（典型为切机房/扩缩容后过时节点发来）；处理上直接回 ACK、不做一致性校验。
锚点：consistency.go:156 中 expectedUpstream.Equal(param.Source) 分支

## ⚠️ 待澄清
- [ ] ShardKey 中 transmitUnit_{endpoint} 用于**路由 ACK** 还是**仅标识 Shard**？（多个不同 transmitUnit 向同一 ShardKey 发 ACK 是否预期？）
```
