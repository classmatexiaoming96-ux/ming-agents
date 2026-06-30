package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ming-agents/server/adapter"
)

type clarificationContext struct {
	RunID             string
	AgentSections     map[string]string
	AggregatedSection string
	Raw               string
}

type planningAgentRun struct {
	RunID       string
	AgentID     string
	AgentType   string
	SessionID   string
	Prompt      string
	PromptFile  string
	OutFile     string
	ExitFile    string
	HistoryFile string
	Session     AgentSession
}

type planningAgentOutput struct {
	AgentType   string
	SessionID   string
	Status      string
	ExitCode    int
	Output      string
	Err         error
	PromptFile  string
	OutFile     string
	ExitFile    string
	HistoryFile string
	Session     AgentSession
}

type planningAgentExecutor func(context.Context, string, planningAgentRun) planningAgentOutput

func RunPlanning(ctx context.Context, repoRoot, clarFile string) (*Plan, error) {
	data, err := os.ReadFile(clarFile)
	if err != nil {
		return nil, fmt.Errorf("read clarification file: %w", err)
	}
	clar := parseClarificationMarkdown(string(data))
	if len(clar.AgentSections) == 0 {
		return nil, fmt.Errorf("no tagged agent sections found in %s", clarFile)
	}
	runID := clar.RunID
	if runID == "" {
		runID = time.Now().Format("20060102-150405")
		clar.RunID = runID
	}
	nodeSession := WorkflowNodeSession(repoRoot, runID, "node2")
	_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node2", Status: NotificationStarted})

	prompt := renderPlanningPrompt(clar)
	nodeDir := filepath.Join(repoRoot, ".workflow", "runs", runID, "node2")
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node2", Status: NotificationFailed})
		return nil, err
	}
	run := newPlanningAgentRun(runID, nodeDir, "codex", prompt, 1)
	out := executePlanningAgent(ctx, repoRoot, run)
	if out.Err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node2", Status: NotificationFailed})
		return nil, fmt.Errorf("run planning prompt: %w", out.Err)
	}
	if err := waitForPlanningAgentApproval(ctx, repoRoot, &out, run, executePlanningAgent); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node2", Status: NotificationFailed})
		return nil, err
	}
	if out.Err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node2", Status: NotificationFailed})
		return nil, fmt.Errorf("run planning prompt: %w", out.Err)
	}
	plan, err := parsePlanningOutputWithRollback(ctx, repoRoot, nodeSession, &out, run, executePlanningAgent)
	if err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node2", Status: NotificationFailed})
		return nil, err
	}

	target := filepath.Join(repoRoot, "docs", "planning.md")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node2", Status: NotificationFailed})
		return nil, err
	}
	if err := writeTextAtomic(target, renderPlanningMarkdown(clar, clarFile, out.Output, plan)); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node2", Status: NotificationFailed})
		return nil, fmt.Errorf("write planning file: %w", err)
	}
	_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node2", Status: NotificationCompleted})
	if runID != "" {
		_ = writeWorkflowState(repoRoot, runID, map[string]NodeStatus{
			"node1": NodeCompleted,
			"node2": NodeCompleted,
			"node3": NodePending,
		}, map[string]any{"planning_file": target})
	}
	return plan, nil
}

