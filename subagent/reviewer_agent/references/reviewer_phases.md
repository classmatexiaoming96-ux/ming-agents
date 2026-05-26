# Reviewer-Agent 执行步骤详解

## 核心架构：纯 Codex 五轴并发审查（v7.1）

Reviewer-Agent 不再自己执行代码审查，而是调度 5 路 **Codex** 并行完成五轴审查：

```
Reviewer-Agent（调度器）
  │
  └─ 五轴并行启动（acpx codex exec × 5）
       ├── Codex #1 → Correctness 审查
       ├── Codex #2 → Security 审查
       ├── Codex #3 → Performance 审查
       ├── Codex #4 → Readability 审查
       └── Codex #5 → Maintainability 审查（自行读 diff + workspace 判定结构）
```

**v7.0 → v7.1 变化**：取消 Phase-1/Phase-2 分阶段架构与 CC Lead 调用。Maintainability prompt 让 codex 直接读 git diff 和 workspace 完成结构判定，并入五轴并发执行。

**为什么这样统一**：
- Codex 轻量并行，5 路同时跑成本远低于"4 路 Codex + 1 路 CC Lead tmux"
- 全部经 `acpx codex exec`，单一外部工具，减少 tmux/cc-start.py 维护负担
- Maintainability 的"整体结构上下文"由 codex 自身的代码读取能力承担，不依赖 Phase-1 结果摘要

---

## rev-n0：审查初始化

> **节点性质**：**AFK** —— 准备 diff / 上下文 / 知识库召回；见 `../../pm_agent/references/afk_hitl.md`

**输入（来自调用方）**：
- `requirement_id`：需求 ID
- `reviewer_type`：correctness / security / performance / maintainability / readability
- `scope`：`full` 或 `increment`
- `feature_branch`：待审查的 Git 分支
- `workspace_dir`：代码工作目录
- `increment_id`（scope=increment 时）：增量 ID，如 `INCR-001-2-2`
- `files_to_review`（scope=increment 时）：本次增量改动的文件列表
- `focus_areas`（可选）：调用方提示的关注点

**执行**：
1. 确认审查范围：
   - `scope=full`：git diff `HEAD~N...HEAD`，列出所有变更文件
   - `scope=increment`：git show `--name-only {commit_hash}`，仅限本次增量文件
2. **召回知识库**：调用 `memory_search` 搜索相关 pitfall / best_practice
3. 读取 `docs/tech_review.md`（了解架构设计意图，如有）
4. 确认审查检查清单（见各轴 Checklist）

**知识库召回查询**：
```
memory_search("pitfall + {reviewer_type} + {requirement_keywords}")
memory_search("best_practice + {reviewer_type} + {requirement_keywords}")
```

**返回（rev-n0 完成）**：
```
tag: autopilot
line: review-line
node: rev-init
goal_status: partial
next_role: rev-axes
outputs:
  knowledge_base_recall:
    pitfalls: [相关踩坑记录列表]
    best_practices: [相关最佳实践列表]
  review_scope:
    scope: full | increment
    files_count: N
    modified_files: [file1.go, file2.go, ...]
    total_lines_changed: N
    git_diff: <git diff 输出>
```

---

## rev-n1：五轴并行 Codex 审查（v7.1）

> **节点性质**：**AFK** —— 5 路 Codex 并发，无用户交互

### 执行策略

五轴（Correctness / Security / Performance / Readability / Maintainability）**全部并行启动**，各自调用 `acpx codex exec`：

```bash
# 并行启动 5 个 Codex session（各自独立）
acpx --approve-all codex exec "REVIEW_PROMPT" 2>&1 | tee /tmp/review-{axis}.log &
```

### 各轴 Codex Prompt 模板

每个 Codex prompt 包含：
- **scope 上下文**：git diff / 变更文件列表
- **axis 专属 checklist**：该轴必须检查的点
- **输出格式要求**：JSON findings
- **knowledge base 召回结果**：相关 pitfall / best practice
- **maintainability 轴额外要求**：自行读 workspace 检测 import cycle、模块耦合等结构信息

#### Codex-Correctness Prompt

```
你是代码正确性审查专家。

## 审查范围
工作目录：{workspace_dir}
变更文件：{modified_files}
Git diff：
{git_diff}

## 正确性检查清单
- [ ] 所有公共函数有正确性相关的单元测试
- [ ] 边界条件被覆盖（空输入、零值、极值）
- [ ] error 返回值被检查，未被默默忽略
- [ ] 并发访问共享变量有适当同步
- [ ] 类型断言使用了安全模式（comma-ok）
- [ ] 无数组/切片越界访问
- [ ] 无 defer + return 导致的资源泄漏

## 知识库参考
{knowledge_base_recall}

## 输出要求
审查完成后，输出以下 JSON（纯 JSON，无其他内容）：
{{"axis": "correctness", "scope": "{scope}", "findings": [{{"file": "...", "line": N, "issue": "...", "severity": "blocker|major|minor|info", "suggestion": "..."}}]}}
```

