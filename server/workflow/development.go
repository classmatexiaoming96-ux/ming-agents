package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ming-agents/server/adapter"
)

type WorkflowState = RunState

const ConfigSkipInternalReviewEvaluation = "skip_internal_review_evaluation"

type developmentOptions struct {
	SkipInternalReviewEvaluation bool
}

func RunDevelopment(ctx context.Context, repoRoot string, plan *Plan) (state *WorkflowState, err error) {
	return runDevelopment(ctx, repoRoot, plan, developmentOptions{})
}

func RunDevelopmentOnly(ctx context.Context, repoRoot string, plan *Plan) (state *WorkflowState, err error) {
	return runDevelopment(ctx, repoRoot, plan, developmentOptions{SkipInternalReviewEvaluation: true})
}

func runDevelopment(ctx context.Context, repoRoot string, plan *Plan, opts developmentOptions) (state *WorkflowState, err error) {
	if err := validatePlan(plan); err != nil {
		return nil, err
	}
	runID := plan.TaskID
	skipDeferredPhaseStatus := false
	defer func() {
		if err != nil && !skipDeferredPhaseStatus {
			_ = writePhaseStatusAt(repoRoot, runID, &PhaseStatus{
				Phase:      "development",
				GateStatus: "failed",
				NextAction: "retry_generator",
			})
		}
	}()
	nodeDir := filepath.Join(repoRoot, ".workflow", "runs", runID, "node3")
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		return nil, err
	}
	nodeSession := WorkflowNodeSession(repoRoot, runID, "node3")
	_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationStarted})
	agents, err := BuildSubtaskAgents(repoRoot, nodeDir, plan)
	if err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
		return nil, err
	}
	if err := WriteSubtaskAgentManifest(filepath.Join(nodeDir, "agents.json"), agents); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
		return nil, err
	}
	agentsBySubtask := subtaskAgentsByID(agents)
	_ = writeWorkflowState(repoRoot, runID, map[string]NodeStatus{
		"node1": NodeCompleted,
		"node2": NodeCompleted,
		"node3": NodeRunning,
	}, map[string]any{"subtask_agents": filepath.Join(nodeDir, "agents.json")})

	results := runDevelopmentSubtasks(ctx, repoRoot, nodeDir, plan, agentsBySubtask, nil, 0)
	if opts.SkipInternalReviewEvaluation {
		return finishDevelopmentOnly(repoRoot, runID, nodeDir, nodeSession, agents, results)
	}
	report, reviewOut, err := RunReview(ctx, repoRoot, nodeDir, plan, results)
	if err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
		return nil, err
	}
	if !report.Passed {
		retryTargets := blockingRetryTargets(report, results)
		if len(retryTargets) > 0 {
			results = append(results, runDevelopmentSubtasks(ctx, repoRoot, nodeDir, plan, agentsBySubtask, retryTargets, 1)...)
			report, reviewOut, err = RunReview(ctx, repoRoot, nodeDir, plan, results)
			if err != nil {
				_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
				return nil, err
			}
		}
	}
	evalResult, evalErr := RunEvaluation(ctx, repoRoot, runID)
	if evalErr != nil {
		_ = evalErr
	} else if !evalResult.Passed {
		_ = writePhaseStatusAt(repoRoot, runID, &PhaseStatus{
			Phase:        "evaluation",
			GateStatus:   "failed",
			FailureClass: evalResult.FailureClass,
			NextAction:   retryActionFor(evalResult.FailureClass),
			MissingItems: []string{evalResult.RetryAdvice},
		})
		skipDeferredPhaseStatus = true
		return nil, fmt.Errorf("evaluation failed: %s - %s", evalResult.FailureClass, evalResult.RetryAdvice)
	}
	agentsPath := filepath.Join(nodeDir, "agents.json")
	if err := WriteSubtaskAgentManifest(agentsPath, agents); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
		return nil, err
	}

	stateStatus := NodeCompleted
	if !report.Passed {
		stateStatus = NodeFailed
	}
	finalState := &WorkflowState{
		RunID: runID,
		Nodes: map[string]NodeStatus{
			"node1": NodeCompleted,
			"node2": NodeCompleted,
			"node3": stateStatus,
		},
		Details: map[string]any{
			"review_passed":  report.Passed,
			"issue_count":    len(report.Issues),
			"subtask_agents": agentsPath,
		},
	}
	independentEval, independentEvalErr := runIndependentEvaluator(ctx, repoRoot, runID, plan)
	if independentEvalErr != nil {
		_ = independentEvalErr
	} else if independentEval != nil && !independentEval.Passed {
		_ = writePhaseStatusAt(repoRoot, runID, &PhaseStatus{
			Phase:        "evaluation",
			GateStatus:   "failed",
			FailureClass: independentEval.FailureClass,
			NextAction:   retryActionFor(independentEval.FailureClass),
			MissingItems: []string{independentEval.RetryAdvice},
		})
		skipDeferredPhaseStatus = true
		return nil, fmt.Errorf("independent evaluation failed: %s - %s", independentEval.FailureClass, independentEval.RetryAdvice)
	}
	_ = writePhaseStatusAt(repoRoot, runID, &PhaseStatus{
		Phase:      "development",
		GateStatus: phaseGateStatus(report.Passed),
		NextAction: phaseNextAction(report.Passed),
	})
	check, checkErr := checkCompletionAt(repoRoot, runID)
	if checkErr != nil {
		// Completion checks are best-effort unless they produce a structured failed result.
	} else if !check.Passed {
		_ = writePhaseStatusAt(repoRoot, runID, &PhaseStatus{
			Phase:        "development",
			GateStatus:   "failed",
			NextAction:   "retry_generator",
			MissingItems: check.Missing,
		})
		skipDeferredPhaseStatus = true
		return nil, fmt.Errorf("completion check failed: %v", check.Missing)
	}
	outputPath := filepath.Join(repoRoot, "docs", "output.md")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
		return nil, err
	}
	if err := writeTextAtomic(outputPath, renderOutputMarkdown(runID, plan, results, report, reviewOut)); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
		return nil, err
	}
	if err := writeJSONAtomic(filepath.Join(repoRoot, ".workflow", "runs", runID, "state.json"), finalState); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
		return nil, err
	}
	if !report.Passed {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
		return finalState, fmt.Errorf("review found blocking issues")
	}
	_ = writePhaseStatusAt(repoRoot, runID, &PhaseStatus{
		Phase:      "completed",
		GateStatus: "passed",
		NextAction: "finish",
	})
	_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationCompleted})
	return finalState, nil
}

