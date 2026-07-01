package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ming-agents/server/adapter"
)

type clarificationRun struct {
	AgentType   string
	SessionID   string
	Prompt      string
	PromptFile  string
	OutFile     string
	ExitFile    string
	HistoryFile string
	Session     AgentSession
}

type clarificationOutput struct {
	AgentType   string
	SessionID   string
	Status      string
	ExitCode    int
	Output      string
	Err         error
	PromptFile  string
	OutFile     string
	ExitFile    string
	RepoRoot    string
	HistoryFile string
	Session     AgentSession
}

type clarificationAgentExecutor func(context.Context, string, clarificationRun) clarificationOutput

func RunClarification(ctx context.Context, repoRoot, userInput string) (string, error) {
	return RunClarificationWithMemory(ctx, repoRoot, userInput, "")
}

func RunClarificationWithMemory(ctx context.Context, repoRoot, userInput, memoryBlock string) (string, error) {
	runID := time.Now().Format("20060102-150405")
	runRoot := filepath.Join(repoRoot, ".workflow", "runs", runID)
	nodeDir := filepath.Join(runRoot, "node1")
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		return "", err
	}
	nodeSession := WorkflowNodeSession(repoRoot, runID, "node1")
	_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node1", Status: NotificationStarted})
	_ = writeWorkflowState(repoRoot, runID, map[string]NodeStatus{
		"node1": NodeRunning,
		"node2": NodePending,
		"node3": NodePending,
	}, nil)

	runs := []clarificationRun{
		newClarificationRun(runID, nodeDir, "codex", userInput, memoryBlock, 1),
		newClarificationRun(runID, nodeDir, "claude-code", userInput, memoryBlock, 2),
	}

	// 注册每个 agent session，以便 WaitForApproval 能找到对应 session
	for i := range runs {
		session := AgentSession{
			ID:          runs[i].SessionID,
			AgentType:   runs[i].AgentType,
			Status:      AgentSessionPending,
			HistoryFile: filepath.Join(nodeDir, runs[i].AgentType+".messages.jsonl"),
		}
		RegisterAgentSession(session)
		runs[i].HistoryFile = session.HistoryFile
		runs[i].Session = session
	}

	for _, run := range runs {
		if err := writeTextAtomic(run.PromptFile, run.Prompt); err != nil {
			_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node1", Status: NotificationFailed})
			return "", err
		}
	}

	outputs := make([]clarificationOutput, len(runs))
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outputs[i] = executeClarificationAgentWithSession(ctx, repoRoot, runs[i])
		}(i)
	}
	wg.Wait()

	// 每个 agent 独立通过审批门，被拒则重跑（最多3次）
	for i := range outputs {
		agentNodeName := "node1:" + outputs[i].AgentType
		if err := waitForAgentApproval(ctx, repoRoot, &outputs[i], runs[i], agentNodeName, executeClarificationAgentWithSession); err != nil {
			_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node1", Status: NotificationFailed})
			return "", err
		}
	}

	merged := mergeClarificationOutputs(runID, outputs)
	target := filepath.Join(repoRoot, "docs", "requirements-clarity.md")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node1", Status: NotificationFailed})
		return "", err
	}
	if err := writeTextAtomic(target, merged); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node1", Status: NotificationFailed})
		return "", err
	}

	state := NodeCompleted
	err := error(nil)
	if allClarificationAgentsFailed(outputs) {
		state = NodeFailed
		err = fmt.Errorf("all clarification agents failed")
	}
	notifyStatus := NotificationCompleted
	if err != nil {
		notifyStatus = NotificationFailed
	}
	_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node1", Status: notifyStatus})
	_ = writeWorkflowState(repoRoot, runID, map[string]NodeStatus{
		"node1": state,
		"node2": NodePending,
		"node3": NodePending,
	}, map[string]any{"clarification_file": target})
	return target, err
}

