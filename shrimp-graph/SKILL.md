---
name: shrimp-graph
description: 通过 LangGraph CLI 驱动 Hermes 与 shrimp graph orchestrator 的 PM + PLAN(含 PM-Dev 多轮协商)+ DEV(per-subTask 迭代)+ REV 四阶段闭环。INIT → PM(dispatch) → GATE_1 → PLAN(planner status=needs_input 时自动 dispatch dev_agent 进咨询模式来回最多 3 轮,出 subtask_plan)→ GATE_1_5 → DEV(每 subTask 各跑一次 dev_agent + codex_reviewer)→ GATE_2 → REV(rev_agent + codex_final 两次 dispatch)→ GATE_3 → DONE / BLOCKED。Gate 1 支持 approve / rollback_to_pm / reject;Gate 1.5 支持 approve / rollback_to_pm / continue_planning(带 user_hint_path 回流 PLANNING)/ reject;Gate 2 支持 approve / rollback_to_planning / rollback_to_pm / reject;Gate 3 支持 approve / rollback_to_dev(清空 subtask_results 重跑)/ rollback_to_planning / rollback_to_pm / reject。每 subTask 结束后 codex_reviewer 给 review 结果,失败不中断聚合到 Gate 2;REV 阶段 codex_final 给 Gate 3 信号;PM-Dev 协商记录走 sha-pinned ConsultationRecord,正文不进 state。包含 start / advance / status / resume / dump 五个命令,所有用户文本(含 continue_planning 的 user_hint)与 subagent 结果都必须以文件形式经 --input-file / --decision-file / decision-file.user_hint_path 传入,完整状态写入 result_file,stdout 只回小摘要。每阶段 dispatch_payload 自动挂 permission_policy(codex_reviewer / codex_final / 咨询模式 dev_agent 用 PLANNING_DEV_CONSULT 等只读策略)防止 cc-lead 卡在等审批;Anti-Drop Guard(G1/G3 严重 + G4/G5_DECL(轻微)/G5_PROVE(严重:文件不存在)/G6(轻微) + 一次带 hint 重试)在 dispatch 后强校验,二次仍 severe 才 BLOCKED + warnings;artifact 真落盘到 scripts/workflows/<wid>/artifacts/...,orch 自己 sha 反验后写入 state.artifacts(ground truth)。当用户说"启动 shrimp graph""推进 graph workflow""跑 PM/PLAN/DEV/REV dispatch""审批 gate""resume graph""subTask codex review""rev codex_final""rollback 到 PM/PLANNING/DEV""PM-Dev 协商""continue_planning 给方向"等场景使用。
---

# Shrimp Graph

shrimp graph orchestrator 的 PM + PLAN + DEV + REV 四阶段闭环 + HITL gate 操作 skill。**图本身不调任何 LLM**,所有 subagent 工作都靠 `subagent_dispatch` 中断把活儿外包出去,外部 runner 跑完写文件,再用 `resume --decision-file` 把结构化结果送回来。

```
INIT ─→ PM_PHASE ──(subagent_dispatch, pm_agent)──┐
                                                  ↓
                                       GATE_1 (gate_decision_needed)
                                       ├─ approve         → PLANNING_PHASE
                                       ├─ rollback_to_pm  → PM_PHASE  (retry++)
                                       └─ reject          → BLOCKED
PLANNING_PHASE(planner + 内部 PM-Dev 协商循环)
   │
   │   ┌── planner.status=done                                  → 出 subtask_plan,GATE_1_5
   │   │   planner.status=needs_input + dev_consultation
   │   │     + round < MAX                                      → planning_dev_consult,回 planner 再跑一轮
   │   │   planner.status=needs_input + (无 dev_consultation
   │   │     OR round >= MAX)                                   → GATE_1_5(让用户介入)
   │   │   planner.status=blocked                               → BLOCKED
   │   ▼   (Anti-Drop Guard 严重失败 → 带 hint 重试 1 次,仍失败才 BLOCKED;每个 dispatch 节点同款规则,详见下面 Anti-Drop Guard 段)
   │
   ↓
GATE_1_5 (gate_decision_needed)
   ├─ approve            → DEV_PHASE
   ├─ rollback_to_pm     → PM_PHASE   (retry++)
   ├─ continue_planning  → PLANNING_PHASE re-entry,user_planning_hints 追加,round 重置 0
   └─ reject             → BLOCKED
DEV_PHASE(per-subTask 迭代)
   │
   │   ┌─── 队列空(没 subtask 或全部跑完)→ 聚合 phase_summary + Gate 2 gate_result → 跳到 GATE_2
   │   │
   ▼   ▼
dev_loop_router ──┐
                  │ 队列有 subtask
                  ↓
            dev_subtask_dispatch (subagent_dispatch, dev_agent, 单个 subtask)
                  ↓
            dev_codex_review    (subagent_dispatch, codex_reviewer, 同一 subtask diff)
                  ↓
            (回 dev_loop_router 取下一个)
                  ⮌

GATE_2 (gate_decision_needed)
   ├─ approve              → REV_PHASE
   ├─ rollback_to_planning → PLANNING_PHASE  (retry++)
   ├─ rollback_to_pm       → PM_PHASE        (retry++)
   └─ reject               → BLOCKED

REV_PHASE(两段 dispatch,串行)
   │
   ▼
rev_dispatch        (subagent_dispatch, rev_agent,
                     输入:PM/PLAN/DEV phase_summary + subtask_results
                     产出:rev_report + acceptance_checklist
                     落盘:phase_summaries["REV_PHASE"] + gate_results["Gate 3 - rev_agent"](claim))
   ▼
rev_codex_final    (subagent_dispatch, codex_final,
                     review_mode=final_end_to_end,只读 CODEX_REVIEW policy
                     落盘:phase_summaries["REV_PHASE_CODEX"] + gate_results["Gate 3"])
   ▼
GATE_3 (gate_decision_needed)
   ├─ approve              → DONE
   ├─ rollback_to_dev      → DEV_PHASE       (retry++,subtask_results 清空,index 重置)
   ├─ rollback_to_planning → PLANNING_PHASE  (retry++)
   ├─ rollback_to_pm       → PM_PHASE        (retry++)
   └─ reject               → BLOCKED
```

> DEV 期间 `state.stage` 始终是 `DEV_PHASE`,不进新 stage 枚举值。内部三节点(loop_router / subtask_dispatch / codex_review)用 `subtasks` 队列状态做路由。
> **codex review 失败(`gate_result.passed=false`)不中断 DEV**,记录到 `state.subtask_results[task_id].review_passed` 即可,聚合后由 Gate 2 反映给你拍板。

## 输出契约(关键)

stdout 只回**小摘要**,完整 state 写入 `result_file` 落盘。这样设计的目的是:**让 LLM 即使想摘要也无东西可摘**。

```json
{
  "command": "start",
  "workflow_id": "wf-...",
  "result_file": "/root/.hermes/workspace/shrimp/orchestrator/scripts/results/<wf>/<cmd>-<ts>.json",
  "stage": "PM_PHASE",
  "next": ["pm_phase"],
  "user_msg_sha256s": ["..."],
  "decision_sha256s": [],
  "stage_log_entries": ["INIT"],
  "pending_interrupt": {
    "node": "pm_phase",
    "payload": {
      "type": "subagent_dispatch",
      "stage": "PM_PHASE",
      "subagent": "pm_agent",
      "skill_path": "shrimp/subagent/pm_agent/SKILL.md",
      "input_payload": { "...": "..." },
      "expected_output_schema": "stage-result/v1",
      "expected_artifacts": ["pm_spec", "pm_risks"]
    }
  }
}
```

`pending_interrupt.payload.type` 决定下一步该怎么 resume:

| `type`                  | 含义                              | resume 时 decision-file 应该是什么 |
|---|---|---|
| `subagent_dispatch`     | 让 runner 真去跑 subagent       | subagent 自己产的 stage-result/v1 结构化结果 |
| `gate_decision_needed`  | 让用户拍 gate                   | `{decision_id, answer, reason?}`,answer ∈ 该 gate 的 options |
| (无 / 为 null)          | 没卡住                          | 不该 resume,改用 advance 或视为已 DONE/BLOCKED |

## 工具路径

```
PY=/root/.hermes/workspace/shrimp/orchestrator/.venv/bin/python
CLI=/root/.hermes/workspace/shrimp/orchestrator/scripts/graph_cli.py
```

**state 持久化**:每次 `start` / `advance` / `status` / `resume` 都通过 LangGraph `SqliteSaver` 读写 `/root/.hermes/workspace/shrimp/orchestrator/scripts/checkpoints/state.db`,以 `workflow_id` 作 thread_id 索引(graph_cli.py line 39)。多个 workflow 共享同一 DB 文件,靠 `workflow_id` 隔离互不污染——这就是严格规则 4(不许改 workflow_id)的物理原因:它是 checkpoint 的 lookup key。要"重启"一个 workflow,直接 `start` 一个**新** workflow_id 即可,不需要清 DB(旧 workflow_id 的 checkpoint 行保留作历史,无副作用)。如果环境迁移真要全清,删 `state.db` 文件即可但会丢全部历史 workflow 的可恢复性。`result_file`(在 `scripts/results/<wf>/`)是另一份独立产物——`dump` 命令读它而不是 checkpoint DB,两者数据冗余但用途不同(checkpoint 是 LangGraph 内部 resume 用,result_file 是审计/呈现给用户用)。

## 五个命令

### 1. start —— 启动新 workflow

```bash
${PY} ${CLI} start [--workflow-id <id>] --input-file <raw_requirement.md> [--policies-file <path>]
```

- `--input-file` **功能上必传** raw requirement 文件,verbatim sha256 存入 `state.user_msgs[0]`。注意 argparse 不强制(graph_cli.py line 298 没 `required=True`)——省略时 `_load_user_msg` 返回空列表 `[]`,PM_PHASE dispatch payload 里 `raw_requirement_path / raw_requirement_sha256` 都是 null,pm_agent 拿到空上下文。**workflow 启动不会报错**,但 PM 阶段实质跑不动(已知 footgun:cli 不拦,要等 PM dispatch 才暴露)。**硬上限 256 KiB(`MAX_INPUT_FILE_BYTES`)+ 必须 UTF-8**;传了但违反任一上限/编码 → `SystemExit` 报到 stderr,workflow 不创建
- 省略 `--workflow-id` 时,脚本生成 `wf-YYYYMMDD-HHMMSS-<microseconds>` 格式(微秒后缀让"同秒并发 start"也能区分;详细 lookup-key 语义见下方"用户怎么验证保真"段的 `workflow_id` 行),**记下返回里的 `workflow_id`**
- 自动跑过 INIT,然后停在 PM_PHASE,`pending_interrupt.payload.type=="subagent_dispatch"`,subagent=`pm_agent`
- `--policies-file` 可选,用来覆盖每阶段 cc-lead(=runner 在 sessions_spawn 时启动的 Claude Code 子 agent runtime,subagent 实际跑代码的载体)权限策略默认值;不传就用 orch 内置的默认。详见下面 `permission_policy` 一节。**硬上限 64 KiB(`MAX_POLICIES_FILE_BYTES`)+ 必须 UTF-8 + 必须 JSON 对象**

### 2. advance —— 推进非中断节点

```bash
${PY} ${CLI} advance --workflow-id <wf>
```

- 如果当前停在 interrupt 上(无论是 subagent_dispatch 还是 gate_decision_needed),`advance` 是 idempotent no-op,`stage_log_entries` 不会变长
- 这条最小图的所有真实推进都靠 `resume` 触发,`advance` 只用来**确认当前是不是真的卡在某个 interrupt 上**

### 3. status —— 查当前状态(不推进)

```bash
${PY} ${CLI} status --workflow-id <wf>
```

- 不调任何节点,只读 checkpoint
- DONE / BLOCKED 后仍可调用,完整 state 仍在 `result_file`

### 4. resume —— 跨过中断

```bash
${PY} ${CLI} resume --workflow-id <wf> --decision-file <path>
```

decision-file 的内容要根据当前 `pending_interrupt.payload.type` 选格式。

> **decision-file 硬上限 256 KiB(`MAX_DECISION_FILE_BYTES`)+ 必须 UTF-8 + 必须 top-level 是 JSON 对象**;cli stat 预检,违反任一条 → `SystemExit` 报到 stderr,**checkpoint 不变**(workflow 仍卡在原中断,改完 decision-file 重发 resume 即可,不会损坏 state)。这条跟 `start` / `continue_planning` 的输入边界规则同源:把"用户传错文件"的失败模式拦在 cli 层,避免错误输入污染 sha-pinned state。

#### 4a. 当 `type=="subagent_dispatch"`(PM_PHASE / PLANNING_PHASE)

把整份 subagent 输出写成 stage-result/v1 形态:

```json
{
  "type": "subagent_result",
  "subagent": "pm_agent",
  "decision_id": "d_pm_run_001",
  "status": "done",
  "summary": "...",
  "phase_summary": {
    "decisions": ["..."],
    "open_issues": [],
    "risks": ["..."],
    "handoff_note": "一句话"
  },
  "artifact_updates": {
    "pm_spec":  {"sha256": "...", "byte_length": 0},
    "pm_risks": {"sha256": "...", "byte_length": 0}
  },
  "gate_result": {
    "name": "Gate 1",
    "passed": true,
    "reasons": []
  }
}
```

