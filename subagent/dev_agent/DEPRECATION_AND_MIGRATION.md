# Deprecation and Migration Guide

> 版本：v1.0.0  
> 日期：2026-04-05  
> 状态：正式版  
> 角色：Dev-Agent 废弃迁移执行指南  
> 依赖：INCREMENTAL_IMPLEMENTATION_INTEGRATION（v1.0.0）+ API Contract-First Design（SKILL.md v5.1）

---

## 一、概念定义

### 1.1 废弃类型分类

| 类型 | 英文 | 定义 | 示例 | 影响范围 |
|------|------|------|------|----------|
| **Protocol 废弃** | Protocol Deprecated | 通信协议层面的废弃 | Thrift 字段编号冲突、gRPC 方法签名变更 | 跨语言 client |
| **API 废弃** | API Deprecated | HTTP/RPC 接口层面的废弃 | REST endpoint 路径变更、gRPC method 废弃 | 直接调用方 |
| **Feature 废弃** | Feature Deprecated | 功能特性的废弃 | 某个算法、策略不再维护 | 功能使用者 |
| **字段废弃** | Field Deprecated | 消息体字段的废弃 | Protobuf 字段标记 deprecated | 消息序列化 |

### 1.2 废弃等级

| 等级 | 描述 | 迁移窗口 | 影响 |
|------|------|----------|------|
| **L1-Critical** | 破坏核心业务流程 | 立即生效，0 天 | 所有用户 |
| **L2-High** | 影响部分功能 | ≤ 30 天 | 部分用户 |
| **L3-Medium** | 非核心路径变更 | ≤ 90 天 | 高级用户 |
| **L4-Low** | 文档化行为变更 | 下一个大版本 | 极少数用户 |

---

## 二、ADR（Architecture Decision Records）规范

### 2.1 什么时候需要 ADR

**必须创建 ADR 的场景**：

| 场景 | 触发条件 | ADR 类型 |
|------|----------|----------|
| Protocol/IDL 重大变更 | 字段编号变化、消息结构变化 | Protocol ADR |
| API 版本跳跃 | API v1 → v3（非 v2） | Migration ADR |
| 破坏性变更 | 删除字段、修改字段类型 | Breaking Change ADR |
| 新增枚举值 | enum 新增状态 | Enum ADR |
| 安全策略变更 | 认证/鉴权方式变化 | Security ADR |
| 跨服务协议变更 | 上游/下游接口变更 | Integration ADR |
| 重大架构重构 | 替换核心组件 | Architecture ADR |

**不需要 ADR 的场景**：
- 纯内部实现重构，不影响外部契约
- Bugfix，不改变公开行为
- 新增 Optional 字段（向后兼容）
- 文档修正

### 2.2 ADR 格式模板

文件名规范：`docs/adr/{service}_{decision_topic}_{YYYYMMDD}.md`

```markdown
# ADR-{number}: {简短标题}

**日期**：{YYYY-MM-DD}  
**状态**：{Proposed | Accepted | Deprecated | Superseded}  
**决策者**：{team_name}  
**影响等级**：{L1-Critical | L2-High | L3-Medium | L4-Low}

---

## 背景

{描述导致这个决策的业务/技术背景}

### 问题陈述

{用一句话描述要解决的问题}

### 约束条件

- {约束 1}
- {约束 2}

---

## 决策

### 方案 A（最终选择）

**描述**：{简述方案}

**优点**：
- {优点 1}

**缺点**：
- {缺点 1}

### 方案 B（被拒绝）

**描述**：{简述方案}

**拒绝原因**：{为什么不选这个方案}

---

## 后果

### 向后兼容性

- [ ] 完全兼容（无破坏性变更）
- [ ] 部分兼容（需客户端配合）
- [ ] 不兼容（需要迁移）

### 迁移路径

{描述客户端如何从旧版本迁移到新版本}

### Feature Flag 清单

| Flag Name | 类型 | 默认值 | 何时删除 |
|-----------|------|--------|----------|
| {flag_name} | {switch | gradual | permanent} | {default} | {when_to_remove} |

### 需要更新的文档

- [ ] API Schema
- [ ] 错误码字典
- [ ] SDK 版本要求
- [ ] 迁移指南

---

## 相关 ADR

- ADR-{number}: {相关文档标题}

---

## 审查记录

| 日期 | 审查者 | 意见 | 结果 |
|------|--------|------|------|
| YYYY-MM-DD | {name} | {comment} | Approved |
```