func newClarificationRun(runID, nodeDir, agentType, userInput, memoryBlock string, index int) clarificationRun {
	sessionID := NewPTYSessionID(runID, "node1", agentType, index)
	base := agentType
	return clarificationRun{
		AgentType:  agentType,
		SessionID:  sessionID,
		Prompt:     prependRelevantMemory(memoryBlock, renderClarificationPrompt(agentType, userInput)),
		PromptFile: filepath.Join(nodeDir, base+".prompt.md"),
		OutFile:    filepath.Join(nodeDir, base+".out.md"),
		ExitFile:   filepath.Join(nodeDir, base+".exit"),
	}
}

func executeClarificationAgent(ctx context.Context, repoRoot string, run clarificationRun) clarificationOutput {
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	out := clarificationOutput{
		AgentType:  run.AgentType,
		SessionID:  run.SessionID,
		Status:     "completed",
		ExitCode:   0,
		PromptFile: run.PromptFile,
		OutFile:    run.OutFile,
		ExitFile:   run.ExitFile,
		RepoRoot:   repoRoot,
	}
	var output string
	var err error
	switch run.AgentType {
	case "codex":
		mgr := adapter.NewCodexSessionManager(adapter.CodexConfig{Command: "codex"})
		sess, startErr := mgr.StartSession(runCtx, repoRoot)
		if startErr != nil {
			err = startErr
			break
		}
		defer sess.Close()
		output, err = sess.SendPrompt(runCtx, run.Prompt)
	case "claude-code":
		mgr := adapter.NewClaudeCodeSessionManager(adapter.ClaudeCodeConfig{Command: "claude"})
		sess, startErr := mgr.StartSession(runCtx, repoRoot)
		if startErr != nil {
			err = startErr
			break
		}
		defer sess.Close()
		output, err = sess.SendPrompt(runCtx, run.Prompt)
	default:
		err = fmt.Errorf("unsupported clarification agent %q", run.AgentType)
	}

	if err != nil {
		out.Status = "failed"
		out.ExitCode = 1
		out.Err = err
		if output == "" {
			output = err.Error()
		}
	}
	out.Output = output
	_ = os.WriteFile(run.OutFile, []byte(output), 0644)
	_ = os.WriteFile(run.ExitFile, []byte(fmt.Sprintf("%d\n", out.ExitCode)), 0644)
	if code, waitedOutput, waitErr := WaitSessionExit(ctx, run.ExitFile, run.OutFile, time.Second); waitErr == nil {
		out.ExitCode = code
		out.Output = waitedOutput
	}
	return out
}

// executeClarificationAgentWithSession 在执行后填充 HistoryFile 和 Session
func executeClarificationAgentWithSession(ctx context.Context, repoRoot string, run clarificationRun) clarificationOutput {
	out := executeClarificationAgent(ctx, repoRoot, run)
	out.HistoryFile = run.HistoryFile
	out.Session = run.Session
	return out
}