- 整份 JSON 文件按 sha256 钉死写入对应 `decisions[i].sha256`
- `phase_summary` 由 orchestrator 节点**整段原样**写进 `state.phase_summaries[<stage>]`,不会被 orchestrator 二次摘要——**例外:`state.phase_summaries["DEV_PHASE"]` 是 orch 合成的"synthetic-aggregate"**,因为 DEV 阶段有 N 个 dev_agent + N 个 codex_reviewer,没有单一 subagent 产出整段 phase_summary。orch 在 `dev_loop_router` 队列耗尽时合成,handoff_note 用 `DEV done (synthetic);` 前缀让下游识别;**真正的 verbatim subagent 声音留在 `state.subtask_results[sid].dev_handoff_note / .codex_handoff_note`**,REV / Hermes IM / Gate 2 想看具体某 subtask 的原文必须读 subtask_results,不要从合成 phase_summary 里反推。合成时严格规则:`decisions[]` 与 `handoff_note` 只用闭集字段(status / passed / 计数,无飘移空间);**`open_issues[]` 每条 review_reason 独占一行**,不许 `", ".join` 合并(reason 文本本身可能含逗号,合并就丢了边界——这是上游已经踩过的飘移坑)
- `gate_result` 是 subagent 自己的声明,**不代表用户已经批准**,真正的批准要在下一步 GATE_<n> 处由用户给
- PM_PHASE 的 dispatch payload(graph.py line 814-823):pm_agent 拿 `workflow_id` / `current_stage="PM_PHASE"` / `raw_requirement_path`(=`state.user_msgs[0].text_path`)/ `raw_requirement_sha256`(=`state.user_msgs[0].sha256`)/ `artifact_paths` / `retry_count` / `prev_phase_summary`(PM_PHASE,即同 stage 上一次 retry/rollback 的 handoff,首跑为 null)+ `output_schema="stage-result/v1"`,产出 `expected_artifacts=["pm_spec","pm_risks"]`。**没**挂任何 `*_phase_summary` 上游字段(因为 PM 是首阶段),也**没** `input_artifacts`(因为 raw_requirement 通过 path+sha 直接给,跟 path-not-content 设计一致)
- **planner 复用 pm_agent 的 SKILL.md 契约**:graph.py line 960 dispatch_payload 的 `skill_path` 是 `shrimp/subagent/pm_agent/SKILL.md` 而**不是**单独的 planner SKILL.md(跟 codex_final 复用 codex_reviewer SKILL.md 一样的"角色复用"模式)。runner 实现 planner 应当读 pm_agent 的契约文件加上本 SKILL.md 里 PLANNING_PHASE 章节的额外要求(必挂 `subtask_plan` 字段、可选 `dev_consultation`、可选 `_decision_file_*` 由 cli 自动注入)。`subagent` 字段在 dispatch_payload 里写 `"planner"`(逻辑名字),但**实际 skill 文件是 pm_agent 的** —— runner 区分两者用 `subagent` 字段而非 `skill_path`,这是这套系统的角色复用约定
- PLANNING_PHASE 的 dispatch payload 里同时挂了 `pm_phase_summary`(PM 阶段已经定稿的 handoff)和 `input_artifacts: ["pm_spec","pm_risks"]`,planner subagent 应该读这两份 artifact 而不是回头重摘 raw requirement。**完整字段集**(graph.py line 961-975):`workflow_id` / `current_stage="PLANNING_PHASE"` / `artifact_paths` / `retry_count` / `prev_phase_summary`(PLANNING_PHASE,同 stage 上一次)/ `pm_phase_summary` / `input_artifacts=["pm_spec","pm_risks"]` / `planning_consultation_round`(当前已完成几轮)/ `planning_consultations`(全历史 ConsultationRecord 列表,只 sha 指针无正文)/ `max_planning_consultations`(=3,跟 planner 协商 budget 协商)/ `latest_user_planning_hint`(最后一条 GATE_1.5 continue_planning 的 MsgRef,首跑为 null)/ `all_user_planning_hints`(所有历史 hint MsgRef 列表) + `output_schema`,产出 `expected_artifacts=["plan","task_list"]`。planner 用这些字段决定:首跑/重做时怎么拆 subtask、协商达上限时收手、收到用户 hint 时换方向
- PLANNING_DEV_CONSULT 子节点的 dispatch payload(graph.py line 1131-1144):`subagent="dev_agent"`(不是 planner),挂 `consultation_mode=true`(标志位)/ `round_index`(下一轮序号)/ `max_rounds=3` / `artifact_paths` / `input_artifacts=["pm_spec","pm_risks","plan","task_list"]`(读 PM + PLAN 的全套 artifact 才能上下文回答)/ `planner_question_ref`(`{path, sha256, field, stage_result_decision_id}`,**只给 path+sha,不给 question 正文**——dev_agent 自行按 path 读文件 + sha 校验)/ `prior_consultations`(全历史)/ `pm_phase_summary` / `planning_phase_summary` / `all_user_planning_hints` + `output_schema`,**`expected_artifacts=[]`**(咨询模式纯只读不产物);policy 走 `PLANNING_DEV_CONSULT` 复用 key 而不是 `DEV_PHASE`
- **PLANNING_PHASE 的 stage-result 必须挂一段 `subtask_plan: [{task_id, title, description}, ...]`**——orch 把它存进 `state.subtasks`,DEV 阶段按这个数组逐个迭代。**没挂 = 数组为空 = DEV 直接跳到 GATE_2**(优雅退化,不报错)
- DEV_PHASE 每个 subTask 的 dev_agent dispatch payload 里挂当前 `current_subtask`(单个 SubtaskSpec,不是整个数组)+ `subtask_index/subtask_total` + `completed_subtask_results`(前面已经做完的 subtask 结果摘要),subagent 知道自己在迭代第几个、上下文是什么。**完整 input_payload 字段集**(graph.py line 1527-1542):`workflow_id` / `current_stage="DEV_PHASE"` / `artifact_paths` / `retry_count` / `current_subtask` / `subtask_index` / `subtask_total` / `completed_subtask_results` / `pm_phase_summary` / `planning_phase_summary` / `input_artifacts=["pm_spec","plan","task_list"]`(注意**没挂 `pm_risks`**,跟 planner / planning_dev_consult 不同 —— 实现选择是 dev 阶段聚焦 spec + plan,risks 留给 PM/PLAN/REV 反思)+ `output_schema="stage-result/v1"`。codex_reviewer 同款 per-subTask 但 input_artifacts 是 `["dev_summary","dev_self_check","dev_changed_files"]`(line 1638),并额外挂 `review_target_subtask` / `dev_handoff_note` / `dev_decision_sha256` 让 codex 拿到刚才 dev 的 verbatim 与 sha 校验
- **每个 subTask 跑完 dev_agent 后,orch 自动 dispatch `codex_reviewer`** 对同一 subTask 做 review,subagent path 是 `shrimp/subagent/codex_reviewer/SKILL.md`(skill 文件可能还不存在,这是契约;runner 自己实现这个角色)。codex 的 permission_policy 是 `CODEX_REVIEW` 策略——**纯只读,Read/Glob/Grep allow,所有 Edit/Write/Bash 一律 deny**
- **REV_PHASE 是两次串行 dispatch**:`rev_agent`(subagent path `shrimp/subagent/rev_agent/SKILL.md`,REV_PHASE policy,可写 `artifacts/rev/**`)产 rev_report + acceptance_checklist;`codex_final`(复用 `shrimp/subagent/codex_reviewer/SKILL.md`,input_payload 里 `review_mode="final_end_to_end"`,policy 复用 `CODEX_REVIEW`)做端到端只读 review,**它的 gate_result.passed 才是 Gate 3 信号**;rev_agent 自己的 gate_result(若有)只作 claim 存到 `gate_results["Gate 3 - rev_agent"]`,不作判定输入。**完整 input_payload 字段集**(graph.py line 1801-1828 / 1901-1933):**rev_agent** 拿 `workflow_id` / `current_stage="REV_PHASE"` / `artifact_paths` / `retry_count` / `prev_phase_summary`(REV_PHASE,即同 stage 上一次 retry 的 handoff 给 rev_agent 自校) / `pm_phase_summary` / `planning_phase_summary` / `dev_phase_summary`(全 3 个上游阶段 summary)/ `subtask_results`(整 dict,DEV per-subtask verbatim 历史)/ `input_artifacts=["pm_spec","pm_risks","plan","task_list","dev_summary","dev_self_check","dev_changed_files"]`(7 个 key 全读,**pm_risks 也读**——区别于 dev_agent 不读 pm_risks)+ `output_schema`,产出 `expected_artifacts=["rev_report","acceptance_checklist"]`;**codex_final** 拿同样 `workflow_id` / `current_stage` / `artifact_paths` / `retry_count` 但加 `review_mode="final_end_to_end"`,phase summaries 增加 `rev_phase_summary` 共 4 个上游(`pm/planning/dev/rev`),加 `rev_agent_gate_claim`(`state.gate_results["Gate 3 - rev_agent"]` dump 给它"看 rev_agent 自己怎么说"以便对照)+ `subtask_results` + `input_artifacts=["pm_spec","plan","dev_summary","dev_changed_files","rev_report","acceptance_checklist"]`(6 个 key,**不读 pm_risks / task_list / dev_self_check / planning_discussion**——只读"最终交付相关"的 artifact),`expected_artifacts=[]`(只读)
- 每个 `subagent_dispatch` payload 都挂了 `permission_policy`,**这是给 runner 的指令**——让 runner 在 sessions_spawn cc-lead 之前先把 settings.json 渲染好,避免 cc-lead 跑到一半弹审批把 workflow 钉死。详见下面 `permission_policy` 专门一节

### permission_policy — 让 cc-lead 不会卡在等审批

每个 `type=="subagent_dispatch"` 的 payload 都会带:

```json
{
  "permission_policy": {
    "allow_silent": ["Read(**)", "Glob(**)", "Grep(**)", "Write(.../artifacts/pm/**)", ...],
    "deny": ["Bash(**)", "Write(src/**)", "Edit(src/**)", "Write(/etc/**)", ...],
    "ask_user": [],
    "default_action": "deny"
  }
}
```

**这条契约的目的是消除一类失败模式:你启动了 workflow 后,cc-lead 跑到一半弹审批却没人去批,workflow 被卡死。**

#### Runner 应该怎么用它

收到 dispatch 后、`sessions_spawn` 之前:

1. 把 `permission_policy` 的四个字段渲染成 cc-lead(或对应 subagent)的 `settings.json` permissions 段
2. 渲染完再 spawn——让权限**先于** subagent 启动到位
3. 不要把 ask_user 翻译成"弹给用户"——orch 的内置默认与启动期校验都保证 `ask_user=[]`,如果 runner 看到非空,应当当作 orch bug 报回来,**不要**真去 prompt 用户
4. 渲染失败 / 不支持某个模式时,选**保守**侧:无法表达的 allow 项 → 当 deny 处理;无法表达的 deny 项 → 当 deny 处理(deny 必须保住)

#### 字段语义

| 字段 | 含义 |
|---|---|
| `allow_silent` | 静默放行的工具调用模式列表 |
| `deny` | 静默拒绝的模式列表 |
| `ask_user` | **应当永远为空**——任何要求人工介入的操作都该改归到 deny 或 allow_silent。orch 在 `start` 阶段会硬拒非空的 ask_user |
| `default_action` | 既不在 allow 也不在 deny 的工具调用怎么办,取值 `"allow_silent"` 或 `"deny"`。**禁止 `"ask_user"`**,start 期硬拒 |

#### 各阶段的内置默认

orch 内置 PM_PHASE / PLANNING_PHASE / DEV_PHASE 三套默认策略,在 `init_node` 时种入 `state.permission_policies`(只在没人通过 `--policies-file` 显式传时才种)。基本风格:

| 阶段 | 风格 | default_action |
|---|---|---|
| PM_PHASE | 紧:只读 + 只能写 `artifacts/pm/`,任何 Bash 和源码改动一律拒 | `deny` |
| PLANNING_PHASE | 紧:只读 + 只能写 `artifacts/plan/`,Bash 和源码改动一律拒 | `deny` |
| DEV_PHASE | 松:能写 src / tests / artifacts/dev,能跑常见 test/build/git commit;但 `git push --force` / push 到 main / sudo / pip install / curl 都进 deny | `allow_silent` |
| REV_PHASE | 紧:只读 + 只能写 `artifacts/rev/`,允许 `git diff` / `git log` / `git status` 用来翻全工作 diff,源码改动 / 任何 push / 安装命令一律拒 | `deny` |
| CODEX_REVIEW(给 codex_reviewer 和 codex_final 共用) | 极紧:Read/Glob/Grep 放,所有 Edit/Write/Bash 全 deny | `deny` |

#### 什么时候用 `--policies-file`

