# shrimp-doc-output 使用指南

> Shrimp 4.0 通用文档输出子 skill —— 由上游 skill（如 PM-Agent）调用，自动从多源**并行**获取内容并生成结构化技术文档。

## 概述

**shrimp-doc-output** 是 Shrimp 4.0 研发体系中的通用文档输出子 skill，负责从多种输入源**并行获取**内容，生成结构化的技术文档。它作为**被调用方**，不直接面向终端用户，而是由 PM-Agent、Tech-Lead 等上游 skill 通过标准 YAML 请求触发。

### 核心特性

| 特性 | 说明 |
|------|------|
| **并行获取** | 多输入源同时获取，效率提升 60%+ |
| **多源输入** | 支持代码仓、飞书文档、外部URL、内部工具、直接输入 |
| **多文档类型** | 支持6种文档模板，覆盖研发全流程 |
| **智能融合** | 多源内容自动融合，冲突标记用户裁决 |
| **状态管理** | DRAFT → PENDING_REVIEW → LOCKED |
| **灵活输出** | 本地文件系统 + 飞书文档（可选） |

---

## 快速开始

### 基本用法

```yaml
doc_type: tech_review
input_sources:
  - type: code_repository
    repos:
      - path: /root/.openclaw/workspace/signal
        focus: [架构设计, 核心流程]
  - type: feishu_doc
    docs:
      - url: https://bytedance.larkoffice.com/docx/xxx
        focus: [需求背景]
```

### 调用方式

上游 skill 通过以下方式调用 `shrimp-doc-output`：

```yaml
# 在上游 skill 中调用
call_skill:
  name: shrimp-doc-output
  params:
    doc_type: tech_review
    input_sources:
      - type: code_repository
        repos:
          - path: /root/.openclaw/workspace/signal
            focus: [架构设计]
```

> **注意**：`doc_type` 和至少一个 `input_sources` 为必填参数，`output_path` 和 `options` 可选。

---

### 完整参数说明

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `doc_type` | string | 是 | 文档类型，见"文档类型"章节 |
| `input_sources` | array | 是 | 输入源列表，至少一个 |
| `output_path` | string | 否 | 输出路径，默认 `docs/{doc_type}_{timestamp}.md` |
| `options.template_variant` | string | 否 | 模板变体：`default` / `detailed` / `compact` |
| `options.include_diagrams` | bool | 否 | 是否生成架构图/流程图，默认 `true` |

---

## 并行获取机制

### 核心原则

**多个输入源时，必须并行获取，禁止串行！**

### 效率对比

| 方案 | 耗时 | 说明 |
|------|------|------|
| 串行获取 | 6分钟 | 代码3min + 飞书1min + AIME2min = 6min |
| **并行获取** | **3分钟** | max(3, 1, 2) = 3min，效率提升一倍 |

> 以下数据为理论估算值，基于 `max(各任务耗时)` 模型计算，实际效果受网络和服务负载影响。

### 效率提升数据

| 输入源配置 | 串行耗时 | 并行耗时 | 提升 |
|------------|----------|----------|------|
| 2个代码仓库 + 2个飞书文档 | 8min | 3min | 63% |
| 1个代码仓库 + 1个AIME + 3个Web | 8min | 3min | 63% |
| 3个代码仓库 | 9min | 3min | 67% |
| 5个飞书文档 | 5min | 1min | 80% |

> 以下数据为理论估算值，实际性能取决于各输入源的响应速度。

---

## 输入源详解

### 1. 代码仓库（CC Lead）

通过 Claude Code subagent 分析代码仓库。

**并行说明**：每个代码仓库单独创建一个 CC Lead subagent，所有仓库并行分析。

```yaml
input_source:
  type: code_repository
  repos:
    - path: /root/.openclaw/workspace/signal
      focus:
        - 架构设计
        - 核心流程
      excludes:
        - "**/*_test.go"
        - "**/testdata/**"
```

| 参数 | 必填 | 说明 |
|------|------|------|
| path | 是 | 仓库路径 |
| focus | 是 | 重点关注的分析维度 |
| branches | 否 | 分析的分支，默认 main |
| excludes | 否 | 排除的文件/目录 |