// waitForAgentApproval 实现单个 agent 的审批门：等待通过，被拒时重跑（最多3次）
func waitForAgentApproval(ctx context.Context, repoRoot string, out *clarificationOutput, run clarificationRun, agentNodeName string, execute clarificationAgentExecutor) error {
	revisions := 0
	for {
		session := out.Session
		if session.ID == "" {
			session = run.Session
		}
		if session.HistoryFile == "" {
			session.HistoryFile = run.HistoryFile
		}
		session.Status = AgentSessionPending
		RegisterAgentSession(session)

		// 写 initial attempt（attempt=0）/ revision 后的最新 attempt 已在重跑分支写入，
		// 此处仅在 attempt 0 时记录 initial（best-effort，不中断主流程）。
		if revisions == 0 {
			_ = writeClarificationAttempt(repoRoot, runIDFromSessionID(out.SessionID), clarificationLineageNodeID, out.AgentType, out.SessionID, revisions, -1, "initial", FailureClassNone, "", out.PromptFile, out.OutFile, out.ExitFile)
		}

		// 通知完成
		_ = EmitNodeNotification(out.SessionID, NodeNotification{
			RunID:    runIDFromSessionID(out.SessionID),
			NodeName: agentNodeName,
			Status:   NotificationCompleted,
		})

		approvalErr := WaitForApproval(ctx, out.SessionID, agentNodeName)
		if approvalErr == nil {
			return nil // 通过
		}
		if !errors.Is(approvalErr, ErrApprovalRejected) {
			return approvalErr // 其他错误（非 rejection）
		}

		// 被拒绝：读取修订指令，追加到 session，重新执行
		decision, ok, err := LatestReviewDecision(out.SessionID, agentNodeName)
		if err != nil {
			return err
		}
		revision := "Please revise your previous output."
		if ok && !decision.Approved && strings.TrimSpace(decision.Reason) != "" {
			revision = strings.TrimSpace(decision.Reason)
		}
		runID := runIDFromSessionID(out.SessionID)
		lineage := NewFileLineageStore(repoRoot)
		rctx := RollbackContext{
			RunID:    runID,
			NodeID:   clarificationLineageNodeID,
			NodeKind: NodeKindClarification,
			Unit: RollbackUnit{
				Scope:       "clarification:" + out.AgentType,
				MaxAttempts: DefaultRollbackSpec(NodeKindClarification).DefaultUnit.MaxAttempts,
				ReusePolicy: SessionReuseSameSession,
			},
			Budget:  RollbackBudget{ExhaustedAction: RollbackActionBlocked},
			Lineage: lineage,
		}
		attempts := syntheticRollbackAttempts(rctx.Unit.Scope, revisions)
		if runID != "" {
			listed, err := lineage.List(AttemptFilter{
				RunID:  runID,
				NodeID: clarificationLineageNodeID,
				Scope:  rctx.Unit.Scope,
			})
			if err != nil {
				return err
			}
			attempts = rollbackBudgetEvents(listed)
		}
		rollbackDecision := NewRollbackRunner().Decide(rctx, DefaultRollbackSpec(NodeKindClarification), rctx.Unit, attempts, HumanRejectSignal(rctx.Unit, decision))
		if rollbackDecision.Action != RollbackActionFixClarification {
			return fmt.Errorf("agent %s exceeded max revision attempts", out.AgentType)
		}
		if err := AppendAgentMessage(&SubtaskAgent{Session: session}, AgentMessage{
			Role:    "user",
			Content: revision,
		}); err != nil {
			return err
		}

		// 写 revision attempt（human_reject，attempt=N，N=1,2,3...）（best-effort）。
		_ = writeClarificationAttempt(repoRoot, runIDFromSessionID(out.SessionID), clarificationLineageNodeID, out.AgentType, out.SessionID, revisions+1, revisions, "human_reject", FailureClassHumanReject, revision, out.PromptFile, out.OutFile, out.ExitFile)

		// 通知进入重跑
		_ = EmitNodeNotification(out.SessionID, NodeNotification{
			RunID:    runIDFromSessionID(out.SessionID),
			NodeName: agentNodeName,
			Status:   NotificationStarted,
		})

		// 重新执行该 agent（带修订指令追加到 prompt）
		nextRun := run
		nextRun.Prompt = renderClarificationRevisionPrompt(run.Prompt, out.Output, revision)
		nextRun.Session = session
		*out = execute(ctx, repoRoot, nextRun)
		if out.Err != nil {
			return fmt.Errorf("rerun clarification prompt: %w", out.Err)
		}
		revisions++
	}
}

func renderClarificationRevisionPrompt(originalPrompt, previousOutput, revision string) string {
	var b strings.Builder
	b.WriteString(originalPrompt)
	b.WriteString("\n\n# Previous Clarification Output\n")
	b.WriteString(strings.TrimSpace(previousOutput))
	b.WriteString("\n\n# Revision Request\n")
	b.WriteString("Please revise your clarification based on this feedback:\n")
	b.WriteString(strings.TrimSpace(revision))
	b.WriteString("\n")
	return b.String()
}