- 默认策略不够用(比如你的项目要跑特定 build 命令)
- 想要更紧(比如审计场景下 DEV 也只允许特定命令)
- 想要给 REV_PHASE 或新阶段添加策略

文件格式是 `{stage_name: PolicyConfig, ...}` 的 JSON,例:

```json
{
  "PM_PHASE": {
    "allow_silent": ["Read(**)", "Glob(**)"],
    "deny": ["Bash(**)"],
    "ask_user": [],
    "default_action": "deny"
  }
}
```

orch 在 `start` 时会校验:
- ❌ 任意阶段的 `ask_user` 非空 → 启动失败
- ❌ 任意阶段 `default_action == "ask_user"` → 启动失败
- ✅ 校验通过,policies 写入 `state.permission_policies`,`init_node` 不再覆盖

**⚠️ partial-override footgun**:`--policies-file` 是**整体替换**(graph.py `init_node` 检测 `state.permission_policies` 非空就**整段跳过**默认种入,line 783-786),不是逐 stage 合并 —— 如果你只在 file 里写了 `PM_PHASE` 一个阶段的策略,其他 5 个 stage(`PLANNING_PHASE` / `DEV_PHASE` / `REV_PHASE` / `CODEX_REVIEW` / `PLANNING_DEV_CONSULT`)就**没有任何策略**进 state,后续 dispatch_payload 的 `permission_policy` 字段是 `null`,runner 拿 null 时按各自实现 fallback(可能放开权限,丢失 cc-lead 不卡审批的保证)。**安全做法**:`--policies-file` 要么干脆不传(全用 orch 内置默认),要么**完整覆盖**(把上述 6 个 stage key 全列出,每条都带完整 `allow_silent / deny / ask_user / default_action` 字段),不要"只改一个阶段"。

校验失败的报错会在 stderr 里清楚说明哪一阶段哪一项违规、改成什么。

### Hermes 必须给用户同步 cc-lead 状态

这套 workflow 把"用户审批"和"用户感知"做了刻意切分:**审批**只在 gate(approve / rollback / reject)上发生,跑期间用户不被打扰;但用户**仍需感知** cc-lead 在做什么,否则 dispatch 出去就是黑盒,体验很差。

Hermes 在执行本 skill 的任何 `subagent_dispatch` 时,**必须**给用户发以下三类同步消息(默认走 Hermes 自带的飞书 IM 能力,跟当前对话的同一个用户):

#### 必须发(硬要求)

每个 `subagent_dispatch` 进出都触发一对消息——这意味着 DEV 阶段的每个 subTask 都会触发**两对**(dev_agent 一对,codex_reviewer 一对),不是整个 DEV 阶段一对。subTask 失败必须**立刻**通知,不能等到 Gate 2 聚合。

| 时机 | 模板 | 备注 |
|---|---|---|
| `sessions_spawn` 之前 | `🚀 <stage>[ subtask=<task_id>] 开工:dispatch <subagent>,预期产物 <expected_artifacts>` | subtask=... 段只在 DEV 内部 dispatch 时加(per-subTask 才有) |
| `sessions_spawn` 返回 **且 status="done" 且 (没 gate_result 或 gate_result.passed=true)** | `✅ <stage>[ subtask=<task_id>] 完成:handoff_note=<phase_summary.handoff_note 原文,逐字>` | 成功路径,正常 ✅ |
| `sessions_spawn` 返回 **且 status!="done"** 或 **gate_result.passed=false** | `⚠️ <stage>[ subtask=<task_id>] 失败:status=<status>, passed=<gate_result.passed>, reasons=<gate_result.reasons 数组,逐字>, handoff_note=<phase_summary.handoff_note 原文,逐字>` | **任何失败必须用 ⚠️,不能套 ✅**——尤其是 codex_reviewer 报 passed=false / dev_agent 报 status=blocked,**用户必须当场感知,不能等到 Gate 2** |
| cc-lead 在 say 里报告"被 policy deny / 我做不到 X" | `⚠️ <stage>[ subtask=<task_id>] 卡点:cc-lead 报告 <原文>` | 在 dispatch 期间(还没 return)就要发 |

**`status="needs_input"` 的特殊情形(planner 触发 PM-Dev 协商,不是失败)**:planner 报 `status="needs_input"` **且** `dev_consultation` 字段非空时,这是**正常进入下一轮 PM-Dev 协商**(PM-Dev 协商章节的"Hermes-side IM 同步"段明示"N 轮协商是正常事件"),**不要走上表 ⚠️ 失败模板**——应当用 ✅ 变体:`✅ <stage> 进入下一轮 PM-Dev 协商:planner 报 needs_input,passed=<gate_result.passed>,reasons=<gate_result.reasons 数组,逐字>,handoff_note=<phase_summary.handoff_note 原文,逐字>,question_sha=<dev_consultation 字段计算的 sha 前 12 字符>,orch 即将 dispatch dev_agent (consultation_mode=true) 答它`(handoff_note + reasons 必须按 "handoff_note / reasons 必须逐字引用"段(IM 模板表后面那段全局规则)**逐字**贴出,跟标准 ✅/⚠️ 模板字段集合对齐;question_sha 是新增的协商专用字段,用作审计指针)。Hermes 误用 ⚠️ 会让用户看到 `⚠️ PLANNING_PHASE 失败` 误判 planner 出错而 abort/rollback,错失正常协商流程——这是 IM 通道上的输入飘移路径,N 轮协商场景下混淆 N 倍。**例外的例外**:planner 报 `status="needs_input"` **但** `dev_consultation` 字段缺失或为空(意味着达 `MAX_PLANNING_CONSULTATIONS` 上限或 planner 不愿继续咨询),此时 stage 会被 orch 推到 GATE_1_5 让用户在选项矩阵里拍板——这种情形仍走上表 ⚠️ 模板(因为 planner 确实没产 subtask_plan),但 ⚠️ 消息后建议追加一句"(planner 协商已尽,等 GATE_1_5 决策)"避免被读成"严重错误"。

**给"任何失败必须 ⚠️ 不能 ✅"这条加一条强调**:codex_reviewer 是这条规则的主要受益方。3 个 subtask 跑下来,如果 T-002 review_passed=false,Hermes 必须在 T-002 codex 这次 dispatch 返回**当场**发 ⚠️,**不能**直到所有 subtask 跑完到 Gate 2 才让用户看见——那时候用户已经等了一阵了,且 Gate 2 那条 IM 是给所有 subtask 一起的总结,失败信号会被淹没。

发不出去(IM 服务挂了 / 用户离线 / 其他)**不影响 dispatch 主流程**——subagent 跑完照样组 stage-result 调 resume,失败信息归到 phase_summary.open_issues 即可。

**handoff_note / reasons 必须逐字引用,禁止 Hermes 改写**:G6 已强制 handoff_note 单行 ≤280 字符(超出/换行 → minor warning),保证 IM 能直接放原文。Hermes **不许**自己"挑一句""压缩""润色",哪怕 G6 检出违反契约,也应当**逐字贴出 raw handoff_note**(可在前面加 `(G6 minor: handoff_note 超长/多行,以下为原文)` 提示),让用户感知到的字段就是 state 里落盘的字段。`reasons` 数组同理,JSON-stringify 整段贴出,不要选择性引用。这条是"防中间 LLM 偷改输入"在 IM 通道的具体落点。

**IM 长度上限的合规应对(reasons 数组 / G6 minor 超长 handoff_note)**:整条 IM 总长可能超出 IM 渠道 cap(飞书富文本约 30 KB / 文本约 4 KB 量级,实际看你的渠道实现)——典型触发场景:N 个 subtask × 多个 codex issues × 长引用、rev_agent 长 report、G6 violation 多行 handoff。**仍然禁止压缩、截断、摘要、选择性引用**(违反 "handoff_note / reasons 必须逐字引用"段) → 合规解法只有两条:**(a) 拆多条有序消息** `part 1/N` / `part 2/N` / ...,每条**自身完整**(JSON-stringify 不切断在数组元素中间;字段切分应在 array boundary),user 顺序拼接后**逐字等于**原数组,首条加前缀 `(IM 因长度上限拆成 N 条,以下 part 1/N)`;**(b) 单条字段本身就超 cap** → 发一条**短引导 IM**:"⚠️ <stage>... handoff_note / reasons 超 IM 上限,请跑 `dump --result-file <path>` 看 verbatim,**Hermes 不会自摘**",用户得到的 IM 内容是引导而非"内容片段",避免任何"半摘要"中间形态。两条都保住"用户感知 = state 落盘"原则,断绝中间 LLM 借"IM 太长"理由偷改输入的口子。Hermes 选 (a) 还是 (b) 取决于内容是否能在 array boundary 切开;不能切的(单条 reason 超 cap)只能选 (b)。

#### 尽力发(best-effort)

| 时机 | 内容 | 备注 |
|---|---|---|
| dispatch 进行中,每 ~5 分钟 | 当前 cc-lead 在做什么的一句话摘要 | 取决于你的 sessions_spawn 是否支持边跑边读流。阻塞 spawn 没法做,跳过 |
| cc-lead 关键里程碑(切到下一个 increment / 新建分支 / 跑完测试) | 一句话摘要 | 同上,看运行时支持 |

**禁止**逐字 mirror cc-lead 的每句 say:刷屏,且违背"防输入飘移"——逐字流是给"主动看的人"的,不是给"想被动感知的人"的,本 skill 服务后者。

#### 跟"审批"的边界

这套同步**只是单向通知**,**永远不要**问用户"要不要 approve"——审批只在 gate 上由 orch 通过 `gate_decision_needed` interrupt 发起。如果 cc-lead 在 dispatch 期间说"我需要批准 X",正确动作是:

1. 把它当 blocker 走"⚠️ 卡点"通道告诉用户(单向通知)
2. cc-lead 那边按 policy 给的 default_action 处理(deny 兜底)
3. 不要把 cc-lead 的请求转化成 IM 弹窗等用户回——那就把 babysit 又拉回来了

如果用户看到卡点不爽、想重跑,正确做法是**让 workflow 跑完到 BLOCKED / Gate**,然后用 `--policies-file` 重起一份新 workflow。

#### 4b. 当 `type=="gate_decision_needed"`(GATE_1 / GATE_1_5 / GATE_2)

```json
{
  "decision_id": "d_g1_001",
  "answer": "approve",
  "reason": "可选,自由备注"
}
```

`answer` 必须是 payload 里 `options` 中的一个。四个 gate 选项不完全一样:

| answer | 在 GATE_1 | 在 GATE_1_5 | 在 GATE_2 | 在 GATE_3 |
|---|---|---|---|---|
| `approve` | → PLANNING_PHASE | → DEV_PHASE | → REV_PHASE | → DONE |
| `rollback_to_pm` | → PM_PHASE,`rollback_counts["PM_PHASE"]++` | → PM_PHASE,`rollback_counts["PM_PHASE"]++` | → PM_PHASE,`rollback_counts["PM_PHASE"]++` | → PM_PHASE,`rollback_counts["PM_PHASE"]++` |
| `rollback_to_planning` | (无)| (无)| → PLANNING_PHASE,`rollback_counts["PLANNING_PHASE"]++` | → PLANNING_PHASE,`rollback_counts["PLANNING_PHASE"]++` |
| `rollback_to_dev` | (无)| (无)| (无)| → DEV_PHASE,`rollback_counts["DEV_PHASE"]++`,**`subtask_results = {}` 且 `current_subtask_index = -1`**(从零重跑) |
| `continue_planning` | (无)| → PLANNING_PHASE re-entry,**decision-file 必须带 `user_hint_path`**;orch 先 stat 文件大小、再读、再算 sha,append 到 `user_planning_hints`,`planning_consultation_round` 重置 0 给一份新协商额度。**hint 文件硬上限 64 KiB(`MAX_USER_HINT_BYTES = 64 * 1024`)+ 必须 UTF-8 文本**:超过 / 不可 stat / 不可读 / 非 UTF-8 都直接 BLOCKED 并写一条 severe warning(check_id 见下) | (无)| (无)|
| `reject` | → BLOCKED,note=`gate-N-rejected` | → BLOCKED | → BLOCKED | → BLOCKED |
| **未列出的 / 拼错 / 字段为空 / 跨 gate 答错** | → BLOCKED + `severity=severe` warning `GATE_INVALID_ANSWER`,note=`gate-N-invalid-answer:<原 answer>`(显式区别于 `reject`) | 同左 | 同左 | 同左 |

