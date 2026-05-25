# Dev-Agent Documentation 规范

> 版本：1.0.0 | 关联：Dev-Agent SKILL.md v5.1+ | 依赖：SPEC.md、api-and-interface-design

---

## 概述

Dev-Agent 的文档产出是 Shrimp 研发体系的核心资产。文档质量直接影响代码可维护性、团队协作效率和系统可靠性。

**核心原则**：代码即文档（Self-Documenting Code）。代码本身应足够清晰，文档用于补充契约、示例和决策记录。

---

## 目录结构

每个服务的标准文档结构：

```
{service}/
├── README.md                    # 服务入口文档（必填）
├── CHANGELOG.md                 # 变更记录（必填，Keep a Changelog）
├── docs/
│   ├── SPEC.md                  # 需求规格（PM-Agent 产出，Dev-Agent 参考）
│   └── api/                     # API 文档（Dev-Agent 产出）
│       ├── {service}_{method}_schema.yaml
│       ├── {service}_errors.yaml
│       └── {service}_backward_compatibility.yaml
└── internal/
    └── ...                      # 代码实现
```

---

## 1. README.md 规范

每个仓库/服务必须有 `README.md`，必须包含以下四个部分：

### 1.1 必须包含的四个部分

```markdown
# {服务名称}

## What
{一句话描述服务做什么，解决什么问题}

## Why
{为什么需要这个服务，与其他服务的关系，边界在哪里}

## How
{快速开始：编译、运行、配置}

## Examples
{最小可运行的示例代码}
```

### 1.2 模板

```markdown
# DedupEngine

## What
DedupEngine 是告警收敛引擎，负责对输入的告警列表进行去重收敛，
输出去重后的告警列表和统计信息。

**核心能力**：
- 基于 (ServiceName, AlertType, Host) 的去重
- Critical 级别告警不过去重
- 支持 feature_flag 热开关

## Why
原始告警量大（10000+/分钟），需要收敛后按优先级呈现，
避免告警风暴。DedupEngine 专注于去重，与 Dispatcher/AlertStore 协作。

**上下游依赖**：
- 上游：Dispatcher（推送告警）
- 下游：AlertStore（存储结果）

## How

### 编译
```bash
go build ./cmd/dedupengine
```

### 运行
```bash
./dedupengine -config ./conf/dedup.yaml
```

### 配置
```yaml
# conf/dedup.yaml
dedup:
  enabled: true
  key_fields: ["service_name", "alert_type", "host"]
  critical_bypass: true
```

### 测试
```bash
go test ./internal/dedup/... -v
```

## Examples

### RPC 调用示例（gRPC）

```go
conn, _ := grpc.Dial("localhost:9090", grpc.WithInsecure())
client := pb.NewDedupEngineClient(conn)

resp, err := client.Dedup(context.Background(), &pb.DedupRequest{
    Alerts: []*pb.Alert{
        {ServiceName: "payment", AlertType: "high_error_rate", Host: "h1"},
        {ServiceName: "payment", AlertType: "high_error_rate", Host: "h1"},
    },
})
// resp.Deduplicated 包含 1 条去重后的告警
```

### HTTP API 调用示例

```bash
curl -X POST http://localhost:8080/api/v1/dedup \
  -H "Content-Type: application/json" \
  -d '{"alerts":[{"service_name":"payment","alert_type":"high_error_rate","host":"h1"}]}'
```

## Limits & Quotas

| 维度 | 限制 |
|------|------|
| 单批次告警数 | ≤ 1000 |
| 单次请求大小 | ≤ 1 MB |
| QPS | ≤ 1000 |
| 并发连接数 | ≤ 500 |

## Error Codes

| 错误码 | HTTP | 说明 |
|--------|------|------|
| ERR_BATCH_TOO_LARGE | 400 | 批次超限 |
| ERR_INVALID_ALERT | 400 | 字段缺失 |
| ERR_ENGINE_UNAVAILABLE | 503 | 服务不可用 |
| ERR_INTERNAL | 500 | 内部错误 |
```

### 1.3 README.md 检查清单