func parsePlanningOutputWithRollback(ctx context.Context, repoRoot string, nodeSession AgentSession, out *planningAgentOutput, run planningAgentRun, execute planningAgentExecutor) (*Plan, error) {
	retries := 0
	for {
		plan, err := extractPlanJSON(out.Output)
		if err == nil {
			err = validatePlan(plan)
		}
		if err == nil {
			return plan, nil
		}

		reason := "planning output contract error: " + err.Error()
		spec := DefaultRollbackSpec(NodeKindPlanning)
		unit := spec.DefaultUnit
		rctx := RollbackContext{
			RunID:    run.RunID,
			NodeID:   planningLineageNodeID,
			NodeKind: NodeKindPlanning,
			Unit:     unit,
			Budget:   RollbackBudget{ExhaustedAction: RollbackActionBlocked},
			Lineage:  NewFileLineageStore(repoRoot),
		}
		rollbackDecision := NewRollbackRunner().Decide(rctx, spec, unit, syntheticRollbackAttempts(unit.Scope, retries), RollbackSignal{
			FailureClass: FailureClassContractError,
			Reason:       reason,
			SourceNode:   run.AgentID,
		})
		if rollbackDecision.Action != RollbackActionRegeneratePlan {
			return nil, err
		}
		parentAttempt := rollbackDecision.NewAttempt - 1
		_ = writePlanningAttempt(repoRoot, run.RunID, planningLineageNodeID, out.SessionID, rollbackDecision.NewAttempt, parentAttempt, "contract_error", FailureClassContractError, reason, out.PromptFile, out.OutFile, out.ExitFile)

		session := out.Session
		if session.ID == "" {
			session = run.Session
		}
		if session.HistoryFile == "" {
			session.HistoryFile = run.HistoryFile
		}
		_ = AppendAgentMessage(&SubtaskAgent{Session: session}, AgentMessage{Role: "user", Content: reason})
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: run.RunID, NodeName: run.AgentID, Status: NotificationStarted})

		nextRun := run
		nextRun.Prompt = renderPlanningRevisionPrompt(run.Prompt, out.Output, reason)
		nextRun.Session = session
		*out = execute(ctx, repoRoot, nextRun)
		if out.Err != nil {
			return nil, fmt.Errorf("rerun planning prompt after contract error: %w", out.Err)
		}
		retries++
	}
}

func newPlanningAgentRun(runID, nodeDir, agentType, prompt string, index int) planningAgentRun {
	sessionID := NewPTYSessionID(runID, "node2", agentType, index)
	agentID := "node2:" + agentType
	historyFile := filepath.Join(nodeDir, agentType+".messages.jsonl")
	session := AgentSession{
		ID:          sessionID,
		AgentType:   agentType,
		Status:      AgentSessionPending,
		HistoryFile: historyFile,
	}
	return planningAgentRun{
		RunID:       runID,
		AgentID:     agentID,
		AgentType:   agentType,
		SessionID:   sessionID,
		Prompt:      prompt,
		PromptFile:  filepath.Join(nodeDir, agentType+".prompt.md"),
		OutFile:     filepath.Join(nodeDir, agentType+".out.md"),
		ExitFile:    filepath.Join(nodeDir, agentType+".exit"),
		HistoryFile: historyFile,
		Session:     session,
	}
}

func executePlanningAgent(ctx context.Context, repoRoot string, run planningAgentRun) planningAgentOutput {
	RegisterAgentSession(run.Session)
	_ = writeTextAtomic(run.PromptFile, run.Prompt)
	_ = AppendAgentMessage(&SubtaskAgent{Session: run.Session}, AgentMessage{Role: "system", Content: run.Prompt})

	out := planningAgentOutput{
		AgentType:   run.AgentType,
		SessionID:   run.SessionID,
		Status:      "completed",
		PromptFile:  run.PromptFile,
		OutFile:     run.OutFile,
		ExitFile:    run.ExitFile,
		HistoryFile: run.HistoryFile,
		Session:     run.Session,
	}
	output, err := runCodexPrompt(ctx, repoRoot, run.Prompt, 30*time.Minute)
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
	_ = AppendAgentMessage(&SubtaskAgent{Session: run.Session}, AgentMessage{Role: "assistant", Content: output})
	RegisterAgentSession(AgentSession{
		ID:          out.SessionID,
		AgentType:   run.AgentID,
		Status:      AgentSessionPending,
		HistoryFile: out.HistoryFile,
	})
	return out
}

