# API Contract-First Design 规范（v5.1 新增）

## 背景：Google api-and-interface-design 核心原则

**Hyrum's Law（哈勒姆定律）**：
> 当 API 有足够多的使用者，API 的所有行为都会被消费者依赖，无论是否在文档中声明。
> 因此，**隐蔽行为不可靠**：所有契约必须显式声明。

**One-Version Rule（单一版本规则）**：
> 服务端必须同时只维护一个稳定的 API 版本。破坏性变更必须通过新版本引入，而非修改现有版本。

**Contract-First Design（契约优先设计）**：
> API 实现之前必须先定义契约（Schema + 错误码 + 兼容性策略），确保所有参与者对接口行为达成共识。

## API Design 强制产出物

每个涉及 RPC/HTTP 接口的 Task，在 CC Lead 实现之前，**必须先产出以下三个文件**：

### 1. 请求/响应 Schema（`docs/api/{service}_{method}_schema.yaml`）

```yaml
# 命名规范：docs/api/{service}_{method}_schema.yaml
# 示例：docs/api/dedup_engine_dedup_schema.yaml

api_contract:
  service: DedupEngine
  method: Dedup
  transport: gRPC  # 或 REST/RPC

request:
  name: DedupRequest
  fields:
    - name: alerts
      type: repeated Alert
      rules:
        max_items: 1000      # 边界：最大批次
        required: true
    - name: dedup_key
      type: DedupKey
      rules:
        required: false
        default: "svc_type_host"

response:
  name: DedupResponse
  fields:
    - name: deduplicated
      type: repeated Alert
    - name: total_removed
      type: int32
    - name: processing_time_ms
      type: int64

# Proto 描述（用于生成 .proto 文件）
proto_definition: |
  message DedupRequest {
    repeated Alert alerts = 1 [(rules) = {max_items: 1000}];
    optional DedupKey dedup_key = 2;
  }
  message DedupResponse {
    repeated Alert deduplicated = 1;
    int32 total_removed = 2;
    int64 processing_time_ms = 3;
  }
```

### 2. 错误码字典（`docs/api/{service}_errors.yaml`）

```yaml
# 命名规范：docs/api/{service}_errors.yaml
# 示例：docs/api/dedup_engine_errors.yaml

service: DedupEngine
error_domain: com.shrimp.dedup.v1

errors:
  # 客户端错误（4xx）
  - code: ERR_BATCH_TOO_LARGE
    http_status: 400
    grpc_code: INVALID_ARGUMENT
    message: "单批次告警数量超过上限 {max}"
    retriable: false
    description: "alerts 数量 > 1000 时触发"
    spec_clause: CONTRACT-2.1

  - code: ERR_INVALID_ALERT
    http_status: 400
    grpc_code: INVALID_ARGUMENT
    message: "告警字段缺失或格式错误: {field}"
    retriable: false
    description: "必填字段 ServiceName/AlertType/Severity 缺失"
    spec_clause: CONTRACT-2.1

  # 服务端错误（5xx）
  - code: ERR_ENGINE_UNAVAILABLE
    http_status: 503
    grpc_code: UNAVAILABLE
    message: "Dedup 引擎暂时不可用"
    retriable: true
    retry_interval: 1s
    max_retries: 3
    description: "引擎 Crash 或超载时触发"
    spec_clause: ERROR-001

  - code: ERR_INTERNAL
    http_status: 500
    grpc_code: INTERNAL
    message: "Dedup 引擎内部错误"
    retriable: false
    description: "未预期的内部错误，上报 SRE"
    spec_clause: ERROR-001

# 错误传递规则
error_propagation:
  engine_to_dispatcher: "return (nil, error)"
  dispatcher_retry: 3
  dispatcher_dead_letter: "failed_alerts table"
```

### 3. 向后兼容策略（`docs/api/{service}_backward_compatibility.yaml`）

```yaml
# 命名规范：docs/api/{service}_backward_compatibility.yaml
# 示例：docs/api/dedup_engine_backward_compatibility.yaml

service: DedupEngine
version: v1
stability: STABLE

field_addition:
  rule: "只允许添加 Optional 字段"
  rationale: "既有 client 不读取新字段，不受影响"

field_removal:
  rule: "永远不能删除已发布字段，只能标记为 deprecated"
  action: "标记为 deprecated = true，等待 2 个版本后删除"

field_type_change:
  rule: "禁止修改字段类型"
  rationale: "Protobuf wire format 不兼容"

enum_handling:
  rule: "只允许追加新枚举值，禁止删除或修改已有枚举值"

migration_path:
  current_version: v1
  next_version: v2 (planned)
  non_breaking_changes:
    - action: "添加可选字段"
      target: "DedupRequest.new_field"
      timeline: "v1.x patch"
```