```
[ ] 包含 What/Why/How/Examples 四个部分
[ ] How 部分有编译、运行、测试命令
[ ] Examples 有可运行的最小示例
[ ] 包含 Limits & Quotas 节（QPS、批次大小等）
[ ] 包含 Error Codes 节（与 docs/api/{service}_errors.yaml 一致）
[ ] README 中的示例与代码实现一致（自验）
```

---

## 2. API 文档规范

API 文档是 Dev-Agent 的核心产出，必须在 CC Lead 实现之前完成。

### 2.1 API 文档产出时机

```
每个涉及 RPC/HTTP 接口的 Task 执行流程：
│
├── 1. 读取 SPEC.md 中的 CONTRACT 条款
│
├── 2. 产出 API Design 三个文件（必须先于 CC Lead 实现）
│     ├── docs/api/{service}_{method}_schema.yaml
│     ├── docs/api/{service}_errors.yaml
│     └── docs/api/{service}_backward_compatibility.yaml
│
├── 3. CC Lead 实现（prompt 包含 API Design 文件路径）
│
└── 4. 在 README.md 中补充 Examples 节
```

### 2.2 API 文档内容要求

**每个 API 必须包含**：

| 内容 | 说明 | 对应文件 |
|------|------|----------|
| 请求/响应 Schema | 字段名、类型、约束 | `{service}_{method}_schema.yaml` |
| 错误码字典 | 所有错误场景及处理方式 | `{service}_errors.yaml` |
| 向后兼容策略 | 字段变更规则 | `{service}_backward_compatibility.yaml` |
| 请求示例 | 最小可运行的 curl / gRPC 调用 | README.md Examples 节 |
| 响应示例 | 成功和错误响应的 JSON | README.md Examples 节 |
| 限流策略 | QPS、批次大小、超时 | README.md Limits 节 |

### 2.3 响应示例格式

**成功响应**：
```json
{
  "deduplicated": [
    {
      "id": "alert-001",
      "service_name": "payment",
      "alert_type": "high_error_rate",
      "severity": "CRITICAL",
      "host": "h1",
      "timestamp": "2026-04-05T10:00:00Z"
    }
  ],
  "total_removed": 1,
  "processing_time_ms": 12
}
```

**错误响应**：
```json
{
  "error": {
    "code": "ERR_BATCH_TOO_LARGE",
    "message": "单批次告警数量超过上限 1000",
    "details": {
      "actual": 1500,
      "max": 1000
    }
  }
}
```

### 2.4 限流策略文档模板

```markdown
## Limits & Quotas

| 维度 | 限制 | 说明 |
|------|------|------|
| 单批次大小 | 1000 条 | 超过返回 ERR_BATCH_TOO_LARGE |
| 请求超时 | 500ms | 超时返回 ERR_ENGINE_UNAVAILABLE |
| QPS | 1000 | 超过触发限流 |
| 并发连接 | 500 | HTTP/2 连接池上限 |
```

---

## 3. 代码注释规范

**原则**：代码即文档。注释是补充，不是替代。

### 3.1 必须注释的场景

| 场景 | 注释内容 | 示例 |
|------|----------|------|
| 公开 API 方法 | 方法用途、输入、输出、不确定性 | 见下方 |
| 关键算法 | 算法选择的原因、参考 | "使用滑动窗口而非固定窗口，避免临界点突刺" |
| feature_flag | 开关作用、降级路径 | "关闭时返回原始告警，不收敛" |
| 非 очевидный 逻辑 | 业务规则来源 | "// 业务规则：连续 3 次 Critical 才告警" |
| 外部依赖 | 依赖来源、版本、用途 | "// 使用 kratos v2.5，来源：go.mod" |
| 警告/待办 | 使用 TODO/FIXME/HACK | "// TODO(v2.1): 替换为更高效的算法" |

### 3.2 注释风格

**Go 语言**：