func waitForPlanningAgentApproval(ctx context.Context, repoRoot string, out *planningAgentOutput, run planningAgentRun, execute planningAgentExecutor) error {
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
		RegisterAgentSession(AgentSession{
			ID:          out.SessionID,
			AgentType:   run.AgentID,
			Status:      AgentSessionPending,
			HistoryFile: session.HistoryFile,
		})
		_ = EmitNodeNotification(out.SessionID, NodeNotification{
			RunID:    run.RunID,
			NodeName: run.AgentID,
			Status:   NotificationCompleted,
		})

		// 写 initial attempt（attempt=0，best-effort，不中断主流程）。
		// revision 后的 attempt 在重跑分支写入。
		if revisions == 0 {
			_ = writePlanningAttempt(repoRoot, run.RunID, planningLineageNodeID, out.SessionID, revisions, -1, "initial", FailureClassNone, "", out.PromptFile, out.OutFile, out.ExitFile)
		}

		approvalErr := WaitForApproval(ctx, out.SessionID, run.AgentID)
		if approvalErr == nil {
			return nil
		}
		if !errors.Is(approvalErr, ErrApprovalRejected) {
			return approvalErr
		}

		decision, ok, err := LatestReviewDecision(out.SessionID, run.AgentID)
		if err != nil {
			return err
		}
		revision := "Please revise your previous planning output."
		if ok && !decision.Approved && strings.TrimSpace(decision.Reason) != "" {
			revision = strings.TrimSpace(decision.Reason)
		}
		lineage := NewFileLineageStore(repoRoot)
		rctx := RollbackContext{
			RunID:    run.RunID,
			NodeID:   planningLineageNodeID,
			NodeKind: NodeKindPlanning,
			Unit: RollbackUnit{
				Scope:       "planning",
				MaxAttempts: DefaultRollbackSpec(NodeKindPlanning).DefaultUnit.MaxAttempts,
				ReusePolicy: SessionReuseSameSession,
			},
			Budget:  RollbackBudget{ExhaustedAction: RollbackActionBlocked},
			Lineage: lineage,
		}
		attempts := syntheticRollbackAttempts(rctx.Unit.Scope, revisions)
		if run.RunID != "" {
			listed, err := lineage.List(AttemptFilter{
				RunID:  run.RunID,
				NodeID: planningLineageNodeID,
				Scope:  rctx.Unit.Scope,
			})
			if err != nil {
				return err
			}
			if budgetEvents := rollbackBudgetEvents(listed); len(budgetEvents) > 0 {
				attempts = budgetEvents
			}
		}
		rollbackDecision := NewRollbackRunner().Decide(rctx, DefaultRollbackSpec(NodeKindPlanning), rctx.Unit, attempts, HumanRejectSignal(rctx.Unit, decision))
		if rollbackDecision.Action != RollbackActionRegeneratePlan {
			return fmt.Errorf("agent %s exceeded max revision attempts", run.AgentID)
		}
		_ = AppendAgentMessage(&SubtaskAgent{Session: session}, AgentMessage{
			Role:    "user",
			Content: revision,
		})

		_ = writePlanningAttempt(repoRoot, run.RunID, planningLineageNodeID, out.SessionID, revisions+1, revisions, "human_reject", FailureClassHumanReject, revision, out.PromptFile, out.OutFile, out.ExitFile)

		_ = EmitNodeNotification(out.SessionID, NodeNotification{
			RunID:    run.RunID,
			NodeName: run.AgentID,
			Status:   NotificationStarted,
		})

		nextRun := run
		nextRun.Prompt = renderPlanningRevisionPrompt(run.Prompt, out.Output, revision)
		*out = execute(ctx, repoRoot, nextRun)
		if out.Err != nil {
			return fmt.Errorf("rerun planning prompt: %w", out.Err)
		}
		revisions++
	}
}

func renderPlanningRevisionPrompt(originalPrompt, previousOutput, revision string) string {
	var b strings.Builder
	b.WriteString(originalPrompt)
	b.WriteString("\n\n# Previous Planning Output\n")
	b.WriteString(strings.TrimSpace(previousOutput))
	b.WriteString("\n\n# Revision Request\n")
	b.WriteString("Please revise your planning output based on this feedback:\n")
	b.WriteString(strings.TrimSpace(revision))
	b.WriteString("\n")
	return b.String()
}