**Focus 关键词参考：**

| 关键词 | 含义 |
|--------|------|
| 架构设计 | 整体架构、模块划分、设计模式 |
| 核心流程 | 业务逻辑、状态机 |
| 数据结构 | 核心数据结构、消息格式 |
| 接口定义 | RPC/HTTP接口 |
| 错误处理 | 错误码、异常处理 |
| 性能优化 | 并发安全、性能瓶颈 |

---

### 2. 飞书文档

支持多种飞书文档 URL 类型的内容提取。

```yaml
input_source:
  type: feishu_doc
  docs:
    - url: https://bytedance.larkoffice.com/docx/Bxxx
      focus:
        - 需求背景
        - 功能列表
    - url: https://bytedance.larkoffice.com/wiki/wikcnxxx
      focus:
        - 技术约束
```

**支持的 URL 类型：**

| 类型 | URL 格式 | 说明 |
|------|----------|------|
| 云文档 | `.../docx/Bxxx` | 新版文档 |
| 旧版文档 | `.../doc/xxx` | 旧版文档 |
| 知识库 | `.../wiki/wikcnxxx` | Wiki 节点 |
| 表格 | `.../sheets/xxx` | 电子表格 |
| 多维表格 | `.../bitable/xxx` | 多维表格 |

---

### 3. 外部 URL（Web）

支持 URL 直接抓取和关键词搜索两种模式。

```yaml
input_source:
  type: web
  urls:
    - url: https://example.com/tech-blog
      focus:
        - 技术方案
      extract_mode: markdown
  searches:
    - query: 相关技术调研
      count: 5
      date_after: 2024-01-01
      language: zh
```

---

### 4. 内部工具

调用 AIME、Argos、Metrics 等字节内部工具获取分析结果。

```yaml
input_source:
  type: internal
  tools:
    - name: aime
      task: 技术可行性分析
      context:
        requirements: |
          需求描述...
    - name: argos
      psm: xxx.service
      query: error
      time_range:
        start: "2026-04-01T00:00:00+08:00"
        end: "2026-04-03T00:00:00+08:00"
```

> 示例中的 `time_range` 为占位符，实际使用时请替换为真实的 ISO 8601 时间范围。

---

### 5. 直接输入

用户直接粘贴的内容，无需获取。

```yaml
input_source:
  type: direct
  content: |
    # 原始内容

    这里可以是用户直接粘贴的文本...
```

---

## 文档类型

### 1. tech_review（技术评审文档）

**适用阶段：** PM-Agent 技术评审

**章节结构：**
1. 需求概述（功能需求、非功能需求）
2. 技术架构方案（整体架构、模块划分）
3. 技术选型（技术栈、选型理由）
4. 接口定义（对外接口、内部接口）
5. 数据模型（核心实体、数据流）
6. 风险评估（高风险项、中低风险项）
7. 决策记录
8. 验收标准

---

### 2. design_doc（设计文档）

**适用阶段：** 详细设计

**章节结构：**
1. 背景与目标
2. 技术方案（总体设计、详细设计）
3. 数据结构
4. 接口详细设计
5. 异常处理
6. 安全性设计
7. 性能考虑
8. 部署方案
9. 迭代计划

---

### 3. research_report（调研报告）

**适用阶段：** 技术调研、竞品分析

**章节结构：**
1. 调研背景
2. 调研方法
3. 现状分析（现有方案、竞品方案）
4. 技术方案分析
5. 推荐方案
6. 风险与挑战
7. 结论与建议

---

### 4. module_plan（模块拆分文档）

**适用阶段：** PM-Agent 模块拆分

**章节结构：**
1. 模块总览
2. 模块依赖关系
3. 详细模块设计
4. 模块间接口汇总
5. 验收标准

---

### 5. task_plan（Task规划文档）

**适用阶段：** PM-Agent Task规划

**章节结构：**
1. Task总览
2. 建议执行顺序
3. 详细Task说明
4. 工作量估算汇总
5. 风险与注意事项

---