func finishDevelopmentOnly(repoRoot, runID, nodeDir string, nodeSession AgentSession, agents []SubtaskAgent, results []*SubtaskResult) (*WorkflowState, error) {
	agentsPath := filepath.Join(nodeDir, "agents.json")
	if err := WriteSubtaskAgentManifest(agentsPath, agents); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
		return nil, err
	}
	finalState := &WorkflowState{
		RunID: runID,
		Nodes: map[string]NodeStatus{
			"node1": NodeCompleted,
			"node2": NodeCompleted,
			"node3": NodeCompleted,
		},
		Details: map[string]any{
			"subtask_agents":  agentsPath,
			"subtask_results": results,
		},
	}
	_ = writePhaseStatusAt(repoRoot, runID, &PhaseStatus{
		Phase:      "development",
		GateStatus: "passed",
		NextAction: "run_evaluator",
	})
	if err := writeJSONAtomic(filepath.Join(repoRoot, ".workflow", "runs", runID, "state.json"), finalState); err != nil {
		_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationFailed})
		return nil, err
	}
	_ = EmitNodeNotification(nodeSession.ID, NodeNotification{RunID: runID, NodeName: "node3", Status: NotificationCompleted})
	return finalState, nil
}

func subtaskAgentsByID(agents []SubtaskAgent) map[string]*SubtaskAgent {
	byID := make(map[string]*SubtaskAgent, len(agents))
	for i := range agents {
		byID[agents[i].SubtaskID] = &agents[i]
	}
	return byID
}