### 2.3 ADR 编号规范

```
格式：ADR-{4位序号}
示例：ADR-0001, ADR-0002, ADR-0025

序号分配规则：
- 按创建顺序递增，不复用
- 大版本里程碑ADR单独编号段（如 ADR-0100, ADR-0200）
- Superseded 的 ADR 保留原编号，状态改为 Superseded
```

---

## 三、废弃协议迁移流程

### 3.1 完整迁移状态机

```
【废弃发起】
    │
    ├── 识别废弃需求（业务变更 / 技术债务 / 安全要求）
    │
    ▼
【Phase 1: 评估与规划】（1-3 天）
    │
    ├── 影响分析：哪些 client 受影响，百分比
    ├── 废弃等级评估：L1 ~ L4
    ├── 创建 ADR（如需）
    ├── 设计迁移路径
    │
    ▼
【Phase 2: 废弃声明】（与开发并行）
    │
    ├── 在 Schema/IDL 中标记字段/方法为 deprecated
    ├── 错误码字典中添加废弃警告码
    ├── 更新 API 文档
    ├── 发送废弃通知（邮件/飞书/公告）
    │
    ▼
【Phase 3: 灰度迁移】
    │
    ├── 启用 feature_flag 控制新旧逻辑
    ├── 新客户端：默认使用新协议
    ├── 老客户端：继续使用旧协议（向后兼容）
    ├── 监控废弃路径使用率
    │
    ▼
【Phase 4: 强制迁移期】（按废弃等级倒计时）
    │
    ├── 使用率 < 5%：发送最终提醒
    ├── 使用率 < 1%：通知进入 7 天倒计时
    │
    ▼
【Phase 5: 下线】
    │
    ├── 关闭旧协议 feature_flag
    ├── 旧代码标记为删除（但保留一段时间）
    ├── 下线后保留 ADR 作为历史记录
```

### 3.2 Protocol 废弃执行步骤

**Step 1: 影响范围分析**

```bash
# 分析哪些服务/客户端依赖此 Protocol
# 检查 IDL 使用情况
grep -r "deprecated_field" api/*.thrift api/*.proto

# 统计调用量
argosis show {psm} --metric deprecated_call_rate --period 7d
```

**Step 2: Schema 标记**

```yaml
# 在 IDL 中标记废弃字段
# protobuf 示例
message Alert {
  string id = 1;
  
  // DEPRECATED: 使用 new_severity 替代，2026-Q3 删除
  // Will be removed after: 2026-09-30
  // Migration: client 迁移到 new_severity field
  reserved 3;  // 保留编号，阻止复用
  // AlertType alert_type = 3 [deprecated = true];  // 注释掉，不能删除
  
  string new_severity = 4;  // 新字段
}
```

**Step 3: 向后兼容实现**

```go
// Go 示例：处理废弃字段
func (a *Alert) GetSeverity() string {
    // 优先使用新字段
    if a.NewSeverity != "" {
        return a.NewSeverity
    }
    // 降级到废弃字段（仅用于兼容）
    if a.AlertType != "" {
        log.Printf("[DEPRECATED] Alert.AlertType is deprecated, use Alert.NewSeverity")
        return string(a.AlertType)
    }
    return ""
}
```

**Step 4: Feature Flag 配置**

```yaml
# increment_plan.md 中的 feature_flag 配置
deprecated_compatibility:
  SHRIMP_DEPRECATED_ALERT_TYPE:
    default_value: true          # 兼容模式开启（老客户端不受影响）
    phase_1_rollback: false      # 灰度后保持 true
    description: "Alert.Type 字段兼容性开关"
    removal_timeline: "2026-Q3"
    migration_guide: "clients should migrate to NewSeverity"
```

### 3.3 API 废弃执行步骤

**Step 1: 错误码字典更新**

```yaml
# docs/api/{service}_errors.yaml 新增废弃相关错误码
errors:
  - code: ERR_API_DEPRECATED
    http_status: 410
    grpc_code: UNAVAILABLE
    message: "此 API 已废弃，请使用 {new_api_endpoint}"
    retriable: false
    description: "API 废弃警告，指导客户端迁移"
    spec_clause: DEPRECATION-001
    migration_url: "https://docs.example.com/migration/v1_to_v2"
```

**Step 2: 响应头添加 Deprecation 头**

