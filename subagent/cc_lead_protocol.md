# CC Lead 调用规范（canonical · pm_agent + dev_agent 共用）

> 被 `pm_agent/SKILL.md` 与 `dev_agent/SKILL.md` 引用。
> 所有"读代码 / 写文档 / brainstorming / 实施 + 测试 + commit"的真实工作都通过本协议启动一个 CC Lead（Claude Code）session 完成；调用方 agent **绝不**直接落笔正文。

## 为什么不是别的方式

| 方式 | 状态 | 为什么不用 |
|------|------|-----------|
| `sessions_spawn(runtime="acp")` | ❌ 从未实现 | 仓库内 grep 0 命中；过去 pm_agent/dev_agent 因此每次直接返回 `status: done` 空跑（见 memory `shrimp-cc-lead-sessions-spawn-phantom`） |
| `delegate_task(acp_command="claude")` | ⚠️ 可用但禁用 | 走 MCP，模型由服务端决定，**无法保证本机 Opus**（见 memory `cc-lead-usage.md`） |
| **`cc-start.py` + `cc-send.py`**（tmux + `claude`） | ✅ **唯一正确方式** | 读 `~/.claude/settings.json` 默认模型（本机 Opus）；走 stdio，brainstorming/HARD GATE 可正常向用户提问；可加载 superpowers（brainstorming / systematic-debugging / test-driven-development 等） |

## 权限放行（已落地）

`orchestrator/scripts/graph.py` `_default_policies()` 已为 PM_PHASE / PLANNING_PHASE / DEV_PHASE 的 `permission_policy` 放行：
`Bash(cc-start.py*)` / `Bash(cc-send.py*)` / `Bash(cc-capture.py*)` / `Bash(jobs.py*)` / `Bash(tmux capture-pane*)` / `Bash(tmux list-sessions*)` —— 静默放行，不卡审批。

## 路径常量

```
CC=/root/.hermes/skills/openclaw-imports/cc-lead/scripts
# cc-start.py / cc-send.py / cc-capture.py / jobs.py 都在这里
```

## 五步驱动 CC Lead

```bash
CC=/root/.hermes/skills/openclaw-imports/cc-lead/scripts
WT="<workspace_dir 或代码仓库 worktree 路径>"
JOB="<repo>:<branch> <stage>"             # 如 mediax/medialive:clean PM_PHASE / DEV_PHASE
SESSION="<safe_id>-<role>"                # tmux session 名，全小写下划线

# 1) 确保 job 存在（已存在则跳过 create）
python3 "$CC/jobs.py" list 2>&1 | grep -F "$JOB" \
  || python3 "$CC/jobs.py" create --repo "<repo>" --branch "<branch>" \
       --title "<title>" --repo-local-path "$WT" --worktree "$WT"

# 2) 写 prompt 文件（注入任务上下文 + 输出契约 + 要加载的 skill）
#    用 Write 工具写到 /tmp/<safe_id>_<node>_prompt.md（见下方 prompt 模板）

# 3) 首次启动：cc-start.py（非阻塞：起 tmux + claude，发完 prompt 立即返回）
python3 "$CC/cc-start.py" --worktree-path "$WT" --job-id "$JOB" \
  --prompt-file "/tmp/<safe_id>_<node>_prompt.md" --tmux-session "$SESSION"
#    后续追加澄清：python3 "$CC/cc-send.py" --tmux-session "$SESSION" --prompt-file <follow_up.md>
#    dev INCR 复用同一 session：首个 INCR cc-start.py，后续 INCR 用 cc-send.py

# 4) 轮询监控直到完成约定行出现（每 15-30s capture 一次）
python3 "$CC/cc-capture.py" --tmux-session "$SESSION" 2>/dev/null | tail -30
#    - 出现 "requires approval" → policy 已放行，极少见；如出现发 "1 Enter"
#    - 完成信号约定（caller 不同 token 不同）：
#        PM_CC_LEAD_DONE: <产物路径>     ← pm-n0a / pm-n2 / plan-n*
#        CC_LEAD_INCR_DONE: <commit_hash> ← dev INCR

# 5) 校验产物 → 读回内容 → 组装本节点 stage-result/v1 返回 Orchestrator
```

> ⚠️ `cc-start.py` **非阻塞**：发完 prompt 即返回。"真正开始工作"的判据是 tmux session 已起 + prompt 已送达（返回 JSON `prompt_sent: true`）+ capture 能看到 claude 正在执行；**不要**在 cc-start.py 返回后立刻当成完成。

## prompt 模板（写给 CC Lead）

```markdown
## 角色：CC Lead（为 <调用方 agent> 产出 <节点产物>）
## 工作目录：{worktree_path}
## 加载 superpower：<brainstorming | systematic-debugging | test-driven-development | ...>

### 输入
- <任务上下文：PRD / SPEC / 已澄清需求 / 上游产物绝对路径>

### 任务
<节点的具体要求，如"产出 docs/tech_review.md，图文并茂，≥2 个 mermaid 块">

### 硬约束
- 不得在 PRD / SPEC 之外自主新增机制/接口；如需补充先停下问用户
- 图表强制 mermaid，禁止 ASCII art
- 产物写到 <目标文件绝对路径>

### 完成信号
产物写完后，在终端打印一行：`<DONE_TOKEN>: <产物路径或 hash>`
```

> CC Lead 打印约定 `DONE_TOKEN` 行可让调用方在 capture 时确定性判完成，避免空轮询。

## 调用方角色差异（速查）

| 调用方 | 工作目录 | DONE_TOKEN | 推荐 superpower |
|---|---|---|---|
| pm_agent / pm-n0a | workspace_dir | `PM_CC_LEAD_DONE:` | brainstorming |
| pm_agent / pm-n2 / plan-n1 | workspace_dir | `PM_CC_LEAD_DONE:` | systematic-debugging（plan-n1） |
| dev_agent / 实现型 INCR | feature 分支 worktree | `CC_LEAD_INCR_DONE:` | test-driven-development |
| dev_agent / fix INCR | feature 分支 worktree | `CC_LEAD_INCR_DONE:` | systematic-debugging（见 `dev_agent/references/diagnose.md`） |