> 注:**Hermes 在 `pending_interrupt.payload.type == "gate_decision_needed"` 节点上必须把 payload.options 数组逐字呈现给用户**——每个选项独立一行,字面量保留 `approve` / `rollback_to_pm` / `rollback_to_planning` / `rollback_to_dev` / `continue_planning` / `reject` 的 snake_case 原形,后接该选项对应的下游动作(从上面的选项矩阵抽取一句话)。**禁止**压缩 / 挑选 / 翻译 / 改写选项字面量,也不许"看似没用就跳过某条"——比如 PLANNING 协商达上限场景里 `approve` 仍然合法存在,Hermes 不能擅自隐藏。这条堵的是"用户没看见 `continue_planning` 错选了 `approve`"或"用户以为自己 reject 但 Hermes 写了 rollback"这类**事前**输入飘移路径;跟下面 `GATE_INVALID_ANSWER` 那条**事后**兜底规则同源——双层防线,前者预防、后者告警。
> 注:**gate_decision_needed payload 还含其他用户决策必读字段,Hermes 必须一并呈现**(graph.py line 869-883 / 1208+ / 1717+ / 1997+):(a) **`question_text`**——orch 给该 gate 的提问字符串(中文),逐字 quote 作 IM 主体;(b) **`subagent_gate_claim`**——上游 subagent 自己的 gate 声明(`{name, passed, reasons}` 或 null,从 `state.gate_results[gate_name]` 取),用户必须看到 subagent 的"自我陈述"才能判断该不该 approve,逐字 quote 整个 dict;(c) **`phase_summary`**——上游阶段的完整 PhaseSummary(`decisions / open_issues / risks / handoff_note`),逐字 quote(尤其 `open_issues` 多条要每条独占一行,跟 DEV synthetic-aggregate 段的"不许 join"规则一致);(d) **`context_user_msg_sha256s`**——所有 user_msgs 的 sha 列表,Hermes 用作"提醒用户原始输入是哪些"。这四个字段加上 options 是用户在 gate 上做出 informed decision 的最小信息集——只 relay options 不 relay 这些等于让用户在不知道上游说了什么的情况下盲选,违反 Priority 2(决策权)。逐字保留规则跟 "handoff_note / reasons 必须逐字引用"段(IM 模板表后面那段全局规则)同源。
> 注:**各 gate 还有 gate-specific 额外字段,也必须一并 relay**(每个 gate 的 payload 不一样,Hermes 应根据 `payload.gate` 字段值选 relay 哪些):(i) **GATE_1**:无额外字段,仅含通用四 + options;(ii) **GATE_1_5**:加 `pm_phase_summary`(让用户看到 PM 上游不只是 planner 的 self-claim) + `planning_consultation_round` / `max_planning_consultations`(用户知道协商额度还剩多少 → 决定 continue_planning 还是 rollback) + `planning_consultations`(协商 audit trail,只含 sha 指针);(iii) **GATE_2**:加 `pm_phase_summary` + `planning_phase_summary`(让用户看到 PM 与 PLAN 两层上游,对比 DEV 是否符合预期 → 决定 rollback_to_planning 还是 rollback_to_pm);(iv) **GATE_3**:最丰富,加 `rev_agent_gate_claim`(rev_agent 的独立 self-claim,**跟 subagent_gate_claim 区分** —— 后者是 codex_final 的真 Gate 3 信号,前者只作溯源)+ `codex_phase_summary`(`state.phase_summaries["REV_PHASE_CODEX"]` 的 dump)+ `pm_phase_summary` / `planning_phase_summary` / `dev_phase_summary`(全 5 阶段总览 → 决定三向 rollback 选哪条)。**漏 relay 这些会让用户在三向 rollback 决策时失明** —— 例如 GATE_3 不展示 dev_phase_summary,用户没法判断 rollback_to_dev 是否合理;GATE_1.5 不展示 planning_consultation_round,用户不知道还能不能 continue_planning。逐字保留规则同上("handoff_note / reasons 必须逐字引用"段)。
> 注:任何 gate 都**不**提供"重跑当前阶段"这种**同阶段**的内部 retry 选项;PM-Dev 多轮协商等内部循环放到后续刀再做。
> 注:**`reject` 与"未识别 answer"被刻意分开**——前者是用户显式选择,后者是输入飘移(typo / 跨 gate 答错 / 字段缺失)。两者最终都到 BLOCKED,但 `state.warnings` 只在第二种情形追加 `check_id="GATE_INVALID_ANSWER"` 的 severe 条目,且 `stage_log` 末位 note 用 `gate-N-invalid-answer:<原 answer>`(而非 `gate-N-rejected`)。**Hermes 看到 `GATE_INVALID_ANSWER` 必须当场 ⚠️**(跟 guard 严重失败那条类似),提示用户重看 decision-file——这条堵的是"用户以为自己 approve 了,实际 workflow 因 typo 静默 BLOCKED"的失败模式。该 check_id 不属于 G1–G6,不参与 anti-drop guard 重试;一次 invalid 直接 BLOCKED,纠正方式是用户改 decision-file 重跑(目前只能新起 workflow,因为 BLOCKED 后没有 in-place 修正路径)。
> 注:Gate 2 是双向 rollback;Gate 3 是三向 rollback——REV 阶段发现的问题可能源于 dev 实现、plan 拆分或上游 spec,由你按 codex_final / rev_report 给的依据拍板。
> 注:**所有 rollback 类 answer 都同时累加两个计数器**——上方选项矩阵每行只显式列了 `state.rollback_counts[<stage>]++`,但 `state.retry_count` **也同步 +1**("跨阶段回滚" SOP 例子 + 验证表的 retry_count/rollback_counts 行 + 对齐进度段的"跨阶段回滚边"行三处一致确认)。两者语义不同:`retry_count` 是**全局回滚总数**(数学上等于 `sum(rollback_counts.values())`),`rollback_counts[<stage>]` 是**该 stage 被回滚到的次数**。Hermes 在告诉用户某次 rollback 的状态变更时,应同时点出两个计数器的新值,避免用户验证 `state.retry_count` 看到 +1 时误以为是飘移。
> 注:Gate 3 的 `rollback_to_dev` 会**清空** `subtask_results` 重跑全部 subtask,旧的 dev/codex 结果丢失(只在历史 result_file 里留痕)。这是为了避免 dev_loop_router 误把上一轮残留当作"已跑完"。其他几条 rollback 边只 retry++ + rollback_count++,不动 phase_summaries(下一轮 dispatch 时通过 prev_phase_summary 让 subagent 看到上一轮自己的 handoff)。**重要语义:rollback_to_dev = 全清重跑,不是"带反馈重跑"**——新一轮 dev_agent dispatch payload 里**不包含**上一轮 codex 反馈的逐字内容:per-subtask verbatim 来源 `subtask_results[sid].codex_handoff_note` 已随清空消失;phase_summaries["DEV_PHASE"] 即便残留也只是 synthetic aggregate 的压缩状态摘要,不是逐字反馈。要让新一轮 dev 看见 codex 发现的**具体**问题以针对性改进,正确路径是先 `rollback_to_planning` 让 planner 把 codex 暴露的问题落进新 subtask_plan,再跑 DEV——不要依赖 rollback_to_dev "带着 codex 反馈重跑",它做不到。Hermes 在 Gate 3 给用户呈现 `rollback_to_dev` 选项时,应在动作摘要里点出这个"全清重跑"语义,避免用户误以为新一轮 dev 自动会知道上次哪里不对。
> 注:Gate 1.5 的 `continue_planning` **不**走 retry_count / rollback_counts 计数(它不是 rollback,是阶段内部协商的延续);它只追加 `user_planning_hints` + 重置 `planning_consultation_round`。`continue_planning` 的 decision-file 缺 `user_hint_path` 字段时 orch 会把 stage 推到 BLOCKED 并写一条 `severity=severe` 的 warning(`GATE_1_5_CONTINUE_NO_HINT`),不会无 hint 空跑一轮。**hint 文件 stat/read 失败、超 64 KiB、或非 UTF-8** 各自走独立 severe warning + 独立 stage_log note,Hermes 必须当场 ⚠️ 让用户知道是哪种边界踩上了:

| check_id | 触发 | stage_log note |
|---|---|---|
| `GATE_1_5_CONTINUE_NO_HINT` | decision-file 没 `user_hint_path` 字段 | `gate-1-5-continue-rejected:no-user-hint-path` |
| `GATE_1_5_CONTINUE_HINT_UNREADABLE` | stat 或 read 抛 OSError(路径不存在 / 没权限 / 是目录 / 等) | `gate-1-5-continue-rejected:unreadable-hint` |
| `GATE_1_5_CONTINUE_HINT_TOO_LARGE` | 文件 > 64 KiB | `gate-1-5-continue-rejected:hint-too-large` |
| `GATE_1_5_CONTINUE_HINT_NOT_UTF8` | 文件不是合法 UTF-8 | `gate-1-5-continue-rejected:hint-not-utf8` |

> 注:这四条都是"用户输入边界违规",不是 anti-drop guard 的 G1-G6,**不进 retry,直接 BLOCKED**。处理方式是修正 hint 文件后用新 workflow 重起。size cap 的目的是堵"用户传错文件 / 指向 /dev/zero / 大 binary"导致 orch OOM 或 hang 的失败路径。

### 5. dump —— 想看完整 state 时透传文件

仅当用户**明确**要求"看完整 state""dump 结果""把 result_file 内容打印出来"才使用:

```bash
${PY} ${CLI} dump --result-file <path>
```

## 严格规则

1. **不要把用户的话作为参数 inline**,所有 verbatim 内容必须写文件再传 `--input-file`(注意 256 KiB 上限,超大 PRD 要先裁)
2. **不要把 subagent 结果或 gate 答案 inline**,必须写文件再传 `--decision-file`(注意 256 KiB 上限,subagent 大输出走 artifact 文件 + sha 引用,正文不进 decision-file)
3. **不要给命令加文档里没列的 flag**(`--note` / `--verbose` / `--debug` / `--answer` 一律不许)
4. **不要修改 workflow_id**,从 start 返回里复制原值
5. stdout JSON **整段原样**回给用户,不要重排字段、不要省略字段、不要总结(摘要已经在脚本端做过了,你不要再做一次)
6. 用户没明确要求就**不要**主动调 dump
7. **不要在 `pending_interrupt` 非 null 的情况下硬调 `advance` 假装能推进**——只能 `resume`
8. **不要替 subagent 编 `phase_summary` / `gate_result` 内容**——必须由真实的 subagent 产生,orchestrator 节点只校验+原样落盘
9. **`pending_interrupt.payload.type == "gate_decision_needed"` 时必须把 payload.options 数组逐字呈现给用户**——每个选项独立一行,snake_case 字面量原形保留(`approve` / `rollback_to_pm` / `rollback_to_planning` / `rollback_to_dev` / `continue_planning` / `reject`),后接该选项对应的下游动作摘要(从 4b 节选项矩阵抽取);**禁止**压缩 / 挑选 / 翻译 / 隐藏选项。详见 4b 节"逐字呈现 options"那条注。这是用户决策权的**事前**预防层,跟 `GATE_INVALID_ANSWER` 严重 warning 的**事后**兜底配套——双层防线,前者预防、后者告警

## 三条标准操作流程

### 顺利路径(全程 approve,planner 出 N 个 subtask)

```
start              →  pending=subagent_dispatch (pm_agent)
↓ pm_agent  → X1.json
resume X1          →  pending=gate_decision_needed (Gate 1)
↓ approve   → X2.json
resume X2          →  pending=subagent_dispatch (planner)
↓ planner   → X3.json (含 subtask_plan: [T-001, T-002, T-003])
resume X3          →  pending=gate_decision_needed (Gate 1.5)
↓ approve   → X4.json
resume X4          →  pending=subagent_dispatch (dev_agent, current_subtask=T-001)
↓ dev_agent → dev_T001.json
resume             →  pending=subagent_dispatch (codex_reviewer, T-001 diff)
↓ codex     → codex_pass.json
resume             →  pending=subagent_dispatch (dev_agent, current_subtask=T-002)
... ...(每 subTask 一个 dev + 一个 codex,共 2N 次 resume)...
resume             →  pending=gate_decision_needed (Gate 2),全部 N 个 subTask 跑完
↓ approve   → G2.json
resume             →  pending=subagent_dispatch (rev_agent)
↓ rev_agent → rev.json
resume             →  pending=subagent_dispatch (codex_final, review_mode=final_end_to_end)
↓ codex_fin → codex_final.json
resume             →  pending=gate_decision_needed (Gate 3)
↓ approve   → G3.json
resume             →  stage=DONE
```

**N 个 subTask + R 轮 PM-Dev 协商总共多少次 resume**:`1(PM) + 1(G1) + (1+2R)(PLAN: 1 次 planner done 之前可能穿插 R 轮 [planner-needs + dev-consult]) + 1(G1.5) + 2N(DEV) + 1(G2) + 1(rev_agent) + 1(codex_final) + 1(G3)` = `8 + 2N + 2R`。R=0(没协商)、N=1 时 10 次;R=2、N=1 时 14 次;R=0、N=3 时 14 次。`continue_planning` 后开启的"第二轮协商"也按 R 累加,每多走一轮 +2 次 resume。

### 跨阶段回滚(在 Gate 1.5 处发现需求其实有问题,跳回 PM)

```
... 一路推进到 GATE_1_5 ...
resume rb.json (answer=rollback_to_pm)
                   →  pending_type=subagent_dispatch (pm_agent 重新进入)
                      state.retry_count = 1
                      state.rollback_counts["PM_PHASE"] = 1
↓ runner 重新跑 pm_agent → 新结果
resume pm2.json    →  pending_type=gate_decision_needed (Gate 1)
resume approve     →  pending_type=subagent_dispatch (planner 重新进入)
↓ runner 重新跑 planner
resume plan2.json  →  pending_type=gate_decision_needed (Gate 1.5)
resume approve     →  stage=DONE
                      stage_log 里 PM_PHASE / GATE_1 / PLANNING_PHASE / GATE_1_5 各出现两次
```