func runDevelopmentSubtasks(ctx context.Context, repoRoot, nodeDir string, plan *Plan, agents map[string]*SubtaskAgent, only map[string][]ReviewIssue, retry int) []*SubtaskResult {
	var subtasks []Subtask
	for _, st := range plan.Subtasks {
		if only == nil {
			subtasks = append(subtasks, st)
			continue
		}
		if _, ok := only[st.ID]; ok {
			subtasks = append(subtasks, st)
		}
	}
	results := make([]*SubtaskResult, len(subtasks))
	for i, st := range subtasks {
		results[i] = runDevelopmentSubtask(ctx, repoRoot, nodeDir, plan, agents[st.ID], st, i+1, retry, only[st.ID])
	}
	return results
}

func runDevelopmentSubtask(ctx context.Context, repoRoot, nodeDir string, plan *Plan, agent *SubtaskAgent, st Subtask, index, retry int, issues []ReviewIssue) *SubtaskResult {
	suffix := fmt.Sprintf("dev-%d", index)
	if retry > 0 {
		suffix = fmt.Sprintf("dev-%d-r%d", index, retry)
	}
	sessionID := NewPTYSessionID(plan.TaskID, "node3", "codex", index)
	promptFile := filepath.Join(nodeDir, suffix+".prompt.md")
	outFile := filepath.Join(nodeDir, suffix+".out.md")
	exitFile := filepath.Join(nodeDir, suffix+".exit")
	workDir := filepath.Join(repoRoot, filepath.Clean(st.RepoPath))
	if agent != nil {
		sessionID = agent.Session.ID
		workDir = agent.WorkDir
		if retry == 0 {
			promptFile = agent.PromptFile
			outFile = agent.OutFile
			exitFile = agent.ExitFile
		}
		agent.Session.Status = AgentSessionRunning
		_ = EmitNodeNotification(sessionID, NodeNotification{RunID: plan.TaskID, NodeName: "subtask:" + st.ID, Status: NotificationStarted})
	}

	result := &SubtaskResult{Subtask: st, SessionID: sessionID, Agent: agent, OutFile: outFile, ExitFile: exitFile, Status: "completed"}
	execute := func(currentPromptFile, currentOutFile, currentExitFile string, currentIssues []ReviewIssue) {
		prompt := renderDevelopmentPrompt(repoRoot, st, sessionID, plan, currentIssues)
		_ = writeTextAtomic(currentPromptFile, prompt)
		if agent != nil {
			agent.Session.Status = AgentSessionRunning
			_ = AppendAgentMessage(agent, AgentMessage{Role: "system", Content: prompt})
		}

		runCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
		defer cancel()
		manager := adapter.NewCodexSessionManager(adapter.CodexConfig{Command: "codex"})
		session, err := manager.StartSession(runCtx, workDir)
		if err == nil {
			rec := &adapter.PTYSessionRecord{
				SessionID:  sessionID,
				RunID:      plan.TaskID,
				NodeName:   "node_3",
				SubtaskID:  st.ID,
				AgentType:  "codex",
				AdapterKey: "codex",
				WorkDir:    workDir,
				Status:     adapter.PTYSessionStatusRunning,
			}
			rec.AttachIO(session.Reader(), session.WriteInput, session.Resize)
			adapter.DefaultPTYSessionRegistry.Register(rec)
			defer session.Close()
		}
		output := ""
		if err == nil {
			output, err = session.SendPrompt(runCtx, prompt)
		}
		result.Output = output
		result.Err = err
		result.Status = "completed"
		if err != nil {
			result.Status = "failed"
			result.ExitCode = 1
		} else {
			result.ExitCode = 0
		}
		if runCtx.Err() != nil {
			result.Status = "failed"
			result.Err = runCtx.Err()
			result.ExitCode = 1
		}
		if agent != nil {
			if result.Status == "failed" {
				agent.Session.Status = AgentSessionFailed
				adapter.DefaultPTYSessionRegistry.UpdateStatus(sessionID, adapter.PTYSessionStatusFailed)
				_ = EmitNodeNotification(sessionID, NodeNotification{RunID: plan.TaskID, NodeName: "subtask:" + st.ID, Status: NotificationFailed})
			} else {
				agent.Session.Status = AgentSessionCompleted
				adapter.DefaultPTYSessionRegistry.UpdateStatus(sessionID, adapter.PTYSessionStatusCompleted)
				_ = EmitNodeNotification(sessionID, NodeNotification{RunID: plan.TaskID, NodeName: "subtask:" + st.ID, Status: NotificationCompleted})
			}
			_ = AppendAgentMessage(agent, AgentMessage{Role: "assistant", Content: result.Output})
		}
		_ = os.WriteFile(currentOutFile, []byte(output), 0644)
		_ = os.WriteFile(currentExitFile, []byte(fmt.Sprintf("%d\n", result.ExitCode)), 0644)
	}

	execute(promptFile, outFile, exitFile, issues)
	if agent != nil && retry == 0 {
		_ = writeDevelopmentAttempt(repoRoot, plan.TaskID, developmentLineageNodeID, sessionID, st.ID, 0, -1, "initial", FailureClassNone, "", promptFile, outFile, exitFile)
	}
	if agent != nil {
		_, approvalErr := waitForSubtaskApprovalRevisionsAt(ctx, repoRoot, agent, sessionID, "subtask:"+st.ID, st.ID, issues, func(revisionIssues []ReviewIssue, revision int) error {
			result.Err = nil
			agent.Session.Status = AgentRevisionInProgress
			_ = EmitNodeNotification(sessionID, NodeNotification{RunID: plan.TaskID, NodeName: "subtask:" + st.ID, Status: NotificationStarted})
			revisionSuffix := fmt.Sprintf("%s-revision-%d", suffix, revision)
			revPromptFile := filepath.Join(nodeDir, revisionSuffix+".prompt.md")
			revOutFile := filepath.Join(nodeDir, revisionSuffix+".out.md")
			revExitFile := filepath.Join(nodeDir, revisionSuffix+".exit")
			execute(revPromptFile, revOutFile, revExitFile, revisionIssues)
			rejectionReason := ""
			if len(revisionIssues) > 0 {
				rejectionReason = revisionIssues[len(revisionIssues)-1].Description
			}
			_ = writeDevelopmentAttempt(repoRoot, plan.TaskID, developmentLineageNodeID, sessionID, st.ID, revision, revision-1, "human_reject", FailureClassHumanReject, rejectionReason, revPromptFile, revOutFile, revExitFile)
			return nil
		})
		if approvalErr != nil {
			result.Status = "failed"
			result.Err = approvalErr
			result.ExitCode = 1
		}
	}
	return result
}

