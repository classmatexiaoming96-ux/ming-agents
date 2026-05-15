# doc_output 调用方产图契约（v2.6 Phase 3）

> 适用对象：**直接调用 doc_output 的 LLM**（CC / Orchestrator / 非 pm_agent 上游）
> 作用：把"图位填成真 mermaid"这件事，从隐性的职责真空变成显式契约
> 与 pm_agent 的关系：pm_agent 路径已有 Gate G1.5.7~G1.5.11 强制产图，**无需引用本文件**；本文件只服务于没有 Gate 兜底的直接调用路径

---

## 背景：为什么需要这份契约

doc_output 是**纯 Python 脚本，自身不调用 LLM**。它的 `template_engine` 会把模板里的 `{{include_diagram:D-XXX}}` 占位替换成一段 **placeholder stub**：

```
<!-- TODO: 补 D-ARCH mermaid 图; 骨架见 TEMPLATES.md §0.4 -->
```mermaid
%% DOC_OUTPUT_DIAGRAM_PLACEHOLDER D-ARCH — 调用方按 §0.4 D-ARCH 骨架替换
```
```

这段 stub **不是真图** —— 它要靠"下一棒"把它替换成真正的 mermaid 代码块。在 pm_agent 路径里，"下一棒"是受 Gate 约束的 pm_agent LLM；在直接调用路径里，**"下一棒"就是你（调用方 LLM）**。

如果你不填，产物里就只剩 stub，飞书画板升级链路（`chart_publisher`）也无块可升 —— 文档退化成"纯文字 + 表格"，违背"图文并茂"目标。

> **v2.6 Phase 3 起的 sentinel 命名**：stub 内首行从 `%% PLACEHOLDER D-XXX` 改为 `%% DOC_OUTPUT_DIAGRAM_PLACEHOLDER D-XXX`，加上命名空间前缀避免与文档中真实出现的"PLACEHOLDER"撞车。`DocOutput.count_unfilled_diagrams()` 优先按新 sentinel 计数，旧 sentinel 仍兼容。

---

## 契约：调用方 LLM 必须做的事

### C1. 填充所有 `%% DOC_OUTPUT_DIAGRAM_PLACEHOLDER` 图位

拿到 doc_output 产物后，**逐个**把形如下面的 stub 替换为真 mermaid：