#### Codex-Security Prompt

```
你是代码安全性审查专家。

## 审查范围
工作目录：{workspace_dir}
变更文件：{modified_files}
Git diff：
{git_diff}

## 安全性检查清单
- [ ] 无敏感信息硬编码（password/token/secret/ak/sk）
- [ ] 所有用户输入经过校验（长度、类型、格式）
- [ ] 数据库查询使用参数化语句，无字符串拼接 SQL
- [ ] 文件路径操作防止路径穿越
- [ ] 权限校验在关键路径上（AuthZ）
- [ ] 敏感数据不出现在日志或错误信息中
- [ ] TLS/加密配置使用安全强度

## 知识库参考
{knowledge_base_recall}

## 输出要求
审查完成后，输出以下 JSON（纯 JSON，无其他内容）：
{{"axis": "security", "scope": "{scope}", "findings": [{{"file": "...", "line": N, "issue": "...", "severity": "blocker|major|minor|info", "suggestion": "..."}}]}}
```

#### Codex-Performance Prompt

```
你是代码性能审查专家。

## 审查范围
工作目录：{workspace_dir}
变更文件：{modified_files}
Git diff：
{git_diff}

## 性能检查清单
- [ ] 无 O(n^2) 或更高复杂度的嵌套循环
- [ ] 数据库查询无 N+1 问题（批量查询替代循环查询）
- [ ] 循环内无大内存分配（预分配 slice/map、使用 sync.Pool）
- [ ] 高并发场景无锁争用（减少锁粒度、使用原子操作）
- [ ] 无 race condition（go test -race 通过）
- [ ] 热路径无不必要的内存分配和拷贝
- [ ] 无 goroutine 泄漏（启动有退出、defer done）

## 知识库参考
{knowledge_base_recall}

## 输出要求
审查完成后，输出以下 JSON（纯 JSON，无其他内容）：
{{"axis": "performance", "scope": "{scope}", "findings": [{{"file": "...", "line": N, "issue": "...", "severity": "blocker|major|minor|info", "suggestion": "..."}}]}}
```

#### Codex-Readability Prompt

```
你是代码可读性审查专家。

## 审查范围
工作目录：{workspace_dir}
变更文件：{modified_files}
Git diff：
{git_diff}

## 统一语言（先读）
若 `{workspace_dir}/CONTEXT.md` 存在，先读它作为领域命名规范词依据；不存在则跳过术语相关检查项。

## 可读性检查清单
- [ ] golangci-lint run 无 error（warning 可记录）
- [ ] 所有导出的函数/类型有 godoc 注释
- [ ] 变量命名有意义（无单字母变量名，例外：i/j/k）
- [ ] 领域术语命名符合 CONTEXT.md 规范词（命中其 `_别名(避免)_` 列出的词 → severity=minor，suggestion 给出规范词；CONTEXT.md 不存在则跳过本项）
- [ ] 函数长度 ≤ 60 行
- [ ] 嵌套 if 层数 ≤ 5
- [ ] 无过长的单行（≤ 120 字符）
- [ ] import 分组清晰（标准库 / 外部 / 内部）
- [ ] 错误信息清晰（包含足够上下文）

## 知识库参考
{knowledge_base_recall}

## 输出要求
审查完成后，输出以下 JSON（纯 JSON，无其他内容）：
{{"axis": "readability", "scope": "{scope}", "findings": [{{"file": "...", "line": N, "issue": "...", "severity": "blocker|major|minor|info", "suggestion": "..."}}]}}
```

#### Codex-Maintainability Prompt

```
你是代码可维护性审查专家。

## 审查范围
工作目录：{workspace_dir}
变更文件：{modified_files}
Git diff：
{git_diff}

## 整体结构判定要求（本轴自行完成，不依赖外部摘要）
在做检查清单之前，请先做以下结构性分析（可直接读 workspace 目录）：
1. 列出本次变更涉及的模块及其 import 关系
2. 检测是否存在 import cycle（提示：`go list -f '{{.ImportPath}} {{.Imports}}' ./...`）
3. 标注新增/修改的公共接口（exported types / funcs）
将结构判定结果作为 maintainability findings 的 evidence 部分。

## 可维护性检查清单
- [ ] 模块划分符合单一职责，无上帝模块
- [ ] 模块间依赖通过接口而非具体实现
- [ ] 无循环依赖（import cycle 检测）
- [ ] 公共 API 有文档注释
- [ ] 错误处理使用 error wrap，保留上下文
- [ ] 配置外部化（无魔法数字、魔法字符串）
- [ ] 可测试性：关键逻辑可通过接口 mock 依赖
- [ ] 无重复代码（>3 处相同逻辑应抽象）
- [ ] 测试文件与实现文件同包、临近放置

## 知识库参考
{knowledge_base_recall}

## 输出要求
审查完成后，输出以下 JSON（纯 JSON，无其他内容）：
{{"axis": "maintainability", "scope": "{scope}", "findings": [{{"file": "...", "line": N, "issue": "...", "severity": "blocker|major|minor|info", "suggestion": "..."}}]}}
```