```go
// Dedup 对告警列表进行去重收敛。
//
// 输入：
//   - alerts: 原始告警列表，单次最多 1000 条
//   - opts: 去重选项，可选
//
// 输出：
//   - deduplicated: 去重后的告警列表
//   - totalRemoved: 被去重的数量
//   - error: 错误信息，仅在引擎内部错误时返回
//
// 注意：
//   - Critical 级别告警不过去重（见 spec: EDGE-001）
//   - 当 feature_flag SHRIMP_XXX 关闭时，直接返回原始列表
//
// Hyrum's Law：本方法的行为是 API 契约，调用方依赖此行为。
func (e *Engine) Dedup(alerts []Alert, opts *DedupOptions) (deduplicated []Alert, totalRemoved int, err error) {
    // ...
}
```

**禁止注释**：

```go
// 错误示例：重复代码、不言自明、误导性注释

// 检查是否为 nil
if err != nil {
    return nil, err
}

// 循环处理每个 alert
for _, alert := range alerts {
    process(alert)
}
```

### 3.3 注释检查清单（Code Review）

```
[ ] 所有导出（exported）的函数/方法有注释
[ ] 注释说明"做什么"，不说明"怎么做"（代码本身说明怎么做）
[ ] 非 очевидный 业务逻辑有注释解释原因
[ ] feature_flag 的作用和降级路径有注释
[ ] TODO/FIXME 有负责人或版本号
[ ] 没有误导性注释（注释与实际行为不一致）
[ ] 注释与代码同步更新（PR 中一起改）
```

---

## 4. CHANGELOG 管理规范

### 4.1 格式：Keep a Changelog