### 6. test_plan（测试计划）

**适用阶段：** 测试计划

**章节结构：**
1. 测试范围
2. 测试策略
3. 测试用例设计
4. 测试进度安排
5. 测试交付物
6. 测试风险

---

## 执行流程

**Step 1: 解析请求**
- 解析 doc_type、input_sources、output_path、options
- 识别所有输入源，按类型分组

**Step 2: 并行内容获取**
- 代码仓库：每个仓库 spawn 一个 CC Lead subagent，并行执行
- 飞书文档：每个文档独立调用 feishu_fetch_doc，并行执行
- Web：并行调用 web_fetch / web_search
- 内部工具：并行调用 AIME / Argos / Metrics
- 直接输入：直接使用，无等待时间

**Step 3: 内容融合**
- 一致项 → 直接采纳
- 互补项 → 合并采纳
- 冲突项 → 标记用户裁决

**Step 4: 模板渲染**
- 根据 doc_type 选择模板
- 填充内容到模板
- 标注内容来源

**Step 5: 用户确认 + 文档保存**
- 生成元数据头
- 保存到 `docs/{doc_type}_{timestamp}.md`
- askUserQuestion 发送确认请求（通过 Orchestrator 路由）
- 用户选择：通过 / 修改 / 重新生成
- 通过后状态设为 DRAFT

**Step 6: 发布飞书（可选）**
- 把 markdown 通过 `FeishuPublisher` 发布到飞书 docx
- **v2.4+**: 若 `options.upgrade_charts != False`（默认 ON），自动把 D-ARCH /
  D-SEQ / D-DAG 三类 mermaid 块升级为可交互画板（通过 `chart_publisher.publish_chart()`）
  - 原 mermaid 块在本地 markdown 中作 ground truth 保留
  - 升级失败/workspace 缺失 → fallback 到原 `publisher.publish()`（不阻塞主链路）
  - 详细行为见 `references/doc-output-chart-upgrade.md`

**Step 7: 输出最终产物**
- 更新状态为 LOCKED
- 返回 JSON 给调用方（含 feishu_url + 升级画板的 token 列表）

> Step 编号与 `SKILL.md` 表一致。早期 v2.2 版本 README 把 "用户确认" 单独列为 Step 6，导致与 SKILL.md 编号不同步；v2.4 统一为本表顺序。

---

## 内容融合规则

### 优先级

```
代码仓库 > 飞书文档 > AIME > Web > 直接输入
```

### 融合结果分类

| 分类 | 标记 | 处理方式 |
|------|------|----------|
| 一致项 | ✅ | 直接采纳 |
| 互补项 | 🔄 | 合并采纳 |
| 冲突项 | ⚠️ | 用户裁决 |

---

## 输出格式

执行完成后，skill 返回以下 JSON 结构给调用方：