```go
// HTTP Response Headers
// RFC 8594 Sunset Header
func setDeprecationHeaders(w http.ResponseWriter, apiVersion string, sunsetDate string) {
    w.Header().Set("Deprecation", "true")
    w.Header().Set("Sunset", sunsetDate)  // RFC 8594
    w.Header().Set("Link", "<{migration_url}>; rel=\"deprecation\"")
    w.Header().Set("X-API-Deprecation", apiVersion)
}
```

**Step 3: API 版本路由**

```yaml
# API Gateway 路由配置
routes:
  - path: /api/v1/alert
    deprecated: true
    sunset_date: "2026-09-30"
    migration_path: /api/v2/alert
    # 2026-10-01 后自动返回 410 Gone
    
  - path: /api/v2/alert
    stable: true
    min_client_version: "2.0.0"
```

---

## 四、与 Incremental Implementation 的联动

### 4.1 废弃 Feature Flag 生命周期

废弃 feature flag 与普通 feature flag 的关键区别：

| 阶段 | 普通 Flag | 废弃 Flag |
|------|-----------|-----------|
| **引入** | 默认 false，功能开发完成后开启 | 默认 true（保持兼容） |
| **灰度** | false → true 灰度 | true → false 灰度（逐步关闭兼容） |
| **稳定期** | 稳定开启 | 保持 true 兼容老客户端 |
| **废弃期** | 永久保留或删除 | 逐步将默认值改为 false |
| **删除** | 代码删除，flag 消失 | 先改默认值，再等窗口期后删除 |

### 4.2 废弃 Flag 执行流程

```
废弃决策确定（ADR 已完成）
    │
    ▼
【Step 1: Flag 状态修改】
    │
    # increment_plan.md 中修改 flag 配置
    SHRIMP_DEPRECATED_OLD_API:
      default_value: true      # 保持兼容
      phase: deprecation       # 标记为废弃阶段
      removal_date: 2026-09-30 # 计划删除日期
    │
    ▼
【Step 2: 创建废弃迁移 Increment（INCR）】

INCR-{task}-{deprecation}-1: 标记废弃
  - IDL/Schema 中添加 deprecated 注释
  - 响应中添加 Deprecation Header
  - 错误码字典添加废弃警告码

INCR-{task}-{deprecation}-2: 降级路径实现
  - 实现新 API 调用路径
  - 新客户端使用新路径
  - 老客户端继续走旧路径

INCR-{task}-{deprecation}-3: 监控埋点
  - 统计旧 API 调用量
  - 统计老客户端分布
  - 设置使用率告警（>5% 触发）

INCR-{task}-{deprecation}-N: 清理旧代码
  - 将 flag 默认值改为 false
  - 或在足够短的窗口后直接删除代码
```

### 4.3 废弃 Increment 模板

```yaml
increment_id: INCR-{task}-{deprecation}-{seq}
parent_sub_task_id: TASK-{task}

name: "DEPRECATION: {废弃目标}"
description: |
  废弃 {old_feature}，迁移到 {new_feature}
  - 废弃等级：{L1-L4}
  - 迁移窗口：{N} 天
  - 受影响客户端：{percentage}%

feature_flag:
  name: SHRIMP_DEPRECATED_{feature}
  scope: endpoint_level
  default_value: true        # 【关键】废弃 flag 默认 true（保持兼容）
  deprecation_phase: true     # 标记为废弃阶段

deprecation_info:
  level: {L1-L4}
  sunset_date: "{YYYY-MM-DD}"
  migration_path: "/api/v2/{endpoint}"
  affected_clients: "{percentage}%"

acceptance_criteria:
  - "旧 API 仍可访问（向后兼容）"
  - "响应包含 Deprecation Header"
  - "监控面板可看到旧 API 调用量"
  - "ADR 已创建并存档"

rollback:
  git_revert: "git revert {commit}"
  flag_rollback: "SHRIMP_DEPRECATED_{feature}=true（回到兼容模式）"

status: pending
```

### 4.4 废弃期 Flag 管理命令

```bash
# 监控废弃 API 使用率
argosis query --psm {psm} --metric deprecated_api_rate --period 7d

# 当使用率低于阈值，触发清理 INCR
# 阈值：L1=0%, L2=1%, L3=5%, L4=10%

# 设置 flag 为 false（前置条件：使用率 < 阈值）
feature_flag set SHRIMP_DEPRECATED_{feature}=false --env prod

# 验证降级成功
argosis query --psm {psm} --metric new_api_rate --period 1h

# 删除旧代码 INCR 触发条件
# 1. flag = false 已生效 7 天以上
# 2. 监控确认无旧 API 调用
# 3. Reviewer Agent 确认清理 INCR 已通过
```

