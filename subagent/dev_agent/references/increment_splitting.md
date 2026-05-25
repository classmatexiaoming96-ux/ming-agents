# increment 拆分规范（v5.0 核心）

## increment 拆分粒度规则

- **时间预算**：单个 increment 15-45 分钟（CC Lead 单次调用）
- **文件数量**：每个 increment 改动 ≤ 3 个文件（最佳 ≤ 1 个文件）
- **feature_flag**：每个 increment 有独立 feature_flag（或复用父 sub_task 的 flag），**默认值为 false**
- **测试先行（TDD）**：实现型 increment **内部**先写失败 UT（红）再写最小实现（绿）；每个 increment 有独立 UT 或已有 UT 通过 → 见 `tdd_discipline.md`
- **单 commit 回滚**：每个 increment 的 rollback = 单 commit `git revert`
- **token 预算**：CC Lead 单次调用不超过 15k output tokens

## Type-A ~ Type-F 拆分模式

| 模式 | 适用场景 | 示例 | 文件限制 |
|------|---------|------|----------|
| **Type-A**（文件新建） | 新增独立文件 | INCR-001-2-1: 新建 engine.go | 1 个新文件 |
| **Type-B**（字段添加） | 现有结构体增字段 | INCR-001-2-X: 给 Alert 加 RootCauseAlertIDs | ≤2 文件 |
| **Type-C**（方法实现） | 已有接口的实现 | INCR-001-2-2: 实现 Dedup 方法 | 1 个文件 |
| **Type-D**（逻辑替换） | 用 feature_flag 切换新旧逻辑 | INCR-001-2-Y: 用 flag 切换 Dedup 算法 | ≤3 文件 |
| **Type-E**（测试追加） | **仅**为既有未覆盖代码补 UT（新行为的测试走 Type-C 内部 red-green，不拆成 Type-E） | INCR-001-2-3: 为遗留 Dedup 补 UT | 1 个测试文件 |
| **Type-F**（配置变更） | 纯配置变更 | INCR-002-1-1: 更新 dedup.yaml | 1 个配置文件 |

**推荐拆分顺序**：Type-A（新建）→ Type-C（实现，**内部 red-green：先写失败 UT 再最小实现**）→ Type-D（集成）。Type-E 仅用于给既有遗留代码补测试，**不是**新行为的默认路径（新行为测试在 Type-C 内 test-first，见 `tdd_discipline.md`）。

## feature_flag 机制

**命名规范**：`SHRIMP_{requirement_id}_{feature_name}`

```
示例：SHRIMP_001_DEDUP_ENGINE
      SHRIMP_001_CRITICAL_FASTPATH
```

**代码实现模式**：
```go
// 1. 全局变量声明（package 级别，默认 false）
var dedupEngineEnabled = false

// 2. 功能入口检查（guard pattern）
func (e *Engine) Dedup(alerts []Alert) ([]Alert, error) {
  if !dedupEngineEnabled {
    return alerts, nil  // 安全降级：不收敛，原样返回
  }
  // ... 实际逻辑
}

// 3. 测试支持（测试文件可设置 true）
func TestDedupEnabled(t *testing.T) {
  old := dedupEngineEnabled
  dedupEngineEnabled = true
  defer func() { dedupEngineEnabled = old }()
  // ... test with flag on
}
```

**热回滚验证**：
```
验证热回滚成功：
1. 检查代码中 dedupEngineEnabled = false
2. 重新运行测试套件
3. 验证 alerts 原样返回（不收敛）
4. 无需代码回滚，无需重新部署
```

## increment 拆分检查清单

```
[ ] 每个 increment 改动文件 ≤ 3 个
[ ] 每个 increment 有 feature_flag（即使复用父级 flag）
[ ] 每个 increment 的 feature_flag 默认为 false
[ ] 每个 increment 有独立 UT 或已有 UT 通过
[ ] 每个 increment 的 rollback 策略 ≤ 1 行 git revert
[ ] increment 之间无循环依赖
[ ] 每个 increment 有对应文档更新（README/CHANGELOG/API Schema）
```
