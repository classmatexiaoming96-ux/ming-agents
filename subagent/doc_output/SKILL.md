# shrimp-doc-output v2.4.0

## 定位
**通用文档输出子 skill**。支持多种输入源**并行获取**，生成结构化文档。

---

## 快速索引

| 主题 | 参考文件 |
|------|---------|
| 并行获取架构 + 全部输入源 + 错误处理 | `references/doc-output-parallel-fetch.md` |
| Chunk 四元组 + 一致/互补/冲突处理 + 融合优先级 | `references/doc-output-content-fusion.md` |
| IdeaRefiner 触发条件 + 流程 + 确认契约 | `references/doc-output-idearefin.md` |
| AIME Session 管理（flock 实现） | `references/doc-output-aime-session.md` |
| 各输入源调用示例 + 输出路径规范 | `references/doc-output-examples.md` |
| 飞书发布 + D-ARCH/D-SEQ/D-DAG 画板自动升级（v2.4+） | `references/doc-output-chart-upgrade.md` |

---

## 执行流程（总览）

```
Step 1: 解析任务（doc_type、input_sources）
Step 2: 并行获取（全部源同时执行，总耗时 = max）
Step 3: 内容融合（Chunk 四元组 → 一致/互补/冲突 → 用户裁决）
Step 4: 生成文档 → docs/{doc_type}_{timestamp}.md
Step 5: 用户确认（通过 Orchestrator）
Step 6: 发布飞书 → feishu docx
        ↳ v2.4+: 自动把 D-ARCH/D-SEQ/D-DAG mermaid 块升级为可交互画板
                 (`options.upgrade_charts` 默认 ON; 详见
                 `references/doc-output-chart-upgrade.md`)
Step 7: 返回结果契约
```

> README.md 用同一套 7-step 编号；早期版本 Step 6 / Step 7 在两份文档里指代不一致（README 把"用户确认"叫 Step 6, SKILL 把"发布飞书"叫 Step 6），v2.4 统一为本表顺序，README.md 已同步。

---

## 核心原则

- **并行获取**：所有输入源同时执行，总耗时 = 最长那个
- **用户裁决**：冲突项（topic 相同但 content 差 > 10%）提交用户裁决
- **Orchestrator 路由**：doc-output 不直接调 `askUserQuestion`，返回 `confirmation_context` 由 Orchestrator 处理
- **IdeaRefiner 先收敛**：模糊想法先结构化为三元组，用户确认后再并行获取

---

## IdeaRefiner 触发条件

满足任一即触发 idea-refine 分支：
- `doc_type == 'idea_refine'`
- `orchestrator_mark.requirement_type == 'rough_idea'`
- 无明确输入源 AND 字数 < 200
- 含模糊词汇 AND 字数 < 200（大概、想做、初步想法、可能要...）

---

## 冲突上限

冲突项 > 3 个时，提取范围 + 生成对比表格让用户圈选。

---

*v2.4.0 | IdeaRefiner + 并行架构 + 飞书画板自动升级*