func validatePlan(plan *Plan) error {
	if plan == nil {
		return errors.New("plan is required")
	}
	if strings.TrimSpace(plan.TaskID) == "" {
		return errors.New("task_id is required")
	}
	if len(plan.Subtasks) == 0 {
		return errors.New("subtasks must contain at least one item")
	}
	seen := make(map[string]bool, len(plan.Subtasks))
	for i, st := range plan.Subtasks {
		if strings.TrimSpace(st.ID) == "" {
			return fmt.Errorf("subtask at index %d missing id", i)
		}
		if seen[st.ID] {
			return fmt.Errorf("duplicate subtask id %q", st.ID)
		}
		seen[st.ID] = true
		if st.AgentType != "codex" {
			return fmt.Errorf("unsupported agent_type for %s: %q", st.ID, st.AgentType)
		}
		if !validRepoPath(st.RepoPath) {
			return fmt.Errorf("invalid repo_path for %s: %q", st.ID, st.RepoPath)
		}
		if strings.TrimSpace(st.Description) == "" {
			return fmt.Errorf("subtask %s missing description", st.ID)
		}
		if len(st.AcceptanceCriteria) == 0 {
			return fmt.Errorf("subtask %s missing acceptance criteria", st.ID)
		}
		for j, criterion := range st.AcceptanceCriteria {
			if strings.TrimSpace(criterion) == "" {
				return fmt.Errorf("subtask %s acceptance criteria %d is empty", st.ID, j)
			}
		}
	}
	return nil
}

func validRepoPath(repoPath string) bool {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" || filepath.IsAbs(repoPath) {
		return false
	}
	clean := filepath.Clean(repoPath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == ".." {
			return false
		}
	}
	return true
}

func parseClarificationMarkdown(markdown string) clarificationContext {
	clar := clarificationContext{
		Raw:           markdown,
		AgentSections: map[string]string{},
		RunID:         extractMetadataLine(markdown, "run_id"),
	}
	re := regexp.MustCompile(`(?s)<!-- agent:([^ ]+) begin -->(.*?)<!-- agent:([^ ]+) end -->`)
	for _, match := range re.FindAllStringSubmatch(markdown, -1) {
		agent := strings.TrimSpace(match[1])
		if agent != strings.TrimSpace(match[3]) {
			continue
		}
		clar.AgentSections[agent] = strings.TrimSpace(match[2])
	}
	if idx := strings.Index(markdown, "## Aggregated Clarifications"); idx >= 0 {
		clar.AggregatedSection = strings.TrimSpace(markdown[idx:])
	}
	return clar
}