### 阻塞路径(reject 或 guard 严重失败)

```
... 推进到任意 gate ...
resume reject.json →  stage=BLOCKED
                      next=[]
                      stage_log 停在 GATE_<n>
```

或在 dispatch 节点上 Anti-Drop Guard 严重失败 + 重试也失败:

```
... resume X1.json (subagent 结果) →  guard G1 严重失败 attempt 1
                                     stage 不变,pending_guard_retry 写入,guard_retries[key]=1
                                     pending=subagent_dispatch (同一个节点,带 guard_failure_hint)
↓ subagent 仍未修正
... resume X2.json                →  guard G1 仍严重失败 attempt 2 (达上限)
                                     stage=BLOCKED
                                     warnings 追加第二条 severe
                                     pending_guard_retry=null
                                     next=[]
                                     stage_log 末位 note=guard-blocked:<subagent>:[G1] (after 1 retry)
```

## PLANNING 内部 PM-Dev 协商(planner ↔ dev_agent 多轮)

### 何时触发

planner 一次 dispatch 不一定能把 subtask_plan 拍稳——它可能需要先问 dev_agent 一些技术性问题(用什么库、是否拆得太粗、依赖怎么处理)再决定怎么拆。这种情况下 planner 在 stage-result 里**主动**返回:

```json
{
  "type": "subagent_result",
  "status": "needs_input",
  "stage": "PLANNING_PHASE",
  "phase_summary": { "...handoff_note 必须非空..." },
  "gate_result": { "name": "Gate 1.5", "passed": false, "reasons": ["awaiting dev consult"] },
  "dev_consultation": {
    "question": "我打算把 ingest 拆成 fetch + parse + write 三个 subtask,但 parse 的依赖很重,要不要单独 spike 一下?"
  }
}
```

orch 看到 `status=needs_input` + `dev_consultation.question` 非空 + 内部协商轮数未到上限,就**不去问用户**,而是 dispatch dev_agent 进**咨询模式**(`consultation_mode=true`)对同一个问题给结构化答复。dev_agent 拿到答复后,orch re-entry planner 让它继续拍。

### 上限与重置

- 内部协商上限 `MAX_PLANNING_CONSULTATIONS = 3`(目前写死,后续可走 `--policies-file` 之类的覆盖)
- 计数字段 `state.planning_consultation_round`,只在内部协商成功完成一轮时 +1;不复用 `retry_count`(retry_count 累的是跨阶段 rollback)
- 触达上限后 planner 仍然 `status=needs_input` → orch 不再自动 dispatch dev,把 stage 推到 GATE_1_5,在 `state.warnings` 里追加一条 `severity=minor` 的 `PLANNING_CONSULT_LIMIT`,让用户在 gate 上从 Gate 1.5 全部四个选项中拍板:`approve`(用户认可现状直接进 DEV)/ `continue_planning`(给方向再开一轮额度)/ `rollback_to_pm`(回 PM 重做)/ `reject`(放弃)
- 用户在 GATE_1_5 选 `continue_planning` 提供 `user_hint_path` 后,`planning_consultation_round` **重置为 0**,planner 重新拿到一份完整的 N 轮协商额度

### dev_agent 咨询模式权限

- 复用思路同 codex_reviewer:**纯只读**
- policy key:`PLANNING_DEV_CONSULT`,默认配置 `Read/Glob/Grep allow`,所有 `Edit/Write/Bash` 一律 deny,`default_action="deny"`,`ask_user=[]`
- dev_agent 在咨询模式下**不准写代码**,答复完全走结构化 stage-result 字段(phase_summary / 一句话 handoff_note / 自己想加的 consultation_answer 字段);planner 在下一轮自己 append `artifacts/plan/discussion.md`(orch 不读这个文件,只在 dispatch payload 里把路径告诉 planner)

### 防输入飘移

planner 的 question **正文**不进 state——只把它存盘的 decision-file path 和 sha256 通过 `state.decisions[-1].file_path / .sha256` 暴露,planning_dev_consult 节点把这两个值塞进 dev_agent 的 dispatch payload(`planner_question_ref: {path, sha256, field}`),dev_agent 自己负责按 path 读文件+校验 sha+提取 `dev_consultation` 字段。orch 全程**不打开**这个文件,也不复述 question。同理,user 通过 `continue_planning` 提供的 hint 文件:orch 读一下文件计算 sha + 长度,以 MsgRef 形式 append 到 `user_planning_hints`,但**不存正文**——planner 下一轮拿 path + sha256 自己读。这两条都走"path + sha,不留正文"模式,跟 raw_requirement 一致。

### 协商记录

每完成一轮(planner 提问 → dev 答复),orch append 一条 `ConsultationRecord(round_index, planner_question_sha256, dev_answer_sha256, recorded_at)` 到 `state.planning_consultations`(append-only)。这串数组就是 PM-Dev 讨论的 audit trail,正文内容用 sha 去外部解析,orch 只保留指针。

### Hermes-side IM 同步

每一轮 planner dispatch 一对 IM(🚀/✅或⚠️),每一轮 dev_agent 咨询 dispatch 一对 IM。**N 轮协商就是 2N 对 IM**,正常事件(开始/完成)。

**stage 字段标注的语义**:咨询模式 dev_agent dispatch 仍属 PLANNING_PHASE 主 stage(`planning_dev_consult` 是子节点而非新 stage 枚举),所以全局 IM 模板里 `<stage>` 字段一律 = `PLANNING_PHASE`,跟 planner 主轮的消息显示同一个 stage 标签。两者的区分点是 `<subagent>` 字段(planner 是 `planner`,咨询是 `dev_agent`)。**Hermes 在咨询模式 dispatch 的 IM 里必须额外加澄清标记**:🚀 消息在 `dispatch <subagent>` 字段后追加 `(consultation_mode=true)`(变成 `dispatch dev_agent (consultation_mode=true)`),让用户立刻分辨这是"PLANNING 阶段内 dev_agent 答 planner 问题"而不是"dev_agent 在做 planning 工作";✅/⚠️ 收尾消息因为全局模板没有 `<subagent>` 字段,Hermes 必须在 `handoff_note=` 之前**补一个 `[dev_agent 咨询答复]` narrative 前缀**(变成 `✅ PLANNING_PHASE 完成:[dev_agent 咨询答复] handoff_note=<...>`)——前缀只是 narrative 装饰,handoff_note **本体仍逐字保留**,跟"禁止 Hermes 改写 handoff_note / reasons"那条不冲突。这条堵的是"用户在 IM 看到 `✅ PLANNING_PHASE 完成` 误以为 PLANNING 整阶段已结束、其实只是一轮 dev 咨询答完"的输入飘移路径,尤其在 N 轮协商场景下混淆面会被放大 N 倍。

失败情形:

- planner `status=blocked` → ⚠️(stage→BLOCKED)
- dev_agent guard severe → ⚠️
- 协商达到上限 → 一条 `⚠️ PLANNING 协商已达 ${MAX} 轮上限,等待用户在 GATE_1.5 选方向`(可由 minor warning `PLANNING_CONSULT_LIMIT` 触发)
- `continue_planning` 缺 `user_hint_path` → severe warning,⚠️ + stage=BLOCKED

## Anti-Drop Guard(dispatch 后结构强校验)

**注意命名**:这里的 G1 / G3 / G4 / G5_DECL / G5_PROVE / G6 是 *subagent 返回结果的结构 guard*(其中 G2 / G7 不做),跟 GATE_1 / GATE_1_5 / GATE_2 / GATE_3 是用户**审批 gate** 不是同一回事——巧合而已,别混。

每个 `subagent_dispatch` 节点 `interrupt` 取回 `result` 后,在落盘进 state 之前强制跑一遍 Anti-Drop Guard。失败结果记到 `state.warnings`(append-only),严重失败直接把 stage 推到 BLOCKED 不再发 interrupt。

### 当前实现的检查项

| 检查 | 内容 | 严重度 | 不通过含义 |
|---|---|---|---|
| G1 | `result.status` 必须存在且 ∈ `{done, blocked, needs_input}` | 严重 | 返回结构畸形,后续字段不可信 |
| G3 | 若 `result.stage` 字段存在,必须等于 dispatch 的 `current_stage` | 严重 | 输入飘移信号——subagent 以为自己在跑别的阶段 |
| G4 | `status=done` ⟹ `gate_result.passed=true`;`passed=false` ⟹ `status ∈ {blocked, needs_input}` | 轻微 | subagent 自我矛盾,逻辑有误 |
| G5_DECL | 本次 dispatch 的 `expected_artifacts` 都必须出现在 `artifact_updates` 的 keys 里(声明级覆盖检查) | 轻微 | subagent 声明了 expected key 但 `artifact_updates` 里没列——声明完成但产物缺失,口头矛盾 |
| G5_PROVE | `artifact_updates` 里每个 key,对应的 `artifact_paths[key]` 文件必须存在,且 orch 自己 sha256 计算结果 == subagent 声明的 sha(真落盘反验) | **严重**(文件不存在)/ 轻微(sha 不匹配 / 未知 key) | 三种情形:(1)**文件不存在** = subagent 明确撒谎(声称写了但文件根本没出现),跟 G1/G3 一样是不可信的输入飘移信号,应给一次重试机会;(2)**sha 不匹配** 仍为轻微——orch 用自己算的 sha 作 ground truth 写入 `state.artifacts`,**subagent 的声明被忽略**,堵的是"subagent 偷改 sha 但文件实际是别的"的输入飘移路径;(3)**未知 key**——subagent 在 `artifact_updates` 里声明了一个 `artifact_paths` 字典里没注册的 key(orch 在 cmd_start 时没给这个 key 分配过路径),detail 形如 `artifact key 'K' not in artifact_paths — orch can't verify`,仍为轻微:orch 没路径可读,既无法证伪文件不存在也无法 sha 校验,只能记 minor warning + **不写 `state.artifacts[K]`**(orch 没 ground truth 可写),堵的是 subagent 凭空发明 artifact key 的飘移路径(graph.py line 587-596 实现) |
| G6 | `phase_summary` 存在 + `handoff_note` 非空 + **handoff_note 单行(无换行/回车)且 ≤ 280 字符** | 轻微 | handoff 信息丢失,或 handoff_note 太长/多行让 Hermes IM 无法逐字引用——后者会逼 Hermes 自己摘要,把"中间 LLM 偷改输入"的口子又开回来 |

> **不做**:
> - **G2** `next_action` 非空:本图直接靠 stage 状态机决定下一步,没有 `next_action` 字段,加它是装饰
> - **G7** state 写回:LangGraph checkpoint 自动管,无需重复校验

> **G4 在 codex_reviewer / codex_final 上的预期行为(不是飘移信号)**:codex 的 review-failure 路径(状态机图 DEV_PHASE 后的 footnote 明示"codex review 失败 不中断 DEV")要求 codex 返回 `status=done + gate_result.passed=false`,这跟 G4 契约(`passed=false ⟹ status ∈ {blocked, needs_input}`)天然冲突 → **每次 codex review 失败必然触发一条 G4 minor warning**。这是设计意图,**不是输入飘移信号**——⚠️ failure IM(IM 模板表的 ⚠️ 行)已在 dispatch 出口当场告诉用户 codex 不通过,Hermes **不必**再为该 G4 minor 单独通知;Anti-Drop Guard 段尾"minor 不强制 IM,可在 dispatch ⏎ 总结捎一句"那条仍适用,但提醒措辞应当注明"其中 codex 的 G4 minor 是预期路径,不是新问题",尤其在 N 个 subtask 全部 review 失败时,result_file 会出现 N 条 G4 minor,如果不显式标注,会掩盖真正非预期的其他 minor warning。同理 codex_final 在 REV 阶段 passed=false 时也走同款 G4 minor + ⚠️ failure IM 双信号,处理逻辑一致。

### 失败动作

| 严重度 | 第一次发生 | 第二次仍失败 |
|---|---|---|
| severe | **不立刻 BLOCKED**——先调度一次"带 hint 重试":`state.pending_guard_retry` 写入失败 findings,`state.guard_retries[<stage>:<subagent>]++`,stage 保持不变,LangGraph 条件边把控制权送回**同一个 dispatch 节点**;下次进入该节点,dispatch_payload 会挂 `guard_failure_hint` 把 findings 透传给 subagent;**`stage_log` 末位同时 append note** `guard-retry-pending:<subagent>:[<check_ids>] attempt=<N>/<MAX>`(graph.py line 528-530 实现),audit replay 可凭这条 note 识别"首次 severe 已触发带 hint 重试"路径,跟下一格的 `guard-blocked:...` 终止 note 形成"重试 → 仍失败 → BLOCKED" 完整事件轨迹 | `stage = BLOCKED`,`warnings` 再追加,`stage_log` 末位 note `guard-blocked:<subagent>:[<ids>] (after 1 retry)`,后继边短路到 END |
| minor | `warnings` 追加一条,workflow 正常推进到下一个节点;不影响 stage | (同上,minor 不触发重试) |