- 定位信号：` ```mermaid ` 代码块内首行是 `%% DOC_OUTPUT_DIAGRAM_PLACEHOLDER D-XXX ...`
- 替换动作：按 `TEMPLATES.md §0.4` 对应图代号的骨架，结合文档上下文（组件表 / 模块表 / 实体表 / Task 表）生成真实 mermaid
- 删除 stub 上方的 `<!-- TODO: ... -->` 注释行（图填好后 TODO 不再成立）
- 自检信号（v2.6 Phase 3 起两种形式）：
  - doc_output **初始返回**契约里的 `unfilled_diagram_count` 字段 —— 反映 doc_output 交付时的状态（几乎总是 > 0，stub 尚未填）
  - 你填完后**自己 re-check**：调用 `DocOutput.count_unfilled_diagrams(content)` 或 `DocOutput.recheck_unfilled_diagrams(path)`，**必须为 0** 才算完成

### C2. 禁止 ASCII art

**绝对禁止**用 `┌─┐ │ └─┘ ▼ ►` 等字符画流程图 / 架构图。原因：

- ASCII 等宽块在飞书 docx 渲染为不可交互、不能批注的代码块
- 无法被 `chart_publisher` 升级为飞书交互画板
- 违背飞书技术评审标准模板 `wikcnSeuhhO00BBpYwl22cL5zoh` 的"多画图，少写字"准则

所有架构图 / 时序图 / 流程图 / 状态机 / 类图 / 依赖图 **必须**用 ` ```mermaid ``` ` 围栏。

### C3. 每图前有 prose、后有图说明

遵循"叙述 → 图 → 说明 → 表"的节内顺序（TEMPLATES.md §0.6）：

- 每个 mermaid 块**前**必须有 **≥ 1 段叙述**上下文（这段图在讲什么、为什么放这里）
- 每个 mermaid 块**后**必须紧跟 **1 行** `> 图说明：...` caption
- 禁止"图集 dump" —— 不要把所有图堆在文末一个"图表"章节里

### C4. 节点命名复用文档实体名

- 架构图节点 ID = §核心组件表的"组件名称"
- 依赖图节点 ID = §模块总览表的"模块ID" / §Task总览表的"Task ID"
- 类图类名 = §核心实体表的"实体名称"
- **禁止凭空补造** PRD/SPEC 未定义的实体

---

## 各 doc_type 的默认图表要求

下表是直接调用路径下，每类文档**至少**应填充的图（图位已由 `TEMPLATES.md` 模板预置，你只需填充）。各 doc_type 在 TEMPLATES.md 中的 §引用以及对应 fallback skeleton 的 §引用都列在表中，便于直接定位填充点：

| doc_type | 必填图 | TEMPLATES.md 位置 | Fallback skeleton 位置 |
|---|---|---|---|
| tech_review | D-ARCH + D-SEQ + D-CLASS | §1 / 2.1 整体架构（架构图 + 关键接口时序） / 5.1 核心实体（数据模型类图） | `_get_rich_skeleton_template`：§2.1 架构图 / §2.3 关键接口时序 / §4 数据模型 |
| design_doc | D-ARCH + D-SEQ | §2 / 2.1.1 架构图 / 2.1.2 核心流程 | `_get_rich_skeleton_template`：§2.1 架构图 / §2.3 关键接口时序 |
| research_report | D-DAG | §3 / 5.2 实施路径 | `_get_planning_skeleton_template`：§2.1 依赖图（"推荐方案实施路径"） |
| module_plan | D-DAG | §4 / 2.1 依赖关系图 | `_get_planning_skeleton_template`：§2.1 依赖图（"模块依赖关系"） |
| task_plan | D-DAG | §5 / 2.1 阶段划分 | `_get_planning_skeleton_template`：§2.1 依赖图（"执行顺序与关键路径"） |
| test_plan | D-DAG | §6 / 4 测试进度安排 | `_get_planning_skeleton_template`：§2.1 依赖图（"测试阶段与依赖"） |
| idea_refine | 无 | §7 | —— |

> 升级提示：D-ARCH / D-SEQ / D-DAG 三类在飞书发布后会被 `chart_publisher` 自动升级为交互画板；D-CLASS / D-STATE 保留 mermaid 原样。规则见 `TEMPLATES.md §0` v2.4.0 表 —— 这不影响你的填充动作，照填即可。

> 占位变体：除了 `{{include_diagram:D-XXX}}` 强类型形式，TEMPLATES.md §0.5 还支持 `{{include_diagram:multi}}`（一次产出 D-ARCH+D-SEQ 两个 stub）和裸 `{{include_diagram}}`（产出一个 GENERIC stub，由调用方按上下文选择图代号）。这三种全部会被 template_engine 替换为带 sentinel 的 stub，**不会**被静默删除。

---

## 可直接嵌入 system prompt 的指令片段

把下面这段**原样**放进调用 doc_output 的 LLM 的 system prompt（或任务指令）中：

```text
你调用 doc_output 生成文档后，产物中会包含若干图位占位 ——
形如 ```mermaid 块内首行为 `%% DOC_OUTPUT_DIAGRAM_PLACEHOLDER D-XXX` 的 stub。
你必须在返回文档前完成以下动作：

1. 逐个把 `%% DOC_OUTPUT_DIAGRAM_PLACEHOLDER ...` stub 替换为真实 mermaid 代码块，
   按 doc_output/references/TEMPLATES.md §0.4 对应 D-XXX 骨架，
   结合文档里的组件表/模块表/实体表/Task表生成；删除 stub 上方的
   `<!-- TODO -->` 注释行。
2. 禁止用 ┌─┐│└─┘▼► 等字符画 ASCII art —— 所有图必须是 ```mermaid``` 围栏。
3. 每个 mermaid 块前 ≥1 段叙述上下文，后紧跟 1 行 `> 图说明：...`。
4. mermaid 节点名复用文档中已有的实体名（组件名/模块ID/Task ID/实体名），
   不要凭空补造实体。
5. 自检：调用 `DocOutput.count_unfilled_diagrams(filled_content)`
   或 `DocOutput.recheck_unfilled_diagrams(path)` 得到的计数 **必须为 0**。
   返回契约里的 `unfilled_diagram_count` 是初始计数（几乎总 > 0），不要被它误导。

mermaid 语法渲染失败时，保留源码块并加
`<!-- TODO: mermaid 渲染失败，待补 -->`，不要静默删除。
```

---

## 失败回退

| 场景 | 处理 |
|---|---|
| 某图缺上下文（表为空，无实体名可复用） | 保留 stub，把 `<!-- TODO -->` 改为 `<!-- TODO: 上下文缺失，待补 D-XXX -->`，不要瞎编 |
| mermaid 语法渲染失败 | 保留源码块 + `<!-- TODO: mermaid 渲染失败，待补 -->`，不静默删除 |
| `unfilled_diagram_count > 0` 但无法补齐 | 在返回结果里显式说明哪些图位未填、原因，交回上游决策 |

---

## 相关文件

- 图位占位语义：`references/TEMPLATES.md §0.5`
- 各图代号骨架：`references/TEMPLATES.md §0.4`
- 飞书画板升级规则：`references/TEMPLATES.md §0` v2.4.0 表 / `references/doc-output-chart-upgrade.md`
- 占位 → stub 替换实现：`template_engine.py:_replace_variables`（`{{include_diagram:D-XXX}}` / `:multi` / bare 三分支）
- 初始自检字段：`doc_output.py:_count_unfilled_diagrams`
- 公开 re-check API：`doc_output.py:DocOutput.count_unfilled_diagrams` / `DocOutput.recheck_unfilled_diagrams`
- Markdown 围栏剥离（P0-1 修复）：`template_engine.py:_unwrap_markdown_fence`