type subtaskRevisionExecutor func(revisionIssues []ReviewIssue, revision int) error

func waitForSubtaskApprovalRevisions(ctx context.Context, agent *SubtaskAgent, sessionID, nodeName, subtaskID string, issues []ReviewIssue, executeRevision subtaskRevisionExecutor) ([]ReviewIssue, error) {
	return waitForSubtaskApprovalRevisionsAt(ctx, ".", agent, sessionID, nodeName, subtaskID, issues, executeRevision)
}

func waitForSubtaskApprovalRevisionsAt(ctx context.Context, baseDir string, agent *SubtaskAgent, sessionID, nodeName, subtaskID string, issues []ReviewIssue, executeRevision subtaskRevisionExecutor) ([]ReviewIssue, error) {
	const maxRevisions = 3

	revisionIssues := append([]ReviewIssue{}, issues...)
	revisions := 0
	for {
		approvalErr := waitForSubtaskApprovalAt(ctx, baseDir, agent, sessionID, nodeName)
		if approvalErr == nil {
			return revisionIssues, nil
		}
		if !errors.Is(approvalErr, ErrApprovalRejected) {
			return revisionIssues, approvalErr
		}
		if revisions >= maxRevisions {
			return revisionIssues, fmt.Errorf("agent %s exceeded max revision attempts", nodeName)
		}

		revision := "Revision requested by reviewer."
		decision, ok, err := LatestReviewDecision(sessionID, nodeName)
		if err != nil {
			return revisionIssues, err
		}
		if ok && !decision.Approved {
			if reason := strings.TrimSpace(decision.Reason); reason != "" {
				revision = reason
			}
		}
		if err := AppendAgentMessage(agent, AgentMessage{Role: "user", Content: revision}); err != nil {
			return revisionIssues, err
		}
		revisionIssues = append(revisionIssues, ReviewIssue{
			SubtaskID:     subtaskID,
			SessionID:     sessionID,
			Severity:      "blocking",
			Description:   revision,
			RequiredFixes: []string{revision},
		})
		revisions++
		if err := executeRevision(revisionIssues, revisions); err != nil {
			return revisionIssues, err
		}
	}
}