所有服务必须使用 [Keep a Changelog](https://keepachangelog.com/) 格式。

### 4.2 CHANGELOG.md 模板

```markdown
# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- {功能描述}

### Changed
- {变更描述}

### Deprecated
- {即将移除的功能}

### Removed
- {已移除的功能}

### Fixed
- {bug 修复}

### Security
- {安全相关}

## [{version}] - {date}

### Added
- 初始发布
```

### 4.3 CHANGELOG 撰写规则

**每次 merge 到 main/master 必须更新 CHANGELOG.md**：
- 新功能 → `### Added`
- 行为变更 → `### Changed`
- bug 修复 → `### Fixed`
- 废弃警告 → `### Deprecated`
- 删除字段/接口 → `### Removed`
- 安全加固 → `### Security`

### 4.4 与 increment 对应

每个 `INCR-XXX-Y` commit 对应 CHANGELOG 的一条变更：

```
## [Unreleased]

### Added
- [INCR-001-2-2] 新增 DedupEngine.Dedup 方法，支持基于 (svc, type, host) 去重

### Changed
- [INCR-001-2-4] Dedup 算法从 O(n²) 优化为 O(n)，延迟降低 50%
```

### 4.5 CHANGELOG 检查清单

```
[ ] 每次 merge 到 main/master 前更新 CHANGELOG.md
[ ] 使用 Unreleased 节记录未发布变更
[ ] 每个变更关联 INCR ID
[ ] 语义化版本号正确（MAJOR.MINOR.PATCH）
[ ] Breaking Changes 有明确标注（!!! 标记）
[ ] 无 personal pronoun（用"we"而非"I"）
```

### 4.6 Breaking Changes 标注

```markdown
### Changed

- !!! [INCR-003-1] DedupRequest.dedup_key 字段类型从 string 改为 DedupKey
  - 旧 client 需要升级才能兼容
  - 迁移路径见 docs/api/dedup_engine_backward_compatibility.yaml
```

---

## 5. 与其他 Agent 的联动

### 5.1 与 SPEC.md 的关系

```
SPEC.md（PM-Agent 产出）
  │
  ├── CONTRACT 条款 → API Schema 定义
  ├── EDGE 条款 → 注释和 README 中的边界说明
  ├── ERROR 条款 → 错误码字典
  └── TEST 条款 → UT 覆盖范围
```

**Dev-Agent 责任**：
- 实现符合 SPEC.md 的所有 CONTRACT/EDGE/ERROR 条款
- SPEC.md 中的接口描述与 API Schema 完全一致
- SPEC.md 中的约束条件在代码和 README 中体现

### 5.2 与 api-and-interface-design 的关系

```
API Contract-First Design 执行流程：
│
├── 1. 读取 SPEC.md CONTRACT 条款
│
├── 2. 产出三个 API Design 文件
│     ├── {service}_{method}_schema.yaml（请求/响应）
│     ├── {service}_errors.yaml（错误码）
│     └── {service}_backward_compatibility.yaml（兼容性）
│
├── 3. 自验：Schema 与 SPEC CONTRACT 一致
│
├── 4. CC Lead 实现（Schema 作为 contract）
│
└── 5. 自验：实现与 Schema 一致

API Design 产出物：
  ├── README.md Examples ← 引用 Schema
  ├── CHANGELOG.md ← 记录 API 变更
  └── docs/api/ ← 原始契约文件
```

### 5.3 与 spec-driven 的关系

SPEC.md 是 Dev-Agent 的核心文档，所有实现必须对照 SPEC.md。

**Dev-Agent 执行时必须**：
1. 读取 SPEC.md 中关联的条款（CONTRACT/EDGE/ERROR/TEST）
2. 将条款编号写入 CC Lead prompt（如 `spec_clauses: [CONTRACT-2.1, EDGE-001]`）
3. 自验时对照 SPEC.md 逐条检查
4. 发现 SPEC 与实现的差异立即上报 Orchestrator

---

## 6. Documentation 执行流程

### 6.1 每个 Increment 的文档产出

```
INCR 执行时：
│
├── 实现前（必须）
│     ├── API Schema 文件 → docs/api/
│     └── 更新 SPEC.md 对应条款（如需）
│
├── 实现中（由 CC Lead 产出）
│     ├── 代码注释（按注释规范）
│     └── 单元测试
│
├── 实现后（必须）
│     ├── 更新 README.md Examples（如新增接口）
│     └── 更新 CHANGELOG.md
│
└── Commit 前（检查清单）
      └── [ ] README.md 自验通过
```

### 6.2 Documentation 检查清单

**README.md**：
```
[ ] 包含 What/Why/How/Examples 四个部分
[ ] Examples 可直接运行
[ ] Limits & Quotas 与 API Schema 一致
[ ] Error Codes 与 docs/api/{service}_errors.yaml 一致
[ ] 代码示例与实现一致
```

**API Schema**：
```
[ ] 请求字段有类型和约束
[ ] 响应字段有类型
[ ] Proto 定义语法正确
[ ] 与 SPEC.md CONTRACT 条款一致
```

**代码注释**：
```
[ ] 导出的函数有注释
[ ] 注释与代码同步
[ ] feature_flag 有降级说明
```

**CHANGELOG.md**：
```
[ ] 有 Unreleased 节
[ ] 本次变更已录入
[ ] 关联 INCR ID
[ ] Breaking Changes 有 !!! 标注
```

---

## 7. 模板文件

### 7.1 README.md 最小模板

```markdown
# {服务名称}

## What
{一句话描述}

## Why
{为什么存在，边界在哪里}

## How

### 编译
\`\`\`bash
go build ./cmd/{service}
\`\`\`

### 运行
\`\`\`bash
./{service} -config ./conf/{service}.yaml
\`\`\`

### 测试
\`\`\`bash
go test ./... -v
\`\`\`

## Examples

### RPC
\`\`\`go
// 见 docs/api/{service}_{method}_schema.yaml
\`\`\`

### HTTP
\`\`\`bash
curl -X POST http://localhost:8080/api/v1/{method}
\`\`\`

## Limits & Quotas

| 维度 | 限制 |
|------|------|
| 单批次大小 | 1000 |
| QPS | 1000 |

## Error Codes

| 错误码 | HTTP | 说明 |
|--------|------|------|
| ERR_XXX | 400 | {说明} |
```

### 7.2 CHANGELOG.md 最小模板

```markdown
# Changelog

## [Unreleased]

### Added
- {INCR-ID} {描述}

### Changed
- {INCR-ID} {描述}

### Fixed
- {INCR-ID} {描述}

## [{version}] - {YYYY-MM-DD}

### Added
- 初始发布
```