func renderClarificationPrompt(agentType, userInput string) string {
	if agentType == "claude-code" {
		return strings.TrimSpace(`# Role
You are the Claude Code requirements clarification agent for a dynamic workflow run.

# Input Requirements
`+userInput+`

# Task
Independently critique and clarify the requirements. Focus on missing context, dependencies, validation strategy, and edge cases. Do not modify files.

# Output Format
Return markdown with these exact headings:

## Ambiguities
## Assumptions
## Acceptance Criteria
## Risks
## Suggested Subtasks`) + "\n"
	}
	return strings.TrimSpace(`# Role
You are the Codex requirements clarification agent for a dynamic workflow run.

# Input Requirements
`+userInput+`

# Task
Analyze the requirements independently. Do not implement anything.

# Output Format
Return markdown with these exact headings:

## Ambiguities
- List unclear requirements and why each matters.

## Assumptions
- List concrete assumptions that would unblock planning.

## Acceptance Criteria
- List observable completion criteria.

## Risks
- List technical or process risks.

## Suggested Subtasks
- List candidate implementation subtasks with likely repo paths.`) + "\n"
}

func mergeClarificationOutputs(runID string, outputs []clarificationOutput) string {
	var b strings.Builder
	b.WriteString("# Requirements Clarity\n\n")
	fmt.Fprintf(&b, "run_id: %s\nnode: 1\nstate: %s\ngenerated_at: %s\n\n", runID, clarificationState(outputs), time.Now().Format(time.RFC3339))
	for _, out := range outputs {
		fmt.Fprintf(&b, "## Agent Result: %s\n\n", out.AgentType)
		fmt.Fprintf(&b, "session: %s\nstatus: %s\nexit_code: %d\n", out.SessionID, out.Status, out.ExitCode)
		if out.Err != nil {
			fmt.Fprintf(&b, "error: %s\n", out.Err.Error())
		}
		fmt.Fprintf(&b, "\n<!-- agent:%s begin -->\n", out.AgentType)
		b.WriteString(strings.TrimSpace(out.Output))
		if !strings.HasSuffix(out.Output, "\n") {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "<!-- agent:%s end -->\n\n", out.AgentType)
	}
	b.WriteString("## Aggregated Clarifications\n\n")
	b.WriteString("### Confirmed Requirements\n")
	b.WriteString("- Preserve the explicit requirements from each tagged agent section for planning.\n\n")
	b.WriteString("### Open Questions\n")
	b.WriteString("- Resolve any ambiguities listed by either clarification agent before broad implementation.\n\n")
	b.WriteString("### Planning Inputs\n")
	b.WriteString("- Use repo paths, constraints, acceptance criteria, and risks from the tagged sections above.\n")
	return b.String()
}

func clarificationState(outputs []clarificationOutput) NodeStatus {
	if allClarificationAgentsFailed(outputs) {
		return NodeFailed
	}
	return NodeCompleted
}

// clarificationLineageNodeID is the node ID used for clarification attempt lineage.
const clarificationLineageNodeID = "clarification"

// writeClarificationAttempt 写 clarification attempt event（best-effort）。
// attempt 编号语义：0 = initial（agent 第一次跑完），1+ = revision（reject 后重跑）。
// lineage 写入失败不中断主流程。
func writeClarificationAttempt(repoRoot, runID, nodeID, agentType, sessionID string, attempt int, parentAttempt int, trigger string, failureClass FailureClass, rejectionReason, promptPath, outputPath, exitPath string) error {
	scope := "clarification:" + agentType
	var outcome *AttemptOutcome
	if failureClass != "" && failureClass != FailureClassNone {
		status := "failed"
		if failureClass == FailureClassHumanReject {
			status = "rejected"
		}
		outcome = &AttemptOutcome{
			Status:       status,
			Passed:       false,
			FailureClass: failureClass,
			Reason:       rejectionReason,
		}
	}
	return writeAttemptEvent(repoRoot, runID, nodeID, NodeKindClarification, scope, "assistant", sessionID, attempt, parentAttempt, trigger, failureClass, string(failureClass), rejectionReason, outcome, nil, promptPath, outputPath, exitPath)
}

func allClarificationAgentsFailed(outputs []clarificationOutput) bool {
	if len(outputs) == 0 {
		return true
	}
	for _, out := range outputs {
		if out.Status == "completed" && out.ExitCode == 0 {
			return false
		}
	}
	return true
}