func waitForSubtaskApproval(ctx context.Context, agent *SubtaskAgent, sessionID, nodeName string) error {
	return waitForSubtaskApprovalAt(ctx, ".", agent, sessionID, nodeName)
}

func waitForSubtaskApprovalAt(ctx context.Context, baseDir string, agent *SubtaskAgent, sessionID, nodeName string) error {
	if agent != nil {
		agent.Session.Status = AgentWaitingApproval
	}
	runID := runIDFromSessionID(sessionID)
	if runID != "" {
		_ = writePhaseStatusAt(baseDir, runID, &PhaseStatus{
			Phase:      "approval",
			GateStatus: "waiting_user",
			NextAction: "ask_user",
		})
	}
	if err := WaitForApproval(ctx, sessionID, nodeName); err != nil {
		if errors.Is(err, ErrApprovalRejected) && agent != nil {
			agent.Session.Status = AgentWaitingRevision
		}
		if runID != "" {
			_ = writePhaseStatusAt(baseDir, runID, &PhaseStatus{
				Phase:      "approval",
				GateStatus: "failed",
				NextAction: "retry_generator",
			})
		}
		return err
	}
	if runID != "" {
		_ = writePhaseStatusAt(baseDir, runID, &PhaseStatus{
			Phase:      "approval",
			GateStatus: "passed",
			NextAction: "run_evaluator",
		})
	}
	return nil
}

func renderDevelopmentPrompt(repoRoot string, st Subtask, sessionID string, plan *Plan, issues []ReviewIssue) string {
	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	var b strings.Builder
	b.WriteString("# Role\n")
	b.WriteString("You are a development agent executing one assigned subtask.\n\n")
	b.WriteString("# Repository Scope\n")
	fmt.Fprintf(&b, "Repo root: %s\nAssigned repo_path: %s\ntmux session: %s\n\n", repoRoot, st.RepoPath, sessionID)
	b.WriteString("# Full Task\n")
	b.WriteString(plan.TaskID)
	b.WriteString("\n\n## Plan\n")
	b.Write(planJSON)
	b.WriteString("\n\n## Subtask\n")
	b.WriteString(st.Description)
	b.WriteString("\n\n## Acceptance Criteria\n")
	for _, criterion := range st.AcceptanceCriteria {
		fmt.Fprintf(&b, "- %s\n", criterion)
	}
	if len(issues) > 0 {
		b.WriteString("\n## Review Feedback To Fix\n")
		for _, issue := range issues {
			fmt.Fprintf(&b, "- %s: %s\n", issue.Severity, issue.Description)
			for _, fix := range issue.RequiredFixes {
				fmt.Fprintf(&b, "  - %s\n", fix)
			}
		}
	}
	b.WriteString("\n# Constraints\n")
	b.WriteString("- Work only on files required for this subtask.\n")
	b.WriteString("- Do not revert unrelated user or agent changes.\n")
	b.WriteString("- Add or update focused tests when behavior changes.\n")
	b.WriteString("- At completion, summarize changed files, tests run, and any blocked criteria.\n")
	return b.String()
}