```json
{
  "skill": "shrimp-doc-output",
  "version": "1.1.0",
  "status": "LOCKED",
  "doc_type": "tech_review",
  "output_path": "docs/tech_review_20260403_1132.md",
  "input_sources_used": [
    { "type": "code_repository", "count": 2, "parallel": true },
    { "type": "feishu_doc", "count": 1, "parallel": true },
    { "type": "web", "count": 3, "parallel": true }
  ],
  "timing": {
    "total_seconds": 180,
    "parallel_fetch_seconds": 165,
    "merge_seconds": 10,
    "render_seconds": 5
  },
  "content_summary": "技术评审文档已完成，包含架构设计、模块划分、接口定义等 5 个章节",
  "gate_conditions": {
    "content_collected": true,
    "template_rendered": true,
    "document_saved": true,
    "user_confirmed": true
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `status` | string | 文档状态：DRAFT / PENDING_REVIEW / LOCKED |
| `doc_type` | string | 文档类型，对应 6 种模板之一 |
| `output_path` | string | 本地文件系统中的文档路径 |
| `input_sources_used` | array | 实际使用的输入源及数量，标注是否并行 |
| `timing` | object | 各阶段耗时统计（用于性能分析） |
| `gate_conditions` | object | 各阶段门控条件是否满足 |

---

## 约束与禁忌

### 流程约束

| 约束 | 说明 |
|------|------|
| ✅ 必须 | 多个输入源时**必须并行获取**，禁止串行 |
| ✅ 必须 | 用户确认后才能标记 LOCKED |
| ❌ 禁止 | 跳过内容获取直接生成 |
| ❌ 禁止 | 忽略多源冲突不标记用户 |

### 内容约束

| 约束 | 说明 |
|------|------|
| ✅ 必须 | 每个章节标注内容来源 |
| ✅ 必须 | 关键决策说明理由 |
| ❌ 禁止 | 混入未经验证的内容 |

---

## 错误处理

### 降级策略

| 输入源 | 降级方案 |
|--------|----------|
| CC Lead subagent 失败 | 直接读取代码文件 / 跳过并警告 |
| 飞书文档失败 | 尝试 wiki 解析 / 让用户提供内容 |
| Web 失败 | 用搜索获取摘要 / 跳过并警告 |
| AIME 请求失败 | 降级为 Web 搜索 / 跳过并警告 |

### 警告级别

| 级别 | 影响 | 处理 |
|------|------|------|
| INFO | 无 | 记录日志，继续执行 |
| WARNING | 部分失败 | 使用降级方案，继续执行 |
| ERROR | 严重失败 | 询问用户是否继续 |

---

## 常见问题（FAQ）

**Q: 多个输入源的内容矛盾时怎么处理？**

A: 系统会按优先级（代码仓库 > 飞书文档 > AIME > Web > 直接输入）自动标记冲突，并通过 `askUserQuestion` 请求用户裁决。

**Q: 可以只用一个输入源吗？**

A: 可以。只需在 `input_sources` 中配置一个源即可，融合阶段会自动跳过。

**Q: 如何保证并行获取的效率？**

A: doc-output skill 会自动分析所有输入源，将可并行的操作（不同仓库、不同文档、不同 URL）同时执行，总耗时取最长的那个。

**Q: 如果某个 subagent 获取失败怎么办？**

A: 系统有降级策略：CC Lead 失败可降级为直接读取文件，飞书失败可尝试 wiki 解析，Web 失败可用搜索摘要。最终结果会标注哪些源失败了。

**Q: 支持自定义模板吗？**

A: 当前版本支持 6 种内置模板。自定义模板可通过 `options.template_variant` 进行微调，完全自定义模板计划在后续版本支持。

---

## 可执行脚本

doc-output 是一套**可执行的 Python 脚本**，支持命令行调用。

### 文件结构

```
doc_output/
├── SKILL.md                    # Skill 定义
├── README.md                   # 本文档
├── doc_output.py              # ⭐ 主执行脚本
├── parallel_fetch.py          # ⭐ 并行获取模块
├── content_merger.py          # ⭐ 内容融合模块
├── template_engine.py         # ⭐ 模板引擎
├── feishu_publisher.py       # ⭐ 飞书发布模块
├── examples/
│   └── cmdb_migration.yaml   # 示例配置
└── references/
    ├── TEMPLATES.md           # 文档模板库
    └── INPUT_SOURCES.md       # 输入源配置
```

### 快速开始

```bash
# 方式1：配置文件
python doc_output.py --config examples/cmdb_migration.yaml

# 方式2：命令行参数
python doc_output.py \
  --doc-type research_report \
  --input-sources code_repository \
  --repos /root/.openclaw/workspace/signal_access
```

详细文档见 SKILL.md 中的「可执行脚本」章节。

---

## 文件结构

```
shrimp/subagent/doc_output/
├── SKILL.md              # 主skill定义
├── README.md             # 本文档
└── references/
    ├── TEMPLATES.md       # 文档模板库
    └── INPUT_SOURCES.md   # 输入源配置详解
```

---

## 更新日志

| 版本 | 日期 | 更新内容 |
|------|------|----------|
| 1.1.0 | 2026-04-03 | **新增并行获取机制**：多输入源并行获取，效率提升 60%+ |
| 1.0.0 | 2026-04-03 | 初始版本，支持6种文档类型、5种输入源 |