## API Design 执行流程

```
每个涉及接口的 Task 执行前：
│
├── 1. 读取 SPEC.md 中的 CONTRACT 条款
│
├── 2. 产出三个 API Design 文件（必须先于 CC Lead 实现）
│     ├── docs/api/{service}_{method}_schema.yaml
│     ├── docs/api/{service}_errors.yaml
│     └── docs/api/{service}_backward_compatibility.yaml
│
├── 3. API Design Review（如果有 Reviewer Agent 可用）
│
└── 4. CC Lead 实现
      └── prompt 必须包含：API Design 文件路径 + SPEC CONTRACT 条款编号
```

## API Design 检查清单

```
[ ] 已读取 SPEC.md 中的相关 CONTRACT 条款
[ ] 已产出 docs/api/{service}_{method}_schema.yaml
      - [ ] 所有请求字段有类型和约束规则
      - [ ] 所有响应字段有类型
      - [ ] Proto 定义完整可执行（protobuf 语法正确）
[ ] 已产出 docs/api/{service}_errors.yaml
      - [ ] 错误码覆盖所有边界场景
      - [ ] 每个错误码有对应的 HTTP/gRPC status code
      - [ ] 错误传递规则明确
[ ] 已产出 docs/api/{service}_backward_compatibility.yaml
      - [ ] 字段变更规则符合 Hyrum's Law
      - [ ] enum 处理符合 One-Version Rule
      - [ ] 迁移路径文档化
[ ] API Design 文件已 commit 到 feature 分支
[ ] CC Lead prompt 中包含 API Design 文件路径
```

## CC Lead 调用格式（API Design 增强版）

```json
{
  "task": {
    "task_id": "TASK-001",
    "task_name": "实现 DedupEngine.Dedup 接口",

    "api_design": {
      "schema_file": "docs/api/dedup_engine_dedup_schema.yaml",
      "errors_file": "docs/api/dedup_engine_errors.yaml",
      "compatibility_file": "docs/api/dedup_engine_backward_compatibility.yaml"
    },

    "spec_clauses": [
      "CONTRACT-2.1: DedupEngine.Dedup 输入输出契约",
      "EDGE-001: Critical 告警不收敛",
      "TEST-001: TDD-001~TDD-003 必须实现"
    ],

    "acceptance_criteria": [
      "满足 CONTRACT-2.1 前置/后置条件",
      "Schema 定义与实现完全一致（无遗漏字段）",
      "所有错误码映射到 ERROR-001 中定义的错误码",
      "UT 覆盖率 ≥ 80%"
    ],

    "security_constraints": [
      "禁止硬编码凭证（AK/SK/password/token），必须从环境变量或 KMS 加载",
      "日志中禁止打印敏感字段（password/token/ak/sk），必须脱敏为 ***",
      "所有数据库操作必须参数化，禁止字符串拼接 SQL",
      "用户输入输出必须转义，禁止 XSS",
      "敏感操作必须记录审计日志"
    ],

    "performance_constraints": [
      "所有 RPC/HTTP 调用必须设置 timeout",
      "连接池必须配置 MaxOpenConns/MaxConnsPerHost 等上限",
      "Goroutine 必须有受控生命周期（context / channel / sync）",
      "避免 N+1 查询，使用批量查询或缓存",
      "关键路径必须打点 metrics"
    ]
  }
}
```

## API Design 产物目录结构

```
docs/
├── SPEC.md
├── api/
│   ├── dedup_engine_dedup_schema.yaml
│   ├── dedup_engine_errors.yaml
│   └── dedup_engine_backward_compatibility.yaml
├── task_breakdown.md
└── increment_plan.md
```

## 字节内部框架约束（注入 CC Lead prompt）

```markdown
## 字节内部框架约束

### Gloop（Thrift RPC 框架）
- IDL 文件路径规范：`api/{service}.thrift`
- 字段编号规范：请求 1-999，响应 1001-1999
- 兼容性要求：禁止删除字段，使用 union 处理可选

### Kratos（gRPC 框架）
- Proto 文件路径：`api/{service}/v1/{service}.proto`
- 错误码映射：使用 kratos/errors 定义业务错误
- 链路治理：必须添加 tracing middleware

### Hertz（HTTP 框架）
- 路由规范：/api/{version}/{service}/{method}
- 请求ID：必须传递 X-Request-ID header
- 限流：必须实现令牌桶限流 middleware
```