---

## 五、与 API Contract-First Design 的联动

### 5.1 API 废弃时的向后兼容策略矩阵

| 变更类型 | 向后兼容 | 策略 |
|----------|----------|------|
| 添加 Optional 字段 | ✅ 兼容 | 直接添加，老 client 忽略 |
| 添加必填字段 | ❌ 破坏 | 新 API 版本添加 |
| 删除 Optional 字段 | ⚠️ 废弃 | 先标记 deprecated，等待 2 版本后删除 |
| 删除必填字段 | ❌ 破坏 | 不可接受，应始终保持 |
| 修改字段类型 | ❌ 破坏 | 新 API 版本实现 |
| 修改字段编号 | ❌ 破坏 | 禁止，Protobuf wire format 不兼容 |
| 枚举新增值 | ✅ 兼容 | 直接添加，老 client 收到 unknown |
| 枚举删除/修改值 | ❌ 破坏 | 新 API 版本实现 |
| 修改 Method 签名 | ❌ 破坏 | 新 API 版本实现 |

### 5.2 API 废弃版本管理

**One-Version Rule 实施**：

```
同一时刻只维护一个稳定版本
    │
    ├── API v1（STABLE）← 当前稳定版
    │     └── 接收 bugfix 和安全更新
    │
    ├── API v1.1（DEPRECATED）
    │     └── 2026-06-30 Sunset
    │     └── 返回 Deprecation Header
    │
    └── API v2（STABLE）← 2026-04-01 发布
          └── 新功能唯一入口
```

**版本选择算法**：

```go
// API 版本选择逻辑
func selectAPIVersion(clientVersion, minimumVersion string) (string, error) {
    // 1. 检查客户端版本是否低于最低要求
    if compareVersion(clientVersion, minimumVersion) < 0 {
        return "", ErrClientTooOld
    }
    
    // 2. 优先使用稳定版本
    stableVersion := getStableVersion()
    
    // 3. 如果客户端指定了废弃版本，发出警告
    if clientRequestedDeprecated(clientVersion) {
        log.Printf("[DEPRECATED] client using deprecated version %s", clientVersion)
        addDeprecationHeader(response)
    }
    
    return stableVersion, nil
}
```

### 5.3 废弃迁移 INCR 序列模板

当一个 API 需要从 v1 迁移到 v2 时：

```yaml
# 【INCR-001】API v2 Schema 定义（Type-A: 新建文件）
increment_id: INCR-{task}-api-v2-1
feature_flag: null  # Schema 定义不需要 flag
files:
  - docs/api/{service}_v2_schema.yaml
  - docs/api/{service}_v2_errors.yaml
  - docs/api/{service}_v2_backward_compatibility.yaml

# 【INCR-002】API v2 路由实现（Type-D: 新旧切换）
increment_id: INCR-{task}-api-v2-2
feature_flag:
  name: SHRIMP_{service}_USE_V2
  default_value: false  # 默认走 v1

# 【INCR-003】v1 标记废弃（Type-C: 增强）
increment_id: INCR-{task}-api-v2-3
feature_flag:
  name: SHRIMP_{service}_V1_COMPAT
  default_value: true  # 保持 v1 兼容

# 【INCR-004】灰度切换（Type-D: 灰度）
increment_id: INCR-{task}-api-v2-4
feature_flag:
  name: SHRIMP_{service}_USE_V2
  default_value: true  # 灰度开启

# 【INCR-005】v1 下线（Type-F: 配置变更）
# 触发条件：v2 使用率 > 95%，v1 使用率 < 5%
increment_id: INCR-{task}-api-v2-5
feature_flag:
  name: SHRIMP_{service}_V1_COMPAT
  default_value: false  # 关闭 v1 兼容
```

### 5.4 跨服务依赖的废弃管理

当上游服务废弃某个接口时，下游服务需要联动：

```yaml
# 上游废弃通知格式
upstream_deprecation_notice:
  psm: upstream.service
  deprecated_endpoint: /api/v1/legacy
  new_endpoint: /api/v2/modern
  sunset_date: "2026-09-30"
  impact_analysis:
    affected_downstream_psms:
      - downstream.service-a
      - downstream.service-b
    estimated_client_count: 150
    migration_complexity: "low"  # low | medium | high
```

**下游服务联动 INCR**：