func RunReview(ctx context.Context, repoRoot, nodeDir string, plan *Plan, results []*SubtaskResult) (*ReviewReport, string, error) {
	diffFile, _, err := writeReviewInputs(repoRoot, nodeDir, plan, results)
	if err != nil {
		return nil, "", err
	}
	prompt := renderReviewPrompt(plan, results, diffFile)
	promptFile := filepath.Join(nodeDir, "review.prompt.md")
	outFile := filepath.Join(nodeDir, "review.out.md")
	exitFile := filepath.Join(nodeDir, "review.exit")
	_ = writeTextAtomic(promptFile, prompt)
	output, err := runCodexPrompt(ctx, repoRoot, prompt, 30*time.Minute)
	exitCode := 0
	if err != nil {
		exitCode = 1
		output = err.Error()
	}
	_ = os.WriteFile(outFile, []byte(output), 0644)
	_ = os.WriteFile(exitFile, []byte(fmt.Sprintf("%d\n", exitCode)), 0644)
	report := ParseReviewReport(output)
	if err != nil {
		report.Passed = false
		report.Issues = append(report.Issues, ReviewIssue{Severity: "blocking", Description: err.Error()})
	}
	_ = writePhaseStatusAt(repoRoot, plan.TaskID, &PhaseStatus{
		Phase:      "evaluation",
		GateStatus: phaseGateStatus(report.Passed),
		NextAction: phaseNextAction(report.Passed),
	})
	return report, output, nil
}

func phaseGateStatus(passed bool) string {
	if passed {
		return "passed"
	}
	return "failed"
}

func phaseNextAction(passed bool) string {
	if passed {
		return "finish"
	}
	return "retry_generator"
}

func retryActionFor(fc FailureClass) string {
	switch fc {
	case FailureClassEnvironmentBlock, FailureClassValidatorIssue:
		return "fix_environment"
	case FailureClassProductDefect:
		return "retry_generator"
	case FailureClassTransient:
		return "retry_evaluation"
	default:
		return "ask_user"
	}
}

func runIndependentEvaluator(ctx context.Context, repoRoot, runID string, plan *Plan) (*EvaluationResult, error) {
	artifacts := adapter.ArtifactIndex{
		PlanPath:    filepath.Join(repoRoot, "docs", "planning.md"),
		OutputPath:  filepath.Join(repoRoot, "docs", "output.md"),
		CodeDirs:    findCodeDirs(repoRoot, runID),
		EvidenceDir: filepath.Join(repoRoot, ".workflow", "runs", runID),
	}
	runner := adapter.NewVerificationRunner(adapter.NewCodexSessionManager(adapter.CodexConfig{Command: "codex"}))
	result, err := runner.Run(ctx, runID, verificationPlanFromPlan(plan), artifacts)
	if err != nil {
		return nil, err
	}
	evalResult := evaluationResultFromVerification(result)
	if err := writeEvaluationResult(repoRoot, runID, evalResult); err != nil {
		_ = err
	}
	return evalResult, nil
}

func verificationPlanFromPlan(plan *Plan) adapter.VerificationPlan {
	if plan == nil {
		return adapter.VerificationPlan{}
	}
	out := adapter.VerificationPlan{TaskID: plan.TaskID}
	for _, st := range plan.Subtasks {
		out.Subtasks = append(out.Subtasks, adapter.VerificationSubtask{
			ID:                 st.ID,
			Description:        st.Description,
			AcceptanceCriteria: append([]string(nil), st.AcceptanceCriteria...),
		})
	}
	return out
}