### 并行执行

```bash
# 5 个 Codex 并行启动（后台）
cd {workspace_dir}
acpx --approve-all codex exec "{correctness_prompt}"     > /tmp/review-correctness.json     2>&1 &
acpx --approve-all codex exec "{security_prompt}"        > /tmp/review-security.json        2>&1 &
acpx --approve-all codex exec "{performance_prompt}"     > /tmp/review-performance.json     2>&1 &
acpx --approve-all codex exec "{readability_prompt}"     > /tmp/review-readability.json     2>&1 &
acpx --approve-all codex exec "{maintainability_prompt}" > /tmp/review-maintainability.json 2>&1 &

# 等待所有 Codex 完成（timeout: 5 分钟）
wait
```

### 解析 Codex 结果

每个 Codex 输出纯 JSON，从 stdout 中提取：
- 解析 `/tmp/review-{axis}.json`
- 提取 `findings` 数组
- 合并到五轴汇聚结果

**返回（rev-n1 完成）**：
```
tag: done
line: review-line
node: rev-axes
goal_status: complete
next_role: orchestrator | dev-agent
scope: full | increment
outputs:
  axis_results:
    correctness:     /tmp/review-correctness.json
    security:        /tmp/review-security.json
    performance:     /tmp/review-performance.json
    readability:     /tmp/review-readability.json
    maintainability: /tmp/review-maintainability.json
  all_findings_count: N
  blocker_count: N
  major_count: N
  minor_count: N
review_conclusion: passed | need_fix | blocked
```

---

## rev-n2：结果汇聚与报告生成

> **节点性质**：**AFK** —— 汇聚 findings + 出报告；Blocker > 0 → 调用方（dev/Orchestrator）处转 Mixed/HITL

**执行**：
1. 读取五个 axis JSON
2. **去重**：同一 file:line 被多轴报告，取 severity 最高
3. **Severity 分级**：
   - Blocker：阻止合并，必须修复
   - Major：重要问题，用户决策
   - Minor (Nit)：次要风格问题，可选修复
   - Info (FYI)：参考信息，无需处理
4. **生成 `docs/review_report.md`**（详细报告）
5. **生成 `docs/findings_summary.md`**（汇总报告，供 Orchestrator 上报）

### Findings 统一格式

```json
{
  "findings": [
    {
      "id": "CORR-001",
      "axis": "correctness",
      "severity": "blocker",
      "title": "API Key 硬编码在代码中",
      "description": "internal/dedup/engine.go:42 存在硬编码的 API Key...",
      "file": "internal/dedup/engine.go",
      "line": 42,
      "suggestion": "使用环境变量或配置中心获取 API Key",
      "source": "knowledge_base:pitfall/api-key-hardcode"
    }
  ],
  "summary": {
    "total": N,
    "blocker": N,
    "major": N,
    "minor": N,
    "info": N
  }
}
```

---

## Review 结论处理

| 结论 | 条件 | Orchestrator 动作 |
|------|------|------------------|
| `passed` | 无 Blocker | 进入 DEPOSIT_PHASE |
| `need_fix` | 有 Major，无 Blocker | 返回 Dev-Agent 修复 |
| `blocked` | 有 Blocker | 阻止合并，立即通知 |

---

## 与 incremental-implementation 联动

每个 INCR commit 成功后，触发五轴并发 review（不等待，Dev-Agent 继续下一 INCR）：

```
Dev-Agent 完成 INCR commit
  │
  └─ 五轴并发（5 个并行 Codex）
       ├── acpx codex exec → Correctness
       ├── acpx codex exec → Security
       ├── acpx codex exec → Performance
       ├── acpx codex exec → Readability
       └── acpx codex exec → Maintainability

Review 结果写入 memory（供后续 INCR 累积和最终汇聚）
```

---

## 工具依赖

| 工具 | 用途 | 调用方式 |
|------|------|---------|
| `acpx` | 启动 Codex ACP session | `acpx --approve-all codex exec "{prompt}"` |

> v7.1 起，reviewer-agent 仅依赖 `acpx`，不再使用 `cc-start.py` / `cc-send.py` / `tmux`。

## 速度规范

| 变更规模 | 五轴并发（5 Codex） |
|----------|---------------------|
| ~100 行（INCR） | ~5 min |
| ~300 行 | ~10 min |
| ~500 行 | ~15 min |
| ≥1000 行 | ~20 min |

> 相比串行 5 轴（约 75 分钟）和 v7.0 两阶段（约 30 分钟），五路全并发节省约 **80% / 50%** 时间。