```yaml
# 下游服务创建的迁移 INCR
increment_id: INCR-{downstream}-upstream-v2-{seq}
name: "迁移到上游 {service} v2"
description: |
  上游服务 v1 将于 2026-09-30 下线
  需要将所有调用从 /api/v1/legacy 迁移到 /api/v2/modern

upstream_dependency:
  psm: upstream.service
  old_endpoint: /api/v1/legacy
  new_endpoint: /api/v2/modern
  migration_deadline: "2026-09-15"  # 提前 15 天完成

feature_flag:
  name: SHRIMP_UPSTREAM_{service}_V2
  default_value: false
  gradual_rollout: true  # 支持按比例灰度

acceptance_criteria:
  - "所有调用已切换到 v2"
  - "集成测试通过"
  - "监控显示 v1 调用量为 0"
```

---

## 六、执行检查清单

### 6.1 废弃发起检查清单

```
[ ] 确认废弃需求来源（业务变更 / 技术债务 / 安全）
[ ] 完成影响分析（哪些 client 受影响）
[ ] 评估废弃等级（L1-L4）
[ ] 创建 ADR（如需）
[ ] 确定迁移窗口期
[ ] 确定 feature_flag 配置
```

### 6.2 废弃开发检查清单

```
[ ] IDL/Schema 中标记废弃字段/方法
[ ] 错误码字典添加废弃警告码
[ ] 实现向后兼容路径
[ ] 添加 Deprecation Response Header
[ ] 实现 feature_flag 切换逻辑
[ ] 添加监控指标（调用量、使用率）
[ ] 产出废弃迁移 INCR 序列
[ ] Reviewer Agent 审核通过
```

### 6.3 废弃上线检查清单

```
[ ] 废弃通知已发送（邮件/飞书/公告）
[ ] 监控告警已配置（使用率 > 阈值触发）
[ ] API 文档已更新
[ ] SDK 已发布（包含废弃警告）
[ ] 迁移指南已发布
[ ] ADR 已存档到 docs/adr/
```

### 6.4 废弃清理检查清单

```
[ ] 使用率低于阈值（按废弃等级）
[ ] 清理 INCR 已创建并 Review 通过
[ ] feature_flag 默认值已修改
[ ] 旧代码已删除（或保留注释）
[ ] ADR 状态更新为 Deprecated
[ ] 监控告警已关闭
```

### 6.5 上线后监控 Checklist

**关键指标观测**：

| 指标 | 监控入口 | 告警阈值 | Rollback 触发条件 |
|------|----------|----------|-------------------|
| 错误率（Error Rate） | Metrics | > 1% | > 5% 持续 5min |
| P99 延迟（Latency） | Metrics | > 基线 20% | > 基线 50% 持续 5min |
| QPS | Metrics | 波动 > 30% | 降至基线 20% 以下 |
| 废弃路径使用率 | Metrics | > 5% | —（观察指标） |

**监控工具**：

- **Argos**：日志搜索
- **Metrics**：指标监控
- **Trace**：调用链追踪

**Rollback 条件**（满足任一即触发）：

```
[ ] 错误率 > 5% 持续 5 分钟
[ ] P99 延迟 > 基线 50% 持续 5 分钟
[ ] QPS 降至基线 20% 以下
[ ] 核心功能成功率 < 95%
```

**Rollback 操作**：

```bash
# 1. 回退 feature_flag
feature_flag set SHRIMP_DEPRECATED_{feature}=true --env prod

# 2. 验证指标恢复
argosis query --psm {psm} --metric error_rate --period 5m

# 3. 上报事件
# 发飞书通知 → 分析根因 → 修复后重新灰度
```

---

## 七、参考文档

| 文档 | 位置 | 用途 |
|------|------|------|
| INCREMENTAL_IMPLEMENTATION_INTEGRATION | `./INCREMENTAL_IMPLEMENTATION_INTEGRATION.md` | feature_flag 生命周期管理 |
| API Contract-First Design | `./SKILL.md#api-contract-first-design-规范v51-新增` | Schema 设计规范 |
| 向后兼容策略 | `docs/api/{service}_backward_compatibility.yaml` | API 兼容性实施指南 |
| ADR 示例 | `docs/adr/` | 架构决策记录参考 |
| Feature Flag Registry | `docs/increment_plan.md#feature_flags` | flag 配置参考 |

---

## 八、变更历史

| 版本 | 日期 | 修改内容 | 修改人 |
|------|------|----------|--------|
| v1.0.0 | 2026-04-05 | 初始版本 | Shrimp Dev-Agent |