func findCodeDirs(repoRoot, runID string) []string {
	runDir := filepath.Join(repoRoot, ".workflow", "runs", runID)
	entries, _ := os.ReadDir(runDir)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && (e.Name() == "code" || e.Name() == "src" || e.Name() == "changes") {
			dirs = append(dirs, filepath.Join(runDir, e.Name()))
		}
	}
	return dirs
}

func blockingRetryTargets(report *ReviewReport, results []*SubtaskResult) map[string][]ReviewIssue {
	targets := map[string][]ReviewIssue{}
	if report == nil {
		return targets
	}
	bySession := map[string]string{}
	for _, result := range results {
		if result != nil {
			bySession[result.SessionID] = result.Subtask.ID
		}
	}
	fallback := ""
	if len(results) > 0 && results[0] != nil {
		fallback = results[0].Subtask.ID
	}
	for _, issue := range report.Issues {
		if normalizeSeverity(issue.Severity) != "blocking" {
			continue
		}
		target := issue.SubtaskID
		if target == "" && issue.SessionID != "" {
			target = bySession[issue.SessionID]
		}
		if target == "" {
			target = fallback
		}
		if target != "" {
			targets[target] = append(targets[target], issue)
		}
	}
	return targets
}

func renderOutputMarkdown(runID string, plan *Plan, results []*SubtaskResult, report *ReviewReport, reviewOut string) string {
	var b strings.Builder
	b.WriteString("# Workflow Output\n\n")
	fmt.Fprintf(&b, "run_id: %s\nnode: 3\nstate: %s\n\n", runID, outputState(report))
	b.WriteString("## Development Results\n\n")
	for _, result := range results {
		if result == nil {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", result.Subtask.ID)
		fmt.Fprintf(&b, "session: %s\nstatus: %s\nexit_code: %d\nout_file: %s\n\n", result.SessionID, result.Status, result.ExitCode, filepath.ToSlash(result.OutFile))
	}
	b.WriteString("## Review Summary\n\n")
	if report != nil && report.Summary != "" {
		b.WriteString(report.Summary)
		b.WriteString("\n\n")
	}
	if report != nil {
		fmt.Fprintf(&b, "passed: %t\nissues: %d\n\n", report.Passed, len(report.Issues))
	}
	if strings.TrimSpace(reviewOut) != "" {
		b.WriteString("## Review Output\n\n")
		b.WriteString(strings.TrimSpace(reviewOut))
		b.WriteString("\n")
	}
	_ = plan
	return b.String()
}

func outputState(report *ReviewReport) NodeStatus {
	if report != nil && report.Passed {
		return NodeCompleted
	}
	return NodeFailed
}

// developmentLineageNodeID is the node ID used for development attempt lineage.
const developmentLineageNodeID = "development"

// writeDevelopmentAttempt 写 development subtask attempt event（best-effort）。
// attempt 编号语义：0 = initial（agent 第一次跑完），1+ = revision（reject 后重跑）。
// lineage 写入失败不中断主流程。
func writeDevelopmentAttempt(repoRoot, runID, nodeID, sessionID, subtaskID string, attempt int, parentAttempt int, trigger string, failureClass FailureClass, rejectionReason, promptPath, outputPath, exitPath string) error {
	scope := "subtask:" + subtaskID
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
			AddedFeedback: "reviewer requested development revision: " + rejectionReason,
		}
	}
	return writeAttemptEvent(repoRoot, runID, nodeID, NodeKindDevelopment, scope, "codex", sessionID, attempt, parentAttempt, trigger, failureClass, string(failureClass), rejectionReason, outcome, promptDelta, promptPath, outputPath, exitPath)
}