**G5_DECL / G5_PROVE 派发到上表的方式**:

| 检查 | 触发条件 | 走哪条 |
|---|---|---|
| G5_DECL | `expected_artifacts` 缺 key | 轻微 → 写 minor warning,workflow 推进 |
| G5_PROVE — 文件不存在 | subagent 声明 key 但 `artifact_paths[key]` 文件不存在 | **严重** → 首次带 hint 重试;重试仍不存在 → BLOCKED |
| G5_PROVE — sha 不匹配 | 文件存在但 orch 算的 sha256 ≠ subagent 声明的 sha | 轻微 → 写 minor warning,workflow 推进(`state.artifacts[K].sha256` 用 orch 算的真值,subagent 声明被覆盖) |
| G5_PROVE — 未知 key | subagent 声明 `artifact_updates[K]` 但 K 不在 `artifact_paths` 字典(orch 在 cmd_start 时没给该 key 分配路径) | 轻微 → 写 minor warning(detail `artifact key 'K' not in artifact_paths — orch can't verify`),workflow 推进(state.artifacts **不写 K**,orch 没路径可读;graph.py line 587-596 实现) |

**重试上限**:`GUARD_RETRY_LIMIT = 1`(每个 `<stage>:<subagent_name>` 独立计数,K=1 一次重试机会)。

**计数键的粒度**:同一阶段不同 subagent 各自计数;DEV 内部 per-subTask 也是 `dev_agent[T-001]` / `codex_reviewer[T-001]` / `dev_agent[T-002]` 各一个键独立计数,所以 T-001 用尽重试不会影响 T-002 的额度。**完整 subagent_name 命名约定**(graph.py 各 dispatch 节点的 `_consume_pending_retry` subagent_name 参数):`pm_agent`(PM_PHASE) / `planner`(PLANNING_PHASE) / `dev_agent[consult]`(PLANNING_PHASE 咨询模式专用,跟 PM-Dev 协商章节"Hermes-side IM 同步"段的"stage 字段标注语义"一致,graph.py line 1108) / `dev_agent[<task_id>]` 与 `codex_reviewer[<task_id>]`(DEV per-subTask) / `rev_agent`(REV_PHASE) / `codex_final`(REV_PHASE 第二段)—— 这些就是 `state.guard_retries` 和 `pending_guard_retry.subagent` 字段实际看到的字符串集合。用户验证 `state.guard_retries` 看到 `PLANNING_PHASE:dev_agent[consult]` 时,`[consult]` 后缀表示这是协商模式的额度,跟 `dev_agent[T-001]` 等 DEV 模式额度独立。

### 给 subagent 的重试 hint 形态

retry 那一次 dispatch_payload 多一个字段(其他正常 dispatch 这字段为 null):

```json
{
  "guard_failure_hint": {
    "stage": "PM_PHASE",
    "subagent": "pm_agent",
    "attempt": 1,
    "findings": [
      {
        "stage": "PM_PHASE",
        "subagent": "pm_agent",
        "check_id": "G1",
        "severity": "severe",
        "detail": "illegal status 'WHATEVER', must be one of ('done', 'blocked', 'needs_input')",
        "recorded_at": "..."
      }
    ]
  }
}
```

subagent SKILL.md 应识别这个字段并据 detail 校正返回。orch 不替 subagent 编 correction message——只把原始 findings 透传。

### 不复用 retry_count / rollback_counts

guard 重试有自己的 `state.guard_retries: dict[str, int]`,**与 retry_count(rollback 累积)和 planning_consultation_round(PM-Dev 协商)完全独立**。这样三类"重做"语义不会互相干扰:

- rollback(用户在 gate 上拍 rollback)→ `retry_count++` + `rollback_counts[<stage>]++`
- PM-Dev 协商(planner needs_input 自动)→ `planning_consultation_round++`
- guard 重试(severe 自动一次)→ `guard_retries[<key>]++`

### Hermes-side 的 IM 通知义务

当 `_persist_and_summarize` 写回的 `state.warnings` 比上一次多了 `severity=severe` 条目时:

- **若 `state.pending_guard_retry` 非空**(进入 retry 路径):发一条 `⚠️ <stage>[ subtask=<task_id>] guard 严重失败 attempt=<N>/<MAX>:<check_ids>,正在重新 dispatch <subagent> 给一次修正机会,detail=<...>`。这是**进度通知不是终止通知**,用户感知到 guard 拦了但 workflow 还在跑
- **若 stage=BLOCKED 且 `check_id ∈ {G1, G3, G4, G5_PROVE, G5_DECL, G6}`**(guard retry 用尽):发一条 `⚠️ <stage>[ subtask=<task_id>] guard 严重失败已用尽重试 → BLOCKED:<check_ids>,detail=<...>`。这是终止通知(注意 G5_PROVE 文件不存在才会触发 severe BLOCKED;G5_DECL 永远是轻微,只有当它跟其他严重检查并发触发时才会出现在此 set 的 BLOCKED check_ids 里)
- **若 stage=BLOCKED 且 `check_id == "GATE_INVALID_ANSWER"`**(用户在 gate 上提交了 options 之外的 answer):发一条 `⚠️ <gate>(GATE_<n>) 收到无法识别的 answer=<原 answer>,options 是 <options 数组逐字>,workflow 已 BLOCKED 区别于显式 reject。请检查 decision-file 是否拼错/答错 gate/字段缺失,确认意图后用新 workflow 重起`。这条**必须**发——是"用户以为自己 approve 了实际却 BLOCKED"的最后一道感知防线;reasons 与 options 必须**逐字**贴出,Hermes 不许总结
- **若 check_id ∈ `{GATE_1_5_CONTINUE_NO_HINT, GATE_1_5_CONTINUE_HINT_UNREADABLE, GATE_1_5_CONTINUE_HINT_TOO_LARGE, GATE_1_5_CONTINUE_HINT_NOT_UTF8}`**(continue_planning 的 hint 文件违规):发一条 `⚠️ Gate 1.5 continue_planning 的 hint 文件违规(<check_id>):<detail 逐字>。workflow 已 BLOCKED,请按 detail 修正 hint 文件后用新 workflow 重起`。`detail` 必须**逐字**贴出(里面包含原 path / 实测字节数 / 错误根因),不许 Hermes 自己改写——这是用户判断"自己路径写错了"还是"系统 cap 太严"的唯一依据

四种情形都不等同于"status!=done 或 gate.passed=false"那条 ⚠️——severe warning 是独立的失败感知通道,不能延后,在每个 dispatch / gate 节点出口当场发。

minor 不强制 IM,但建议在最近一次 dispatch ⏎ 总结里捎一句"今轮带 N 条 minor warning,可在 result_file 里看 `state.warnings`"。

## Artifact 真落盘 + 反验

### 路径布局

`cmd_start` 时 cli 在 `scripts/workflows/<workflow_id>/` 下创建产物目录树:

```
scripts/workflows/<wid>/
└── artifacts/
    ├── pm/
    │   ├── spec.md
    │   └── risks.md
    ├── plan/
    │   ├── plan.md
    │   ├── task_list.md
    │   └── discussion.md
    ├── dev/
    │   ├── summary.md
    │   ├── self_check.md
    │   └── changed_files.txt
    └── rev/
        ├── report.md
        └── acceptance_checklist.md
```

`state.artifact_base_dir` 存这个目录的绝对路径。`state.artifact_paths` 各 key 是这棵树下对应文件的**绝对路径**——subagent 在 dispatch_payload 里直接拿到绝对路径,不需要自己拼接 base。

**DEV 阶段 artifact 是 aggregate(last-write-wins),不是 per-subtask 隔离**:`dev_summary` / `dev_self_check` / `dev_changed_files` 这三个 key 在 N 个 subtask 的 dev_agent dispatch 里被**复用**——orch 给所有 subtask 同一组 `artifact_paths` 路径(`_default_artifact_paths`,graph.py line 89-107),T-K 的 dev_agent 写完 `artifacts/dev/summary.md` 后 T-K+1 直接**覆盖**同一文件。每次 dispatch 内部 G5_PROVE 反验逻辑独立:T-K 自己声明的 sha 只跟 T-K 写完瞬间的文件 sha 比对,T-K+1 进来时文件已经是 T-K 的内容,T-K+1 dev_agent 应当先读取(若需要)再覆盖写新内容。**DEV 完成后,磁盘上 `artifacts/dev/*` 只剩 T-N(最后 subtask)的内容**;per-subtask verbatim 在 `state.subtask_results[sid].dev_handoff_note / .codex_handoff_note / .review_reasons` 里保留(`subtask_results` 也通过 dispatch_payload 挂给下游 rev_agent,见 graph.py line 1815-1817)。**REV / 审计 / 用户读取 per-subtask 历史必须走 `state.subtask_results`,不要从 `artifacts/dev/*` 文件反推**——否则只能拿到 T-N 一份,T-1 .. T-(N-1) 的 dev/codex 文本输出在 artifact 层已经被覆盖丢失。同理 `state.artifacts["dev_summary"]` 等 key 的 sha / written_by / written_at 也只反映最后一次写入(T-N),想审计某个中间 subtask 的 sha 要靠 `state.subtask_results[sid].dev_decision_sha256` 之类字段。

### 谁负责落盘

- **subagent 写文件**:dispatch policy(PM_PHASE / PLANNING_PHASE / DEV_PHASE / REV_PHASE)在 `allow_silent` 里放行 `Write({base}/artifacts/<stage>/**)` 和 `Edit(...)`,subagent 直接写到 `artifact_paths[key]` 就好
- **orch 反验**:dispatch 返回时,orch 自己读文件 + 算 sha256,跟 subagent 声明对比;**只信 orch 自己算的 sha**,把 verified `ArtifactRef(path, sha256, written_by, written_at)` 写进 `state.artifacts`(append/upsert)

### 反验的三种结果

| 情形 | G5_PROVE / G5_DECL warning | `state.artifacts` 写入 |
|---|---|---|
| 文件存在 + sha 匹配 | 无 | ✅ 用 orch 计算的 sha |
| 文件存在 + sha 不匹配 | 有(`artifact 'K' sha mismatch ...`) | ✅ **仍然用 orch 计算的真 sha**(subagent 声明被忽略) |
| 文件不存在(subagent 声明了 key 但没真落盘) | 有(`artifact 'K' claimed but file missing`) | ❌ 该 key **不写入** `state.artifacts` |
| `artifact_updates` 含未知 key(K 不在 `artifact_paths`) | 有(G5_PROVE minor,`artifact key 'K' not in artifact_paths — orch can't verify`) | ❌ 该 key **不写入** `state.artifacts`(orch 没路径可读) |
| `artifact_updates` 缺 key | 有(G5_DECL,`artifact_updates missing keys: [...]`) | ❌ 缺的 key 不写 |

这四类加起来构成"subagent 不能伪造已落盘"的硬保护:任何谎报都会留下对应的 G5_DECL/G5_PROVE warning(severity 按情形:G5_PROVE 文件不存在 severe,G5_PROVE sha 不匹配 minor,G5_PROVE 未知 key minor,G5_DECL 缺 key minor)+ state.artifacts 实情。

### 防输入飘移角度

- **path + sha,正文不进 state**:`state.artifacts[K]` 只存绝对路径 + sha(由 orch 现场算),不存正文——跟 raw_requirement / user_planning_hints / planner_question_ref 的设计一致
- **下游 subagent 拿 artifact**:rev_agent / dev_agent / codex_reviewer / codex_final 看到的都是 `artifact_paths` 字典;它们自己负责按路径读 + 用 `state.artifacts[K].sha256` 校验文件是否还是当时被钉死的版本
- **审计**:任意一次回放 result_file → `state.artifacts[K].path / .sha256 / .written_by / .written_at` 可还原"那一刻文件就是这个 sha,由哪个 stage 写的"

### G5_DECL(声明级)与 G5_PROVE(真文件反验)的关系

声明级和真文件 sha 反验**两者都跑**,findings 互不替代:

- 声明级缺 key → G5_DECL minor warning(`expected_artifacts` 没都覆盖)
- 真文件:文件不存在 → G5_PROVE **severe** warning(首次带 hint 重试,二次仍失败 BLOCKED);文件存在但 sha 不对 → G5_PROVE minor warning

举例:planner 说自己 done,但 `artifact_updates` 没有 `task_list`(缺 key)→ 一条 G5_DECL minor warning 进 `state.warnings`,workflow 仍推进到 GATE_1.5 让用户看;若同时 `plan.md` 文件被声明但没真落盘 → 一条 G5_PROVE **severe** warning 触发首次带 hint 重试(planner 第二次仍漏写则 BLOCKED),`state.artifacts` 里 `plan` key 不会写入(因为 `plan.md` 文件其实没落盘)。

## 用户怎么验证保真

返回小摘要里:

| 字段 | 验证方法 |
|---|---|
| `user_msg_sha256s[0]` | 等于本地 `sha256sum <input_file>` 的值 |
| `decision_sha256s[i]` | 等于本地 `sha256sum <第 i 个 decision_file>` 的值 |
| `stage` | 应符合状态机预期(对照上面三条 SOP) |
| `workflow_id` | 整个 workflow 的稳定标识。stdout 摘要里的 `workflow_id` 跟 `state.workflow_id` 必然一致(都来自启动时设定/生成的同一个值)。**充当 LangGraph checkpoint 的 thread_id**(graph_cli.py line 39 + line 98 `_config({"thread_id": workflow_id})`),所以严格规则 4 不许改——改了后续命令找不到 checkpoint(thread_id 不匹配)。**生成格式**:`wf-YYYYMMDD-HHMMSS-<microseconds>`(graph_cli.py line 110-111 `_new_workflow_id`,微秒后缀让"同秒并发 start"也能区分);用户也可传自定义 `--workflow-id`,只需保证全局唯一(否则覆盖旧 checkpoint)。`state.workflow_id` 也跟 `state.artifact_base_dir` 路径里的 `<wid>` 与 `result_file` 路径 `scripts/results/<wid>/<cmd>-<ts>.json` 子目录一致(三处 wid 必然相同)|
| `stage_log_entries` | (stdout 摘要)单调追加,长度只增不减;rollback 后会出现重复阶段。**这里只列 stage 名字符串**(graph_cli.py line 201 `[s["stage"] for s in stage_log]` 投影);完整的 `StageRecord` 列表(含 `note` / `entered_at` / `seen_msg_ids` / `seen_decision_ids` 字段)在 result_file 的 `state.stage_log`,**note 字符串完整目录见上面"stage_log notes 词汇表"段** —— 想验证某条 audit-replay note(如 `dev-loop:enter-subtask-T-001` / `guard-retry-pending:...`)必须读 `state.stage_log[i].note`,stdout 摘要里没有 |
| `pending_interrupt.payload.type` | 在 PM/PLAN/DEV-内部/REV-内部 dispatch 节点上是 `subagent_dispatch`,在 GATE_1/GATE_1_5/GATE_2/GATE_3 时是 `gate_decision_needed`,DONE/BLOCKED 时为 null |
| `result_file` 里的 `state.phase_summaries[<stage>]` | 应**逐字**等于 subagent 给的 phase_summary,没有任何字段被 orchestrator 改写或合并。**例外**:`DEV_PHASE` 是 synthetic-aggregate(handoff_note 以 `DEV done (synthetic);` 开头),verbatim 来源在 `state.subtask_results[sid].{dev,codex}_handoff_note`;`open_issues` 每条 review_reason 单独一行(可能多条同 sid),不许 `", ".join` |
| `result_file` 里的 `state.gate_results[<gate>]` | 应**逐字**等于 subagent 给的 gate_result(代表 subagent 自己的声明,不是用户的最终批准)。**例外**:`state.gate_results["Gate 2"]` 是 orch 合成的(graph.py line 1473-1483 dev_loop_router 队列耗尽时根据 `all(r.review_passed for r in state.subtask_results.values())` 聚合 + `reasons` 拼出"subtask {sid} codex review failed" 列表),**不是**任何单一 subagent 的 verbatim claim——跟 `phase_summaries["DEV_PHASE"]` 是 synthetic-aggregate 同一类设计(N 个 codex_reviewer 没法产单一 gate_result,orch 必须聚合)。verbatim 来源在 `state.subtask_results[sid].review_passed` 与 `state.subtask_results[sid].review_reasons` —— 想审计某 subtask 的 codex 真实判定要查这两个字段,不要从合成 Gate 2 反推。其余 4 个 keys(`"Gate 1"` / `"Gate 1.5"` / `"Gate 3 - rev_agent"` / `"Gate 3"`)都仍是 verbatim subagent claim |
| `result_file` 里的 `state.retry_count` / `rollback_counts` | rollback 一次就 +1,无 rollback 时为 0 / 空 dict |
| `result_file` 里的 `state.warnings` | append-only;每次 dispatch 后只增不减,即便 subagent 返回完美也会保留历史 minor。**末位 `severity=severe` 不再等同于 BLOCKED**——如果只是首次 severe,workflow 正在 retry 中(看 `state.pending_guard_retry` 是否非 null);只有当 retry 也失败、`stage_log` 末位是 `guard-blocked:...` 才进入 BLOCKED |
| `result_file` 里的 `state.guard_retries` | 每个 `<stage>:<subagent>` 键独立累加;append-only(BLOCKED 也保留)。出现 ≥1 表示该 dispatch 被 guard 严重拦截过,可对照 `state.warnings` 里同 stage/subagent 的 severe 条目验证 |
| `result_file` 里的 `state.pending_guard_retry` | 正常情况下 = `null`。非空时表示当前 dispatch 节点等待 subagent 重交一次结果;字段含 `stage / subagent / attempt / findings`,attempt = `guard_retries[key]`(同步) |
| `result_file` 里的 `state.planning_consultation_round` / `state.planning_consultations` | 累计内部协商轮数;`continue_planning` 后 round 重置为 0,但 `planning_consultations` 仍保留全历史(不清);每条记录只有 sha + round 索引,无正文 |
| `result_file` 里的 `state.user_planning_hints` | 每次 GATE_1.5 选 `continue_planning` 给的 hint 文件以 MsgRef 形式 append;字段含 `sha256` / `byte_length` / `text_path`,`sha256sum` 本地 hint 文件应等于这里的值 |
| `state.decisions[i].file_path` | 该 audit Decision 对应的 verbatim decision-file 绝对路径(若有);用来回溯 PM-Dev 协商问答的源文件 |
| `state.artifact_base_dir` | `scripts/workflows/<wid>/` 的绝对路径,cli 在 cmd_start 时设定。`state.artifact_paths` 的所有路径都以它为前缀 |
| `state.updated_at` | ISO 8601 UTC 时间戳字符串(`now_iso()` = `datetime.now(timezone.utc).isoformat()`)。**每次节点 return 都重写**(graph.py 各节点 return 字典里都有 `"updated_at": now_iso()`,line 517/539/749/792/862 等)。**不是** workflow 创建时间(没有 `created_at` 字段),只是"最后一次状态更新的时间"。审计用途:对照 stage_log 末位的 `entered_at` / `exited_at` 大致 = updated_at(同一节点 return 时一起设);中长时间没变化(几分钟以上)说明 workflow 卡在某个 interrupt 等用户/runner 响应,跟 stdout `pending_interrupt.payload.type` 配合诊断 |
| `state.artifact_paths[K]` | 每个 artifact key 对应的目标文件**绝对路径**;subagent 直接写、orch 直接读 |
| `state.artifacts[K]` | 反验后 orch 写入的 ArtifactRef:`path` / `sha256`(orch 自己算的真值)/ `written_by`(写它的 stage)/ `written_at`。验证保真:`sha256sum <path>` 应等于这里的 `.sha256`。某个 key 缺位 = subagent 声明过但没真落盘 / 文件被删 |
| `result_file` 里的 `state.user_msgs[i]` | 用户消息 MsgRef 列表(append-only):每个 raw_requirement(`i=0`) 和 continue_planning hint(`i>=1`) 进来都 append 一条。**字段**(per `MsgRef` schema): `msg_id` / `sha256` / `received_at` / `text_path`(verbatim 文件绝对路径) / `byte_length` / `char_length`。验证保真:`sha256sum <text_path>` 必须等于 `.sha256`;stdout 摘要里的 `user_msg_sha256s[i]` 就是这里 `.sha256` 的镜像。**正文不进 state**(只 path + sha,跟 raw_requirement 的设计一致) |
| `result_file` 里的 `state.subtask_results[<task_id>]` | DEV 阶段 per-subtask 完整结果记录(SubtaskResult)。**字段**:`dev_status`(dev_agent 返回的 status) / `dev_handoff_note`(dev_agent 的 phase_summary.handoff_note **逐字**) / `dev_decision_sha256`(dev decision-file sha) / `codex_status`(codex_reviewer 返回的 status) / `codex_handoff_note`(codex 的 handoff_note **逐字**) / `codex_decision_sha256` / `review_passed`(codex 的 gate_result.passed 声明) / `review_reasons`(codex 的 gate_result.reasons 数组,**每条独立保留**,不许 join)。**这是 DEV 阶段 per-subtask verbatim 唯一的 ground truth**——artifact 文件是 last-write-wins 只剩 T-N 的内容,phase_summaries["DEV_PHASE"] 是 synthetic-aggregate 压缩摘要(`DEV done (synthetic);` 开头),只有 subtask_results 留**逐字**历史。rev_agent / 审计 / Hermes IM 想看具体某 sid 的 dev/codex 原文必须读这里(详见上面"DEV 阶段 artifact 是 aggregate"那段)。验证保真:`state.subtask_results[sid].dev_handoff_note` 应等于该 sid 的 dev_agent dispatch ✅/⚠️ IM 里 quote 的 handoff_note;rollback_to_dev 后整个 dict 被清空,旧记录只在历史 result_file 文件里留痕 |
| `result_file` 里的 `state.subtasks` | planner 在 PLANNING_PHASE 给的 subtask 队列(`list[SubtaskSpec]`):每条含 `task_id` / `title` / `description`(state.py line 79-84)。**写入语义**:planner 一次性返回 subtask_plan,orch 直接落进 state.subtasks;rollback_to_planning 后会被新一轮 planner **整段替换**;rollback_to_dev **不动 state.subtasks**(只清 subtask_results + reset current_subtask_index,Gate 选项矩阵的 rollback_to_dev 行已暗示)。验证保真:`len(state.subtasks)` 等于 SOP 计数公式 `8 + 2N + 2R` 里的 `N`;每个 `state.subtasks[i].task_id` 应跟 stage_log 里 `dev-loop:enter-subtask-<task_id>` 与 `dev-subtask-<task_id>-dispatched:*` 一一对应(用"stage_log notes 词汇表"段交叉检验)|
| `result_file` 里的 `state.current_subtask_index` | DEV 队列指针(int,默认 `-1`)。**含义**:`-1` 表示 DEV 还没开始或刚 reset(rollback_to_dev / DEV 队列耗尽后);`0 ≤ idx < len(state.subtasks)` 表示当前正在处理的 subtask(对应 `state.subtasks[idx]`);**等于 -1 又看到 stage=DEV_PHASE** 是 dev_loop_router 队列耗尽后准备聚合到 GATE_2 的瞬态(dev_loop_router 内 reset, graph.py line 1489 `"current_subtask_index": -1`)。**写入语义**:每次 dev_loop_router `enter-subtask-X` 都 +1(graph.py line 1416-1425);Gate 3 rollback_to_dev 重置回 -1。验证保真:在 DEV 中途的任何 result_file 里,`state.current_subtask_index` 应等于 `state.subtask_results` dict 当前已有的 entry 数 - 1(因为下标从 0 起);DEV 完成或 BLOCKED 时是 -1 |
| `result_file` 里的 `state.permission_policies` | `dict[stage_name, PolicyConfig]`,每个阶段最终生效的权限策略(orch 内置默认与 `--policies-file` 用户覆盖合并后的结果)。Keys 通常含 `PM_PHASE` / `PLANNING_PHASE` / `DEV_PHASE` / `REV_PHASE` / `CODEX_REVIEW`(codex_reviewer + codex_final 共用)/ `PLANNING_DEV_CONSULT`(dev_agent 咨询模式)。每个 PolicyConfig 含 `allow_silent` / `deny` / `ask_user` / `default_action`(state.py line 102-116)。**验证保真**:(a) 用户传 `--policies-file` 时,这里的内容应等于文件内容经 `PolicyConfig` schema 校验后的 normalized 形式(graph_cli.py `_load_policies` 走 `PolicyConfig(**body)` 标准化);(b) **`ask_user` 必为空、`default_action` ∈ `{"allow_silent", "deny"}` 但绝不是 `"ask_user"`**(start 期硬拒,见 graph_cli.py line 130-140)——任何阶段违反都会让 `start` SystemExit,workflow 不创建,所以一旦 result_file 存在就保证已通过校验;(c) 没传 `--policies-file` 时这里是 orch 内置默认(`_default_policies`,graph.py line 110+),内容跟 "5 个命令"章节"各阶段的内置默认"表一致 |

如果用户想看完整 state,引导用户:
- 自己 `cat <result_file>`,或
- 让 Hermes 调 `dump --result-file <path>`(透传,不是摘要)

## stage_log notes 词汇表

stage_log 是 append-only 的 StageRecord 列表,每个节点跑完 append 一条(含 `note` 字段),用于 audit replay。下面是 graph.py 实际产生的所有 note 字符串完整目录,跟 result_file 里的 `stage_log_entries` 配对解析:

| 来源 | note 格式 | 触发 / 含义 |
|---|---|---|
| INIT | `initialized` | INIT 节点 setup 完成,种入 artifact_paths + permission_policies + stage→PM_PHASE |
| PM_PHASE | `pm-dispatch:<answer>` | pm_agent dispatch 返回(`<answer>` = `result.status`,如 `done` / `blocked` / `needs_input`)|
| GATE_1 | `gate-1-approved` | 用户选 approve |
| GATE_1 | `gate-1-rollback-to-pm` | 用户选 rollback_to_pm(`retry_count++` + `rollback_counts["PM_PHASE"]++`)|
| GATE_1 | `gate-1-rejected:<answer>` | 用户选 reject(`<answer>` 总是字面量 `reject`)—— 注意带 `:answer` 后缀,跟 `invalid-answer` 仅靠 note 主体(`rejected` vs `invalid-answer`)区分 |
| GATE_1 | `gate-1-invalid-answer:<answer>` | answer 不在 options 里(typo / 跨 gate / 字段缺失),触发 `GATE_INVALID_ANSWER` severe warning |
| PLANNING_PHASE | `planning-dispatch:<answer>` | planner dispatch 返回 |
| PLANNING_PHASE 内部协商 | `planning-dev-consult:round=<N>/<MAX> status=<answer>` | dev_agent 咨询模式 dispatch 返回(`<N>` 是新 round_index,`<MAX>` = `MAX_PLANNING_CONSULTATIONS=3`)|
| PLANNING_PHASE 状态分支 | `planning-blocked-by-planner` | planner 报 `status="blocked"` → 直接推到 BLOCKED(graph.py line 1027)|
| PLANNING_PHASE 状态分支 | `planning-needs-dev-consult:round=<N>/<MAX>` | planner 报 `status="needs_input"` 且 `dev_consultation` 字段非空且 round < MAX → 留在 PLANNING_PHASE,路由到 planning_dev_consult 节点(graph.py line 1043)|
| PLANNING_PHASE 状态分支 | `planning-needs-input-surface-to-gate:exhausted=<bool>` | planner 报 `status="needs_input"` 但 (`dev_consultation` 缺失/为空 OR round >= MAX),无法继续协商 → 推到 GATE_1.5 让用户决策(`<bool>` 为 true 表示协商达上限,false 表示 planner 没挂 dev_consultation 字段;前者会同时 append `PLANNING_CONSULT_LIMIT` minor warning)(graph.py line 1075)|
| GATE_1_5 | `gate-1-5-approved` | 用户选 approve(进 DEV)|
| GATE_1_5 | `gate-1-5-rollback-to-pm` | 用户选 rollback_to_pm |
| GATE_1_5 | `gate-1-5-continue-planning:hint_sha=<前 12 字符>` | 用户选 continue_planning,4 个 hint check 全过(完整 sha 在 `state.user_planning_hints[i].sha256`)|
| GATE_1_5 | `gate-1-5-continue-rejected:no-user-hint-path` | continue_planning decision-file 缺 user_hint_path(`GATE_1_5_CONTINUE_NO_HINT`)|
| GATE_1_5 | `gate-1-5-continue-rejected:unreadable-hint` | hint 文件 stat / read 抛 OSError(`GATE_1_5_CONTINUE_HINT_UNREADABLE`)|
| GATE_1_5 | `gate-1-5-continue-rejected:hint-too-large` | hint 文件 > 64 KiB(`GATE_1_5_CONTINUE_HINT_TOO_LARGE`)|
| GATE_1_5 | `gate-1-5-continue-rejected:hint-not-utf8` | hint 文件不是合法 UTF-8(`GATE_1_5_CONTINUE_HINT_NOT_UTF8`)|
| GATE_1_5 | `gate-1-5-rejected:<answer>` / `gate-1-5-invalid-answer:<answer>` | 同 GATE_1 模式 |
| DEV_PHASE 内部 | `dev-loop:enter-subtask-<task_id>` | dev_loop_router 进入下一个 subtask |
| DEV_PHASE 内部 | `dev-loop:exit subtasks_done=<N>/<M> all_passed=<bool>` | dev_loop_router 队列耗尽,聚合 phase_summary["DEV_PHASE"] + Gate 2 gate_result,推到 GATE_2 |
| DEV_PHASE 内部 | `dev-subtask-<task_id>-dispatched:<answer>` | dev_agent 单 subtask dispatch 返回(`<answer>` = dev_agent 的 status)|
| DEV_PHASE 内部 | `dev-subtask-<task_id>-reviewed:passed=<bool>` | codex_reviewer 单 subtask dispatch 返回(`<bool>` = codex 的 `gate_result.passed`)|
| DEV_PHASE 边界 | `dev-subtask-dispatch:no-current` / `codex-review:no-current` | router 错位的 fallback note,正常路径不会出现 |
| GATE_2 | `gate-2-approved` / `gate-2-rollback-to-planning` / `gate-2-rollback-to-pm` / `gate-2-rejected:<answer>` / `gate-2-invalid-answer:<answer>` | 同 GATE_1 模式 |
| REV_PHASE | `rev-agent-dispatched:<answer>` | rev_agent dispatch 返回 |
| REV_PHASE | `codex-final-dispatched:<answer> passed=<bool>` | codex_final dispatch 返回(`<bool>` 直接拼进 note,因为 Gate 3 信号源就是 codex_final 的 `gate_result.passed`)|
| GATE_3 | `gate-3-approved` | 用户选 approve(进 DONE)|
| GATE_3 | `gate-3-rollback-to-dev` | 用户选 rollback_to_dev(同时清空 `subtask_results` + `current_subtask_index = -1`,见上面"全清重跑"段)|
| GATE_3 | `gate-3-rollback-to-planning` | 用户选 rollback_to_planning |
| GATE_3 | `gate-3-rollback-to-pm` | 用户选 rollback_to_pm |
| GATE_3 | `gate-3-rejected:<answer>` / `gate-3-invalid-answer:<answer>` | 同 GATE_1 模式 |
| 任意 dispatch 节点 | `guard-retry-pending:<subagent>:[<check_ids>] attempt=<N>/<MAX>` | severe 首次失败,触发带 hint 重试(graph.py line 528-530)|
| 任意 dispatch 节点 | `guard-blocked:<subagent>:[<ids>] (after <N> retry)` | severe 重试仍失败,推到 BLOCKED |

**audit 注意点**:

- `gate-N-rejected` 的精确格式是 `gate-N-rejected:reject`(answer 总是字面量 `reject`)。SKILL.md 早期 4b 节 Gate 选项矩阵的 `reject` 行写 `gate-N-rejected`(无后缀)是简写,跟本表对照解析时按精确格式(带 `:reject`)
- DEV-internal notes(`dev-loop:*` / `dev-subtask-*` / `dev-subtask-dispatch:no-current` / `codex-review:no-current`)的 StageRecord.stage 字段都是 `DEV_PHASE` —— DEV 三节点不进新 stage 枚举(状态机图 DEV_PHASE 块底注),仅靠 note 主体区分
- 协商 dispatch(`planning-dev-consult:*`)的 stage 字段同样保持 `PLANNING_PHASE` —— 跟 PM-Dev 协商章节"Hermes-side IM 同步"段的"stage 字段标注语义"一致
- audit replay 把 stage_log 视作事件流时,典型成对模式:
  - dispatch 返回 → `<dispatch>:<answer>` note,接下来要么进 gate 要么进下一节点
  - gate 决策 → `gate-N-<action>` note
  - guard 严重失败 → `guard-retry-pending:...` note → 重新进入同一 dispatch 节点 → 第二次返回若仍 severe → `guard-blocked:...` note + END
  - DEV 主循环 → `dev-loop:enter-subtask-Tk` → `dev-subtask-Tk-dispatched:Y` → `dev-subtask-Tk-reviewed:passed=Z` → `dev-loop:enter-subtask-Tk+1` ...直到队列耗尽 `dev-loop:exit ...`

## 与 `shrimp/orchestrator/` 设计的对齐进度

当前已实现:
- ✅ Stage 架构: INIT / PM_PHASE / GATE_1 / PLANNING_PHASE / GATE_1_5 / DEV_PHASE / GATE_2 / REV_PHASE / GATE_3 / DONE / BLOCKED
- ✅ 真实 dispatch:pm_agent / planner / dev_agent(per-subTask)/ codex_reviewer(per-subTask)/ rev_agent / codex_final,都带 permission_policy(codex_reviewer 与 codex_final 共用 CODEX_REVIEW 只读策略;REV_PHASE 紧策略只允许写 `artifacts/rev/**`)
- ✅ DEV_PHASE 按 planner 给的 subtask_plan 自动迭代:每 subTask 跑一对 dev_agent + codex_reviewer dispatch;codex review 失败**不中断 workflow 但 Hermes 必须当场 ⚠️ 通知用户**(规则在 SKILL.md "硬要求" 那张表),失败信号也聚合给 Gate 2 做最终拍板
- ✅ REV_PHASE 两段串行 dispatch:rev_agent 出 rev_report + acceptance_checklist → codex_final 端到端只读 review,**codex_final 的 gate_result.passed 即 Gate 3 信号**;rev_agent 自己的 claim 单独存到 `gate_results["Gate 3 - rev_agent"]` 做溯源
- ✅ Gate 1 / Gate 1.5(三选一)+ Gate 2(四选一,双向 rollback 到 PLANNING 或 PM)+ Gate 3(五选一,三向 rollback 到 DEV / PLANNING / PM,DEV rollback 清空 subtask_results 重跑)
- ✅ phase_summaries / gate_results / retry_count / rollback_counts / artifact_paths 字段;REV 阶段 phase_summary 拆 `REV_PHASE`(rev_agent)与 `REV_PHASE_CODEX`(codex_final)两键
- ✅ 跨阶段回滚边: GATE_1 → PM,GATE_1_5 → PM,GATE_2 → PLANNING / PM,GATE_3 → DEV / PLANNING / PM(retry_count++,对应 rollback_counts++)
- ✅ phase_summary / gate_result 经 pydantic 校验后**逐字**落盘,orchestrator 不再二次摘要
- ✅ 每阶段 `permission_policy` 默认策略 + `--policies-file` 覆盖 + 启动期硬拒 `ask_user`,消除 cc-lead 跑到一半等审批没人批的失败模式
- ✅ Hermes 必须在 dispatch 开始 / 结束 / cc-lead 报 blocker 时给用户发 IM 同步,实现"不审批但感知" —— 规则写在本 SKILL.md 而非自定义 dispatch 字段,Hermes 加载 skill 即读到。**REV 阶段两次 dispatch 各自一对 IM**(rev_agent 一对、codex_final 一对),codex_final passed=false 也必须当场 ⚠️ 不能等到 Gate 3
- ✅ Anti-Drop Guard MVP(G1/G3 严重 + G5_PROVE 严重(文件不存在)/轻微(sha 不匹配)+ G4/G5_DECL/G6 轻微)在 6 个 dispatch 节点(pm/planner/dev_agent/codex_reviewer/rev_agent/codex_final)统一执行;严重失败 → `state.stage=BLOCKED` + `state.warnings` 追加 + 后继边短路到 END;轻微失败 → 写 `warnings` 但正常推进。Hermes 必须在 severe warning 出现时当场 ⚠️
- ✅ PLANNING 内部 PM-Dev 多轮协商:planner `status=needs_input` + `dev_consultation` → planning_dev_consult 节点 dispatch dev_agent(`PLANNING_DEV_CONSULT` 只读 policy)→ 回 planner 再跑;上限 `MAX_PLANNING_CONSULTATIONS=3`,达上限或缺 dev_consultation 字段 → 推到 GATE_1_5;Gate 1.5 新增 `continue_planning`,用户给 `user_hint_path` → orch 读文件算 sha,append `user_planning_hints`,`planning_consultation_round` 重置 0,planner 重新拿额度;`Decision.file_path` 字段贯通 audit trail 让协商问答可回溯到源文件;**正文不进 state**(只 path + sha,跟 raw_requirement 同模式)
- ✅ Anti-Drop Guard severe 失败带 hint 重试:每个 `<stage>:<subagent>` 独立 `GUARD_RETRY_LIMIT=1` 一次重试机会;首次 severe → `state.pending_guard_retry` 写入 + 控制权送回同一节点 + dispatch_payload 挂 `guard_failure_hint`;再失败才 BLOCKED。DEV 内部 per-subTask 各自独立计数(`dev_agent[T-001]` / `codex_reviewer[T-001]` 等),T-001 用尽不影响 T-002 的额度。三种"重做"语义(rollback / 协商 / guard 重试)各自独立计数互不干扰
- ✅ Artifact 真落盘 + sha 反验:cli 在 cmd_start 创建 `scripts/workflows/<wid>/artifacts/{pm,plan,dev,rev}/` 目录树,`state.artifact_base_dir` 设绝对路径,`artifact_paths` 全部为绝对路径直接给 subagent 写;dispatch 返回时 orch 调 `_verify_and_persist_artifacts` 自己 sha256 文件,跟 subagent 声明对比,**用 orch 算出的 sha 作 ground truth** 写入 `state.artifacts[K]`;文件不存在 → **severe** G5_PROVE warning(首次重试,二次 BLOCKED);sha 不匹配 → minor G5_PROVE warning;`expected_artifacts` 缺 key → minor G5_DECL warning;subagent 谎报无法伪造 `state.artifacts`。G5_DECL 跑声明级覆盖、G5_PROVE 跑真文件反验,两条独立 checks 都会执行

下一步要补的(经评估都不做):
- ❌ Anti-Drop Guard G2 `next_action`:本图状态机自己决定下一步,加该字段是装饰
- ❌ DEPOSIT_PHASE 知识沉淀:可选阶段,不属于核心闭环,后续如有需要再起新刀