func extractMetadataLine(markdown, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func renderPlanningPrompt(clar clarificationContext) string {
	var b strings.Builder
	b.WriteString("# Role\n")
	b.WriteString("You are the planning agent for a dynamic workflow run.\n\n")
	b.WriteString("# Source\n")
	b.WriteString("docs/requirements-clarity.md\n\n")
	b.WriteString("# Clarification Context\n")
	for agent, section := range clar.AgentSections {
		fmt.Fprintf(&b, "## Agent: %s\n%s\n\n", agent, section)
	}
	if clar.AggregatedSection != "" {
		b.WriteString(clar.AggregatedSection)
		b.WriteString("\n\n")
	}
	b.WriteString("# Task\n")
	b.WriteString("Create a concrete execution plan. Do not modify files other than returning the planning document.\n\n")
	b.WriteString("# Output Format\n")
	b.WriteString("Return markdown with a human-readable planning summary followed by exactly one fenced JSON block under '## Execution Plan JSON'.\n")
	b.WriteString("The JSON must match this schema:\n")
	b.WriteString("```json\n")
	b.WriteString(`{"task_id":"stable-task-id","subtasks":[{"id":"subtask-1","agent_type":"codex","repo_path":"relative/path","description":"concrete work","acceptance_criteria":["observable criterion"]}]}`)
	b.WriteString("\n```\n")
	b.WriteString("All subtasks must use agent_type codex and repo_path values relative to the repository root without '..'.\n")
	return b.String()
}

func extractPlanJSON(markdown string) (*Plan, error) {
	re := regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")
	match := re.FindStringSubmatch(markdown)
	if len(match) != 2 {
		return nil, errors.New("planning output missing fenced json plan")
	}
	var plan Plan
	if err := json.Unmarshal([]byte(match[1]), &plan); err != nil {
		return nil, fmt.Errorf("parse planning json: %w", err)
	}
	return &plan, nil
}

func renderPlanningMarkdown(clar clarificationContext, clarFile, plannerOut string, plan *Plan) string {
	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	runID := clar.RunID
	if runID == "" {
		runID = plan.TaskID
	}
	var b strings.Builder
	b.WriteString("# Planning\n\n")
	fmt.Fprintf(&b, "run_id: %s\nnode: 2\nstate: COMPLETED\nsource: %s\n\n", runID, filepath.ToSlash(clarFile))
	b.WriteString("## Planning Summary\n\n")
	summary := extractSection(plannerOut, "Planning Summary")
	if strings.TrimSpace(summary) == "" {
		b.WriteString("- Goal: execute the clarified user request through validated subtasks.\n")
		b.WriteString("- Execution strategy: dispatch each validated subtask to a Codex development session.\n")
		b.WriteString("- Review strategy: run a review barrier after development and retry blocking subtasks once.\n")
	} else {
		b.WriteString(strings.TrimSpace(summary))
		b.WriteString("\n")
	}
	b.WriteString("\n## Execution Plan JSON\n\n```json\n")
	b.Write(planJSON)
	b.WriteString("\n```\n")
	return b.String()
}

func extractSection(markdown, heading string) string {
	lines := strings.Split(markdown, "\n")
	target := "## " + heading
	inSection := false
	var out []string
	for _, line := range lines {
		if strings.TrimSpace(line) == target {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(strings.TrimSpace(line), "## ") {
			break
		}
		if inSection {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func runCodexPrompt(ctx context.Context, repoRoot, prompt string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	mgr := adapter.NewCodexSessionManager(adapter.CodexConfig{Command: "codex"})
	sess, err := mgr.StartSession(runCtx, repoRoot)
	if err != nil {
		return "", err
	}
	defer sess.Close()
	return sess.SendPrompt(runCtx, prompt)
}

func writeWorkflowState(repoRoot, runID string, nodes map[string]NodeStatus, details map[string]any) error {
	state := RunState{RunID: runID, Nodes: nodes, Details: details}
	return writeJSONAtomic(filepath.Join(repoRoot, ".workflow", "runs", runID, "state.json"), state)
}

const planningLineageNodeID = "planning"

// writePlanningAttempt 写 planning attempt event（best-effort）。
// attempt 编号语义：0 = initial（agent 第一次跑完），1+ = revision（reject 后重跑）。
// lineage 写入失败不中断主流程。
func writePlanningAttempt(repoRoot, runID, nodeID, sessionID string, attempt int, parentAttempt int, trigger string, failureClass FailureClass, rejectionReason, promptPath, outputPath, exitPath string) error {
	scope := "planning"
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
	var promptDelta *AttemptPromptDelta
	if rejectionReason != "" {
		promptDelta = &AttemptPromptDelta{
			AddedFeedback: "reviewer requested planning revision: " + rejectionReason,
		}
	}
	return writeAttemptEvent(repoRoot, runID, nodeID, NodeKindPlanning, scope, "codex", sessionID, attempt, parentAttempt, trigger, failureClass, string(failureClass), rejectionReason, outcome, promptDelta, promptPath, outputPath, exitPath)
}
