# Shrimp Git Workflow

> **版本**: v1.0  
> **生效日期**: 2026-04-05  
> **约束**: rebase + push = MR 创建/更新 + master 保护分支

---

## 1. 分支策略

### 1.1 分支命名

```
feature/shrimp-{requirement_id}
```

示例：
```
feature/shrimp-12345
feature/shrimp-INCR-001
```

### 1.2 分支角色

| 分支 | 角色 | 说明 |
|------|------|------|
| `master` | 保护分支 | 禁止直接 push，禁止 force push，禁止个人直接合并 |
| `feature/shrimp-*` | 开发分支 | 从 master checkout，开发完成后 rebase 合入 master |

### 1.3 分支生命周期

```
1. 从 master checkout 新分支
       │
       ▼
   [开发 + Commit]
       │
       ▼
   检查是否需要 rebase（master 有更新？）
       │
       ├── 需要 → git rebase master → 解决冲突
       │
       └── 不需要 → 直接下一步
       │
       ▼
   git push（自动创建/更新 MR）
       │
       ▼
   MR 创建 → Reviewer Agent 审查
       │
       ▼
   删除 feature 分支（MR 合入后）
```

### 1.4 Feature 分支删除时机

**合入 master 后立即删除**。合入即删除，不保留历史分支。

---

## 2. Commit 规范

### 2.1 格式

```
[INCR-XXX] feat(module): description
```

### 2.2 结构

```
[INCR-XXX] feat(module): description

body（可选），描述变更动机、变更内容、注意事项等。
每行不超过 72 字符。

INCR-ID: INCR-XXX
```

### 2.3 规则

| 维度 | 规则 |
|------|------|
| Subject 长度 | ≤ 72 字符 |
| Body 每行长度 | ≤ 72 字符 |
| Subject 与 Body 之间 | 必须空一行 |
| INCR-ID 引用 | 在 Body 中显式引用 |
| Breaking Change | 在 Footer 中以 `BREAKING CHANGE:` 声明 |

### 2.4 类型（type）

使用 Conventional Commits 风格：

| type | 用途 |
|------|------|
| `feat` | 新功能 |
| `fix` | 修复 Bug |
| `refactor` | 重构（不改变功能） |
| `test` | 测试相关 |
| `docs` | 文档相关 |
| `chore` | 构建/工具/依赖 |
| `perf` | 性能优化 |

### 2.5 示例

```
[INCR-001] feat(signal): add connection keepalive mechanism

Implement keepalive timer for WebRTC connection monitoring.
Rebase to latest master before merge.

INCR-ID: INCR-001
```

Breaking Change 示例：

```
[INCR-002] feat(api): rename GetSession to QuerySession

The GetSession API has been renamed to QuerySession for
consistency. Old clients must migrate to the new name.

INCR-ID: INCR-002
BREAKING CHANGE: GetSession removed, use QuerySession instead.
```

---

## 3. Commit 前检查

**禁止 commit 未通过检查的代码。**

每次 commit 前必须确认：

| 检查项 | 通过标准 |
|--------|----------|
| 编译 | `go build ./...` 无错误 |
| Lint | `golangci-lint run ./...` 无 error |
| UT | `go test ./...` 全部通过 |

### 3.1 快速验证命令

```bash
# 编译
go build ./...

# Lint（golangci-lint）
golangci-lint run ./...

# 单元测试
go test ./... -count=1
```

### 3.2 拦截机制

- CI 阶段会重复上述检查，未通过则阻止合入
- Reviewer Agent 会检查 commit 规范，不合规则要求整改

---

## 4. Rebase 合入流程

### 4.1 核心约束

**rebase + push = MR 创建/更新**。push 自动创建或更新 MR，无需任何额外合入动作。

### 4.2 流程

```
INCR 完成
    ↓
检查是否需要 rebase（master 有更新？）
    ↓
需要 → git rebase master → 解决冲突
不需要 → 直接下一步
    ↓
git push（自动创建/更新 MR）
    ↓
MR 创建 → Reviewer Agent 审查
```

### 4.3 每 INCR 完成后执行

```bash
# 1. 确保工作区干净
git status

# 2. 检查 master 是否有更新
git fetch origin master
git log HEAD..origin/master --oneline

# 3. 有更新则 rebase，无更新则跳过
git rebase origin/master

# 4. 如有冲突，解决后继续
git add .
git rebase --continue

# 5. 确认 rebase 成功，无冲突
git log --oneline -3

# 6. 推送（自动创建/更新 MR）
git push --force-with-lease origin feature/shrimp-12345
```

### 4.4 冲突处理原则

- **谁引入的冲突谁解决**：冲突由 rebase 产生，由 feature 分支开发者解决
- **禁止绕过**：不得使用 `--skip` 跳过冲突，必须显式解决
- **解决后验证**：冲突解决后重新运行编译和 lint，确保无引入新问题

### 4.5 合入前 CI 必须通过

CI 流程包含：
- 编译检查
- Lint 检查
- 单元测试

**CI 不通过，禁止 push。**

---

## 5. PR/MR 准入标准

合入 master 必须满足以下全部条件：

### 5.1 必须项（P0）

| 条件 | 说明 |
|------|------|
| CI 通过 | 所有 CI 检查必须 green，无 error |
| Reviewer Agent 通过 | Reviewer Agent v6.0 五轴审查全部通过 |
| 无 blocking 问题 | blocking 级别的问题必须全部修复 |
| Rebase 最新 master | 分支已 rebase 到最新 master，无落后 |

### 5.2 五轴审查（Reviewer Agent v6.0）

Reviewer Agent 从以下五个维度审查：

1. **正确性** — 逻辑正确，符合需求，无 Bug
2. **安全性** — 无安全漏洞，符合安全规范
3. **性能** — 无性能退化，符合性能要求
4. **可维护性** — 代码清晰，符合编码规范
5. **测试覆盖** — UT/集成测试充分，覆盖核心路径

### 5.3 Blocking 问题定义

以下问题级别为 **blocking**，必须修复后才能合入：

- 编译错误
- 单元测试失败
- Lint error
- 安全漏洞
- 内存泄漏
- 严重性能退化
- API 协议不兼容变更未声明

---

## 6. 快速检查清单

合入前逐项确认：

- [ ] 分支是从 master checkout 的
- [ ] 分支名符合 `feature/shrimp-{requirement_id}` 格式
- [ ] Commits 符合规范（`[INCR-XXX] type(module): desc`）
- [ ] Subject ≤ 72 字符，Body 每行 ≤ 72 字符
- [ ] INCR-ID 在 Body 中已引用
- [ ] Breaking Change 已声明（如有）
- [ ] 编译通过：`go build ./...`
- [ ] Lint 通过：`golangci-lint run ./...`
- [ ] UT 通过：`go test ./... -count=1`
- [ ] 已 rebase 到最新 master
- [ ] 已解决所有冲突
- [ ] CI green
- [ ] Reviewer Agent 五轴审查全部通过
- [ ] 无 blocking 级别问题
- [ ] MR 已创建且 Reviewer Agent 审查通过
- [ ] MR 合入后删除 feature 分支

---

## 7. 参考文档

- Dev Agent SKILL.md（v5.2.0）：`shrimp/subagent/dev_agent/SKILL.md`
- INCREMENTAL_IMPLEMENTATION_INTEGRATION.md
- Conventional Commits 规范
- Reviewer Agent v6.0 五轴审查标准
