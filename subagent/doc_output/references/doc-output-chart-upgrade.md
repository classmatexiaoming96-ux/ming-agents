# doc_output — 飞书画板自动升级（v2.4+）

> 版本：v2.4 | D-ARCH / D-SEQ / D-DAG mermaid 块 → 可交互飞书画板

---

## 目的

`tech_review.md` / `module_plan.md` 等 pm-n2 产物通常含若干 `\`\`\`mermaid` 代码块。
飞书原生 mermaid 渲染只能产 SVG，评审者无法点节点、加批注、看交互。v2.4 在
"发布飞书" 这一步后追加一个自动升级链路：识别 D-ARCH / D-SEQ / D-DAG 三类
块，挑选每块创建一张飞书画板，把 mermaid 内容写进去，并保留原 mermaid 块作
ground truth。

---

## 触发条件

| 条件 | 状态 |
|------|------|
| `options.publish_feishu` 不为 False | 必需（默认 True） |
| `options.upgrade_charts` 不为 False | 默认 ON；显式传 False 可关闭 |
| `self.workspace` 目录存在 | 必需，用于落 chart_publisher 的 tmp .mmd 文件 |
| Markdown 中存在 `<!-- chart: D-{ARCH,SEQ,DAG} -->` 标注的 mermaid 块 | 决定有哪些块要升级 |

不满足时走原 `FeishuPublisher.publish()`，不影响主链路。

---

## 升级目标三选

| 图代号 | 适用 mermaid 语法 | 是否自动升级 |
|--------|------------------|--------------|
| **D-ARCH** | `flowchart TB/LR` + 多 `subgraph` | ✅ |
| **D-SEQ** | `sequenceDiagram` | ✅ |
| **D-DAG** | `flowchart` + `subgraph Phase*` | ✅ |
| D-CLASS | `classDiagram` / `erDiagram` | ❌ 保留 mermaid |
| D-STATE | `stateDiagram-v2` | ❌ 保留 mermaid |
| D-DECISION | `flowchart` 无 subgraph | ❌ 保留 mermaid |
| D-ERR | error-flow `flowchart` | ❌ 保留 mermaid |
| D-MIND / D-GANTT / D-MATRIX | `mindmap` / `gantt` / 矩阵表格 | ❌ |

完整升级规则与原因见 `TEMPLATES.md §0` v2.4.0 表。

---

## 标注规范

`template_engine.annotate_mermaid_blocks()` 在 `render()` 末尾按 mermaid 首行
语法自动 prepend `<!-- chart: D-XXX -->` 注释；幂等。pm_agent / dev_agent 不
需要手动写标注。

例：

```markdown
<!-- chart: D-ARCH -->
\`\`\`mermaid
flowchart TB
  subgraph Client
    A1
  end
\`\`\`
```

---

## 链路（单图）

```
publish (docx URL) → extract_upgradable_charts(markdown)
   ├─ 抽 (D-XXX, mermaid_body) 列表
   └─ 每个块走 chart_publisher.publish_chart:
        ├─ docs +update mode=append <whiteboard type="blank"/>
        ├─ 从 update 响应取 board_tokens[0]（或 fallback fetch markdown 抽 <whiteboard token=.../>）
        ├─ whiteboard +update --input_format mermaid --source @<tmp>.mmd
        └─ whiteboard +query --output_as code → 与 source 比对(round-trip)

返回: {url, doc_token, chart_results: [PublishResult, ...], skipped: [(code, reason), ...]}
```

---

## 容错与回退

`doc_output._publish_feishu()` 是三档 fallback 路由：

1. `options.upgrade_charts == False`        → 走原 `publisher.publish()`
2. `self.workspace` 目录不存在               → fallback `publisher.publish()` + 打 warn
3. `ChartPublisher(...)` 初始化抛异常        → fallback `publisher.publish()` + 打 warn
4. 否则                                       → `publisher.publish_with_charts(...)`

`publish_with_charts` 内部：单个 chart 失败不中断兄弟 chart（best-effort），失败原因记到 `result["skipped"]`。

---

## bot scope 要求（实测最小集）

| Scope | 用途 |
|------|------|
| `docs:document` (read+write) | docx fetch + append |
| `board:whiteboard:node:create` | 写画板内容 |
| `board:whiteboard:node:read` | round-trip 回读 + PNG 导出 |
| `wiki:wiki:readonly` | 仅当 pm-n2 走 wiki 形态时必需 |

缺 scope 时画板升级链路会失败但不阻塞 docx 发布主链。

---

## 关掉升级

```python
DocOutput({
    ...,
    "options": {
        "publish_feishu": True,
        "upgrade_charts": False,  # ← 关掉, 走原 publish
    },
}).run()
```

---

## 实现入口

- 路由 helper: `doc_output.py:DocOutput._publish_feishu`
- 编排: `feishu_publisher.py:FeishuPublisher.publish_with_charts`
- 抽块: `feishu_publisher.py:extract_upgradable_charts`
- 单图: `chart_publisher.py:ChartPublisher.publish_chart`
- 标注: `template_engine.py:annotate_mermaid_blocks`

测试覆盖：
- `test_chart_publisher.py` — chart_publisher core
- `test_feishu_publisher_with_charts.py` — publish_with_charts + extract
- `test_template_engine_annotate.py` — annotator + classifier
- `test_doc_output_publish_feishu.py` — _publish_feishu routing
