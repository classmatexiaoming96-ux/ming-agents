package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ming-agents/server/adapter"
)

func TestValidatePlanRejectsInvalidPlans(t *testing.T) {
	tests := []struct {
		name string
		plan *Plan
		want string
	}{
		{
			name: "missing task id",
			plan: &Plan{Subtasks: []Subtask{validSubtask("one")}},
			want: "task_id is required",
		},
		{
			name: "duplicate subtask id",
			plan: &Plan{TaskID: "task", Subtasks: []Subtask{validSubtask("one"), validSubtask("one")}},
			want: "duplicate subtask id",
		},
		{
			name: "unsupported agent",
			plan: &Plan{TaskID: "task", Subtasks: []Subtask{{
				ID: "one", AgentType: "claude-code", RepoPath: "workflow", Description: "edit workflow files", AcceptanceCriteria: []string{"build passes"},
			}}},
			want: "unsupported agent_type",
		},
		{
			name: "absolute repo path",
			plan: &Plan{TaskID: "task", Subtasks: []Subtask{{
				ID: "one", AgentType: "codex", RepoPath: "/tmp", Description: "edit workflow files", AcceptanceCriteria: []string{"build passes"},
			}}},
			want: "invalid repo_path",
		},
		{
			name: "empty acceptance criteria",
			plan: &Plan{TaskID: "task", Subtasks: []Subtask{{
				ID: "one", AgentType: "codex", RepoPath: "workflow", Description: "edit workflow files",
			}}},
			want: "acceptance criteria",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePlan(tt.plan)
			if err == nil {
				t.Fatalf("validatePlan() error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validatePlan() error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidatePlanAcceptsValidPlan(t *testing.T) {
	plan := &Plan{TaskID: "task", Subtasks: []Subtask{validSubtask("one")}}
	if err := validatePlan(plan); err != nil {
		t.Fatalf("validatePlan() error = %v", err)
	}
}

func TestPlanningRollbackRetriesContractErrorOutput(t *testing.T) {
	repoRoot := t.TempDir()
	commandDir := t.TempDir()
	countPath := filepath.Join(commandDir, "count")
	codexPath := filepath.Join(commandDir, "codex")
	script := fmt.Sprintf(`#!/bin/sh
printf 'OpenAI Codex\n› '
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      count=0
      if [ -f %[1]q ]; then
        count=$(cat %[1]q)
      fi
      count=$((count + 1))
      printf '%%s\n' "$count" > %[1]q
      marker=$(printf '%%s' "$line" | tr -d '"' | sed 's/ + //g')
      if [ "$count" -eq 1 ]; then
        printf 'planning output without json\n'
      else
        printf '## Execution Plan JSON\n\n'
        printf '\140\140\140json\n'
        printf '{"task_id":"run-planning-contract","subtasks":[{"id":"api","agent_type":"codex","repo_path":"server","description":"fix api","acceptance_criteria":["tests pass"]}]}\n'
        printf '\140\140\140\n'
      fi
      printf '%%s\n' "$marker"
      ;;
  esac
done
`, countPath)
	if err := os.WriteFile(codexPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", commandDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	clarFile := filepath.Join(repoRoot, "docs", "requirements-clarity.md")
	if err := os.MkdirAll(filepath.Dir(clarFile), 0755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	clarification := `# Requirements Clarity

run_id: run-planning-contract

## Agent Result: codex

<!-- agent:codex begin -->
Build the API.
<!-- agent:codex end -->
`
	if err := os.WriteFile(clarFile, []byte(clarification), 0644); err != nil {
		t.Fatalf("write clarification: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	approvalDone := make(chan error, 1)
	go func() {
		sessionID := NewPTYSessionID("run-planning-contract", "node2", "codex", 1)
		historyFile := filepath.Join(repoRoot, ".workflow", "runs", "run-planning-contract", "node2", "codex.messages.jsonl")
		waitForHistoryRoleCount(t, historyFile, "approval_request", 1)
		approvalDone <- ApproveSession(sessionID, "node2:codex", "approved")
	}()

	plan, err := RunPlanning(ctx, repoRoot, clarFile)
	if err != nil {
		t.Fatalf("RunPlanning() error = %v", err)
	}
	if err := <-approvalDone; err != nil {
		t.Fatalf("approval error = %v", err)
	}
	if plan.TaskID != "run-planning-contract" {
		t.Fatalf("TaskID = %q, want run-planning-contract", plan.TaskID)
	}
	if len(plan.Subtasks) != 1 || plan.Subtasks[0].ID != "api" {
		t.Fatalf("Subtasks = %#v, want api subtask", plan.Subtasks)
	}
}

func TestParseReviewReportDetectsBlockingIssues(t *testing.T) {
	md := `# Review

## Summary
Two criteria are not satisfied yet.

## Issues
- severity: blocking
  subtask_id: one
  session_id: pty-run-node3-codex-1
  failure_class: product_defect
  evidence_refs: test.log, build.log
  description: Missing retry output in docs/output.md.
  required_fixes:
  - Add the retry summary.
- severity: warning
  description: Tests are light.
`

	report := ParseReviewReport(md)
	if report.Passed {
		t.Fatal("ParseReviewReport() Passed = true, want false")
	}
	if report.Summary != "Two criteria are not satisfied yet." {
		t.Fatalf("Summary = %q", report.Summary)
	}
	if len(report.Issues) != 2 {
		t.Fatalf("len(Issues) = %d, want 2", len(report.Issues))
	}
	if report.Issues[0].Severity != "blocking" || report.Issues[0].SubtaskID != "one" {
		t.Fatalf("first issue = %+v", report.Issues[0])
	}
	if len(report.Issues[0].RequiredFixes) != 1 {
		t.Fatalf("first issue fixes = %#v", report.Issues[0].RequiredFixes)
	}
	if report.Issues[0].FailureClass != FailureClassProductDefect {
		t.Fatalf("first issue failure class = %q", report.Issues[0].FailureClass)
	}
	if got := report.Issues[0].EvidenceRefs; len(got) != 2 || got[0] != "test.log" || got[1] != "build.log" {
		t.Fatalf("first issue evidence refs = %#v", got)
	}
}

func TestWriteJSONAtomicWritesJSONAndRemovesTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	state := RunState{RunID: "run", Nodes: map[string]NodeStatus{"node1": NodeCompleted}}

	if err := writeJSONAtomic(target, state); err != nil {
		t.Fatalf("writeJSONAtomic() error = %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), `"run_id": "run"`) {
		t.Fatalf("state file missing run_id: %s", data)
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file stat error = %v, want not exist", err)
	}
}

func TestBuildSubtaskAgentsCreatesDedicatedSessions(t *testing.T) {
	plan := &Plan{
		TaskID: "run-1",
		Subtasks: []Subtask{
			validSubtask("api"),
			validSubtask("ui"),
		},
	}
	agents, err := BuildSubtaskAgents("/repo", "/repo/.workflow/runs/run-1/node3", plan)
	if err != nil {
		t.Fatalf("BuildSubtaskAgents() error = %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("len(agents) = %d, want 2", len(agents))
	}
	if agents[0].SubtaskID != "api" || agents[1].SubtaskID != "ui" {
		t.Fatalf("unexpected subtask ids: %+v", agents)
	}
	if agents[0].Session.ID == "" || agents[0].Session.ID == agents[1].Session.ID {
		t.Fatalf("sessions not unique: %+v", agents)
	}
	if !strings.Contains(agents[0].Session.HistoryFile, "api.messages.jsonl") {
		t.Fatalf("history file = %q, want api.messages.jsonl", agents[0].Session.HistoryFile)
	}
	if agents[0].Context["repo_path"] != "workflow" {
		t.Fatalf("context repo_path = %q", agents[0].Context["repo_path"])
	}
}

func TestRouteSubtaskMessageByExplicitAndMention(t *testing.T) {
	agents := []SubtaskAgent{
		{
			SubtaskID: "api",
			Session:   AgentSession{ID: "session-api"},
		},
		{
			SubtaskID: "ui",
			Session:   AgentSession{ID: "session-ui"},
		},
	}
	byID, err := RouteSubtaskMessage(agents, SubtaskMessage{SubtaskID: "ui", Content: "status?"})
	if err != nil {
		t.Fatalf("RouteSubtaskMessage(explicit) error = %v", err)
	}
	if byID.SubtaskID != "ui" {
		t.Fatalf("explicit route = %s, want ui", byID.SubtaskID)
	}
	byID.Session.Messages = append(byID.Session.Messages, AgentMessage{Role: "user", Content: "ui"})
	if len(agents[1].Session.Messages) != 1 {
		t.Fatal("routed agent did not reference original session state")
	}
	byMention, err := RouteSubtaskMessage(agents, SubtaskMessage{Content: "api 这个子任务进展如何"})
	if err != nil {
		t.Fatalf("RouteSubtaskMessage(mention) error = %v", err)
	}
	if byMention.Session.ID != "session-api" {
		t.Fatalf("mention route session = %s, want session-api", byMention.Session.ID)
	}
	if _, err := RouteSubtaskMessage(agents, SubtaskMessage{Content: "进展如何"}); err == nil {
		t.Fatal("RouteSubtaskMessage(ambiguous) error = nil, want error")
	}
}

func TestAppendAgentMessagePersistsHistory(t *testing.T) {
	dir := t.TempDir()
	agent := &SubtaskAgent{
		SubtaskID: "api",
		Session: AgentSession{
			ID:          "session-api",
			HistoryFile: filepath.Join(dir, "api.messages.jsonl"),
		},
	}
	msg := AgentMessage{Role: "user", Content: "看一下 api 子任务"}
	if err := AppendAgentMessage(agent, msg); err != nil {
		t.Fatalf("AppendAgentMessage() error = %v", err)
	}
	if len(agent.Session.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(agent.Session.Messages))
	}
	data, err := os.ReadFile(agent.Session.HistoryFile)
	if err != nil {
		t.Fatalf("ReadFile(history) error = %v", err)
	}
	var persisted AgentMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &persisted); err != nil {
		t.Fatalf("unmarshal persisted message: %v", err)
	}
	if persisted.Role != "user" || persisted.Content != msg.Content {
		t.Fatalf("persisted = %+v", persisted)
	}
}

func TestEmitNodeNotificationPersistsLifecycleEvent(t *testing.T) {
	dir := t.TempDir()
	sessionID := "session-node1"
	history := filepath.Join(dir, "node1.messages.jsonl")
	RegisterAgentSession(AgentSession{
		ID:          sessionID,
		AgentType:   "workflow",
		Status:      AgentSessionRunning,
		HistoryFile: history,
	})

	notification := NodeNotification{
		RunID:    "run-1",
		NodeName: "node1",
		Status:   NotificationStarted,
	}
	if err := EmitNodeNotification(sessionID, notification); err != nil {
		t.Fatalf("EmitNodeNotification() error = %v", err)
	}

	messages := readHistoryMessages(t, history)
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	if messages[0].Role != "notification" {
		t.Fatalf("role = %q, want notification", messages[0].Role)
	}
	if !strings.Contains(messages[0].Content, `"status":"STARTED"`) {
		t.Fatalf("notification content = %s", messages[0].Content)
	}
}

func TestWaitForApprovalWritesRequestAndUnblocksOnApproval(t *testing.T) {
	dir := t.TempDir()
	sessionID := "session-node2"
	history := filepath.Join(dir, "node2.messages.jsonl")
	RegisterAgentSession(AgentSession{
		ID:          sessionID,
		AgentType:   "workflow",
		Status:      AgentSessionRunning,
		HistoryFile: history,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- WaitForApproval(ctx, sessionID, "node2")
	}()

	waitForHistoryRole(t, history, "approval_request")
	if err := ApproveSession(sessionID, "node2", "approved by test"); err != nil {
		t.Fatalf("ApproveSession() error = %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForApproval() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("WaitForApproval() did not unblock")
	}
	messages := readHistoryMessages(t, history)
	if messages[len(messages)-1].Role != "approval" {
		t.Fatalf("last role = %q, want approval", messages[len(messages)-1].Role)
	}
}

func TestWaitForApprovalAcceptsDecisionWrittenBeforeRequest(t *testing.T) {
	dir := t.TempDir()
	sessionID := "session-preapproved"
	history := filepath.Join(dir, "preapproved.messages.jsonl")
	RegisterAgentSession(AgentSession{
		ID:          sessionID,
		AgentType:   "codex",
		Status:      AgentSessionRunning,
		HistoryFile: history,
	})
	if err := ApproveSession(sessionID, "node2:codex", "approved before request"); err != nil {
		t.Fatalf("ApproveSession() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := WaitForApproval(ctx, sessionID, "node2:codex"); err != nil {
		t.Fatalf("WaitForApproval() error = %v", err)
	}
}

func TestLatestReviewDecisionIgnoresOtherAgentDecisionsForSameNode(t *testing.T) {
	dir := t.TempDir()
	historyOne := filepath.Join(dir, "agent-one.messages.jsonl")
	historyTwo := filepath.Join(dir, "agent-two.messages.jsonl")
	RegisterAgentSession(AgentSession{
		ID:          "agent-one",
		AgentType:   "codex",
		Status:      AgentWaitingApproval,
		HistoryFile: historyOne,
	})
	RegisterAgentSession(AgentSession{
		ID:          "agent-two",
		AgentType:   "codex",
		Status:      AgentWaitingApproval,
		HistoryFile: historyTwo,
	})

	requestOne, _ := json.Marshal(ApprovalRequest{SessionID: "agent-one", NodeName: "node3", Status: "WAITING"})
	requestTwo, _ := json.Marshal(ApprovalRequest{SessionID: "agent-two", NodeName: "node3", Status: "WAITING"})
	rejectOne, _ := json.Marshal(ReviewDecision{
		Approved:    false,
		Reason:      "agent one needs revision",
		RejectType:  RejectTypeReviseSubtask,
		ResumePoint: "node3:one",
		SessionID:   "agent-one",
		NodeName:    "node3",
	})
	if err := AppendAgentMessage(&SubtaskAgent{Session: AgentSession{ID: "agent-one", HistoryFile: historyOne}}, AgentMessage{Role: "approval_request", Content: string(requestOne)}); err != nil {
		t.Fatalf("append request one: %v", err)
	}
	if err := AppendAgentMessage(&SubtaskAgent{Session: AgentSession{ID: "agent-one", HistoryFile: historyOne}}, AgentMessage{Role: "rejection", Content: string(rejectOne)}); err != nil {
		t.Fatalf("append rejection one: %v", err)
	}
	if err := AppendAgentMessage(&SubtaskAgent{Session: AgentSession{ID: "agent-two", HistoryFile: historyTwo}}, AgentMessage{Role: "approval_request", Content: string(requestTwo)}); err != nil {
		t.Fatalf("append request two: %v", err)
	}
	if err := AppendAgentMessage(&SubtaskAgent{Session: AgentSession{ID: "agent-two", HistoryFile: historyTwo}}, AgentMessage{Role: "rejection", Content: string(rejectOne)}); err != nil {
		t.Fatalf("append cross-agent rejection: %v", err)
	}

	if _, ok, err := LatestReviewDecision("agent-two", "node3"); err != nil {
		t.Fatalf("LatestReviewDecision(agent-two) error = %v", err)
	} else if ok {
		t.Fatal("LatestReviewDecision(agent-two) accepted rejection for agent-one")
	}
	decision, ok, err := LatestReviewDecision("agent-one", "node3")
	if err != nil {
		t.Fatalf("LatestReviewDecision(agent-one) error = %v", err)
	}
	if !ok || decision.SessionID != "agent-one" || decision.Approved {
		t.Fatalf("LatestReviewDecision(agent-one) = %+v, %v; want rejection for agent-one", decision, ok)
	}
}

func TestWaitForPlanningAgentApprovalRejectsAndRerunsUntilApproved(t *testing.T) {
	dir := t.TempDir()
	run := planningAgentRun{
		RunID:       "run-planning-revision",
		AgentID:     "node2:codex",
		AgentType:   "codex",
		SessionID:   "planning-agent-session",
		Prompt:      "initial planning prompt",
		PromptFile:  filepath.Join(dir, "codex.prompt.md"),
		OutFile:     filepath.Join(dir, "codex.out.md"),
		ExitFile:    filepath.Join(dir, "codex.exit"),
		HistoryFile: filepath.Join(dir, "codex.messages.jsonl"),
		Session: AgentSession{
			ID:          "planning-agent-session",
			AgentType:   "codex",
			Status:      AgentSessionPending,
			HistoryFile: filepath.Join(dir, "codex.messages.jsonl"),
		},
	}
	out := planningAgentOutput{
		AgentType:   run.AgentType,
		SessionID:   run.SessionID,
		Status:      "completed",
		Output:      "first output",
		PromptFile:  run.PromptFile,
		OutFile:     run.OutFile,
		ExitFile:    run.ExitFile,
		HistoryFile: run.HistoryFile,
		Session:     run.Session,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	approverDone := make(chan error, 1)
	go func() {
		waitForHistoryRoleCount(t, run.HistoryFile, "approval_request", 1)
		if err := RejectSession(run.SessionID, run.AgentID, ReviewDecision{
			Reason:     "tighten the plan",
			RejectType: RejectTypeReplan,
		}); err != nil {
			approverDone <- err
			return
		}
		waitForHistoryRoleCount(t, run.HistoryFile, "approval_request", 2)
		approverDone <- ApproveSession(run.SessionID, run.AgentID, "approved after revision")
	}()

	executions := 0
	err := waitForPlanningAgentApproval(ctx, "/repo", &out, run, func(ctx context.Context, repoRoot string, next planningAgentRun) planningAgentOutput {
		executions++
		if !strings.Contains(next.Prompt, "tighten the plan") {
			return planningAgentOutput{Err: errors.New("revision prompt missing reviewer feedback"), Status: "failed"}
		}
		next.Session = run.Session
		return planningAgentOutput{
			AgentType:   next.AgentType,
			SessionID:   next.SessionID,
			Status:      "completed",
			Output:      "revised output",
			PromptFile:  next.PromptFile,
			OutFile:     next.OutFile,
			ExitFile:    next.ExitFile,
			HistoryFile: next.HistoryFile,
			Session:     next.Session,
		}
	})
	if err != nil {
		t.Fatalf("waitForPlanningAgentApproval() error = %v", err)
	}
	if err := <-approverDone; err != nil {
		t.Fatalf("approval goroutine error = %v", err)
	}
	if executions != 1 {
		t.Fatalf("executions = %d, want 1 revision rerun", executions)
	}
	if out.Output != "revised output" {
		t.Fatalf("out.Output = %q, want revised output", out.Output)
	}
	messages := readHistoryMessages(t, run.HistoryFile)
	var approvals, revisions int
	for _, msg := range messages {
		if msg.Role == "approval_request" {
			approvals++
		}
		if msg.Role == "user" && strings.Contains(msg.Content, "tighten the plan") {
			revisions++
		}
	}
	if approvals != 2 {
		t.Fatalf("approval requests = %d, want 2", approvals)
	}
	if revisions != 1 {
		t.Fatalf("revision messages = %d, want 1", revisions)
	}
}

func TestWaitForPlanningAgentApprovalExceedsMaxRevisions(t *testing.T) {
	dir := t.TempDir()
	run := planningAgentRun{
		RunID:       "run-planning-max-revisions",
		AgentID:     "node2:codex",
		AgentType:   "codex",
		SessionID:   "planning-agent-session-max",
		Prompt:      "initial planning prompt",
		PromptFile:  filepath.Join(dir, "codex.prompt.md"),
		OutFile:     filepath.Join(dir, "codex.out.md"),
		ExitFile:    filepath.Join(dir, "codex.exit"),
		HistoryFile: filepath.Join(dir, "codex.messages.jsonl"),
		Session: AgentSession{
			ID:          "planning-agent-session-max",
			AgentType:   "codex",
			Status:      AgentSessionPending,
			HistoryFile: filepath.Join(dir, "codex.messages.jsonl"),
		},
	}
	out := planningAgentOutput{
		AgentType:   run.AgentType,
		SessionID:   run.SessionID,
		Status:      "completed",
		Output:      "first output",
		PromptFile:  run.PromptFile,
		OutFile:     run.OutFile,
		ExitFile:    run.ExitFile,
		HistoryFile: run.HistoryFile,
		Session:     run.Session,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	approverDone := make(chan error, 1)
	go func() {
		for i := 1; i <= 4; i++ {
			waitForHistoryRoleCount(t, run.HistoryFile, "approval_request", i)
			if err := RejectSession(run.SessionID, run.AgentID, ReviewDecision{
				Reason:     "revise attempt",
				RejectType: RejectTypeReplan,
			}); err != nil {
				approverDone <- err
				return
			}
		}
		approverDone <- nil
	}()

	executions := 0
	err := waitForPlanningAgentApproval(ctx, "/repo", &out, run, func(ctx context.Context, repoRoot string, next planningAgentRun) planningAgentOutput {
		executions++
		next.Session = run.Session
		return planningAgentOutput{
			AgentType:   next.AgentType,
			SessionID:   next.SessionID,
			Status:      "completed",
			Output:      "revised output",
			PromptFile:  next.PromptFile,
			OutFile:     next.OutFile,
			ExitFile:    next.ExitFile,
			HistoryFile: next.HistoryFile,
			Session:     next.Session,
		}
	})
	if err == nil {
		t.Fatal("waitForPlanningAgentApproval() error = nil, want max revision error")
	}
	if !strings.Contains(err.Error(), "exceeded max revision attempts") {
		t.Fatalf("waitForPlanningAgentApproval() error = %v, want max revision attempts", err)
	}
	if err := <-approverDone; err != nil {
		t.Fatalf("approval goroutine error = %v", err)
	}
	if executions != 3 {
		t.Fatalf("executions = %d, want 3 revisions", executions)
	}
}

func TestWaitForAgentApprovalRejectsAndRerunsUntilApproved(t *testing.T) {
	dir := t.TempDir()
	run := clarificationRun{
		AgentType:   "codex",
		SessionID:   "clarification-agent-session",
		Prompt:      "initial clarification prompt",
		PromptFile:  filepath.Join(dir, "codex.prompt.md"),
		OutFile:     filepath.Join(dir, "codex.out.md"),
		ExitFile:    filepath.Join(dir, "codex.exit"),
		HistoryFile: filepath.Join(dir, "codex.messages.jsonl"),
		Session: AgentSession{
			ID:          "clarification-agent-session",
			AgentType:   "codex",
			Status:      AgentSessionPending,
			HistoryFile: filepath.Join(dir, "codex.messages.jsonl"),
		},
	}
	RegisterAgentSession(run.Session)
	out := clarificationOutput{
		AgentType:   run.AgentType,
		SessionID:   run.SessionID,
		Status:      "completed",
		Output:      "first clarification",
		PromptFile:  run.PromptFile,
		OutFile:     run.OutFile,
		ExitFile:    run.ExitFile,
		HistoryFile: run.HistoryFile,
		Session:     run.Session,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	approverDone := make(chan error, 1)
	agentNodeName := "node1:codex"
	go func() {
		waitForHistoryRoleCount(t, run.HistoryFile, "approval_request", 1)
		if err := RejectSession(run.SessionID, agentNodeName, ReviewDecision{
			Reason:     "clarify deployment constraints",
			RejectType: RejectTypeReplan,
		}); err != nil {
			approverDone <- err
			return
		}
		waitForHistoryRoleCount(t, run.HistoryFile, "approval_request", 2)
		approverDone <- ApproveSession(run.SessionID, agentNodeName, "approved after revision")
	}()

	executions := 0
	err := waitForAgentApproval(ctx, "/repo", &out, run, agentNodeName, func(ctx context.Context, repoRoot string, next clarificationRun) clarificationOutput {
		executions++
		if !strings.Contains(next.Prompt, "initial clarification prompt") {
			return clarificationOutput{Err: errors.New("revision prompt lost original prompt"), Status: "failed"}
		}
		if !strings.Contains(next.Prompt, "clarify deployment constraints") {
			return clarificationOutput{Err: errors.New("revision prompt missing reviewer feedback"), Status: "failed"}
		}
		next.Session = run.Session
		return clarificationOutput{
			AgentType:   next.AgentType,
			SessionID:   next.SessionID,
			Status:      "completed",
			Output:      "revised clarification",
			PromptFile:  next.PromptFile,
			OutFile:     next.OutFile,
			ExitFile:    next.ExitFile,
			HistoryFile: next.HistoryFile,
			Session:     next.Session,
		}
	})
	if err != nil {
		t.Fatalf("waitForAgentApproval() error = %v", err)
	}
	if err := <-approverDone; err != nil {
		t.Fatalf("approval goroutine error = %v", err)
	}
	if executions != 1 {
		t.Fatalf("executions = %d, want 1 revision rerun", executions)
	}
	if out.Output != "revised clarification" {
		t.Fatalf("out.Output = %q, want revised clarification", out.Output)
	}
	messages := readHistoryMessages(t, run.HistoryFile)
	var approvals, revisions int
	for _, msg := range messages {
		if msg.Role == "approval_request" {
			approvals++
		}
		if msg.Role == "user" && strings.Contains(msg.Content, "clarify deployment constraints") {
			revisions++
		}
	}
	if approvals != 2 {
		t.Fatalf("approval requests = %d, want 2", approvals)
	}
	if revisions != 1 {
		t.Fatalf("revision messages = %d, want 1", revisions)
	}
}

func TestClarificationRevisionWritesLineage(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "20260630-120000"
	sessionID := NewPTYSessionID(runID, "node1", "codex", 1)
	run := clarificationRun{
		AgentType:   "codex",
		SessionID:   sessionID,
		Prompt:      "initial clarification prompt",
		PromptFile:  filepath.Join(tmpDir, "codex.prompt.md"),
		OutFile:     filepath.Join(tmpDir, "codex.out.md"),
		ExitFile:    filepath.Join(tmpDir, "codex.exit"),
		HistoryFile: filepath.Join(tmpDir, "codex.messages.jsonl"),
		Session: AgentSession{
			ID:          sessionID,
			AgentType:   "codex",
			Status:      AgentSessionPending,
			HistoryFile: filepath.Join(tmpDir, "codex.messages.jsonl"),
		},
	}
	RegisterAgentSession(run.Session)
	out := clarificationOutput{
		AgentType:   run.AgentType,
		SessionID:   run.SessionID,
		Status:      "completed",
		Output:      "first clarification",
		PromptFile:  run.PromptFile,
		OutFile:     run.OutFile,
		ExitFile:    run.ExitFile,
		HistoryFile: run.HistoryFile,
		Session:     run.Session,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	approverDone := make(chan error, 1)
	agentNodeName := "node1:codex"
	go func() {
		waitForHistoryRoleCount(t, run.HistoryFile, "approval_request", 1)
		if err := RejectSession(run.SessionID, agentNodeName, ReviewDecision{
			Reason:     "clarify deployment constraints",
			RejectType: RejectTypeReplan,
		}); err != nil {
			approverDone <- err
			return
		}
		waitForHistoryRoleCount(t, run.HistoryFile, "approval_request", 2)
		approverDone <- ApproveSession(run.SessionID, agentNodeName, "approved after revision")
	}()

	err := waitForAgentApproval(ctx, tmpDir, &out, run, agentNodeName, func(ctx context.Context, repoRoot string, next clarificationRun) clarificationOutput {
		next.Session = run.Session
		return clarificationOutput{
			AgentType:   next.AgentType,
			SessionID:   next.SessionID,
			Status:      "completed",
			Output:      "revised clarification",
			PromptFile:  next.PromptFile,
			OutFile:     next.OutFile,
			ExitFile:    next.ExitFile,
			HistoryFile: next.HistoryFile,
			Session:     next.Session,
		}
	})
	if err != nil {
		t.Fatalf("waitForAgentApproval() error = %v", err)
	}
	if err := <-approverDone; err != nil {
		t.Fatalf("approval goroutine error = %v", err)
	}

	nodePath := filepath.Join(tmpDir, ".workflow", "runs", runID, "clarification", "attempts.jsonl")
	if _, err := os.Stat(nodePath); err != nil {
		t.Fatalf("attempts.jsonl not created: %v", err)
	}

	events, err := ReadAttemptEvents(tmpDir, runID, "clarification")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 {
		t.Fatalf("expected >= 2 events, got %d", len(events))
	}
	if events[0].Attempt != 0 {
		t.Errorf("first event attempt = %d, want 0", events[0].Attempt)
	}
	if events[0].NodeKind != NodeKindClarification {
		t.Errorf("first event node_kind = %q, want %q", events[0].NodeKind, NodeKindClarification)
	}
	if events[0].Scope != "agent:codex" {
		t.Errorf("first event scope = %q, want agent:codex", events[0].Scope)
	}

	var foundReject bool
	for _, e := range events {
		if e.FailureClass == FailureClassHumanReject && e.RejectionReason != "" {
			foundReject = true
			if e.Attempt < 1 {
				t.Errorf("human_reject event attempt = %d, want >= 1", e.Attempt)
			}
			if e.RejectionReason != "clarify deployment constraints" {
				t.Errorf("rejection reason = %q, want clarify deployment constraints", e.RejectionReason)
			}
		}
	}
	if !foundReject {
		t.Errorf("no human_reject event with rejection reason found")
	}

	indexPath := filepath.Join(tmpDir, ".workflow", "runs", runID, "attempts.index.jsonl")
	if _, err := os.Stat(indexPath); err != nil {
		t.Errorf("attempts.index.jsonl not created: %v", err)
	}
}

func TestPlanningRevisionWritesLineage(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "20260630-130000"
	run := planningAgentRun{
		RunID:       runID,
		AgentID:     "node2:codex",
		AgentType:   "codex",
		SessionID:   NewPTYSessionID(runID, "node2", "codex", 1),
		Prompt:      "initial planning prompt",
		PromptFile:  filepath.Join(tmpDir, "codex.prompt.md"),
		OutFile:     filepath.Join(tmpDir, "codex.out.md"),
		ExitFile:    filepath.Join(tmpDir, "codex.exit"),
		HistoryFile: filepath.Join(tmpDir, "codex.messages.jsonl"),
		Session: AgentSession{
			ID:          NewPTYSessionID(runID, "node2", "codex", 1),
			AgentType:   "codex",
			Status:      AgentSessionPending,
			HistoryFile: filepath.Join(tmpDir, "codex.messages.jsonl"),
		},
	}
	RegisterAgentSession(run.Session)
	out := planningAgentOutput{
		AgentType:   run.AgentType,
		SessionID:   run.SessionID,
		Status:      "completed",
		Output:      "first planning output",
		PromptFile:  run.PromptFile,
		OutFile:     run.OutFile,
		ExitFile:    run.ExitFile,
		HistoryFile: run.HistoryFile,
		Session:     run.Session,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	approverDone := make(chan error, 1)
	go func() {
		waitForHistoryRoleCount(t, run.HistoryFile, "approval_request", 1)
		if err := RejectSession(run.SessionID, run.AgentID, ReviewDecision{
			Reason:     "tighten the plan",
			RejectType: RejectTypeReplan,
		}); err != nil {
			approverDone <- err
			return
		}
		waitForHistoryRoleCount(t, run.HistoryFile, "approval_request", 2)
		approverDone <- ApproveSession(run.SessionID, run.AgentID, "approved after revision")
	}()

	err := waitForPlanningAgentApproval(ctx, tmpDir, &out, run, func(ctx context.Context, repoRoot string, next planningAgentRun) planningAgentOutput {
		next.Session = run.Session
		return planningAgentOutput{
			AgentType:   next.AgentType,
			SessionID:   next.SessionID,
			Status:      "completed",
			Output:      "revised planning output",
			PromptFile:  next.PromptFile,
			OutFile:     next.OutFile,
			ExitFile:    next.ExitFile,
			HistoryFile: next.HistoryFile,
			Session:     next.Session,
		}
	})
	if err != nil {
		t.Fatalf("waitForPlanningAgentApproval() error = %v", err)
	}
	if err := <-approverDone; err != nil {
		t.Fatalf("approval goroutine error = %v", err)
	}

	nodePath := filepath.Join(tmpDir, ".workflow", "runs", runID, "planning", "attempts.jsonl")
	if _, err := os.Stat(nodePath); err != nil {
		t.Fatalf("attempts.jsonl not created: %v", err)
	}

	events, err := ReadAttemptEvents(tmpDir, runID, "planning")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 {
		t.Fatalf("expected >= 2 events, got %d", len(events))
	}
	if events[0].Attempt != 0 {
		t.Errorf("first event attempt = %d, want 0", events[0].Attempt)
	}
	if events[0].NodeKind != NodeKindPlanning {
		t.Errorf("first event node_kind = %q, want %q", events[0].NodeKind, NodeKindPlanning)
	}
	if events[0].Scope != "node-agent" {
		t.Errorf("first event scope = %q, want node-agent", events[0].Scope)
	}
	if events[0].Role != "codex" {
		t.Errorf("first event role = %q, want codex", events[0].Role)
	}

	var foundReject bool
	for _, e := range events {
		if e.FailureClass == FailureClassHumanReject && e.RejectionReason != "" {
			foundReject = true
			if e.Attempt < 1 {
				t.Errorf("human_reject event attempt = %d, want >= 1", e.Attempt)
			}
			if e.RejectionReason != "tighten the plan" {
				t.Errorf("rejection reason = %q, want tighten the plan", e.RejectionReason)
			}
		}
	}
	if !foundReject {
		t.Errorf("no human_reject event with rejection reason found")
	}
}

func TestDevelopmentSubtaskInitialWritesLineage(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "20260630-140000"

	writeDevelopmentAttempt(
		tmpDir,
		runID,
		developmentLineageNodeID,
		"session-dev-api",
		"api",
		0,
		-1,
		"initial",
		FailureClassNone,
		"",
		filepath.Join(tmpDir, "api.prompt.md"),
		filepath.Join(tmpDir, "api.out.md"),
		filepath.Join(tmpDir, "api.exit"),
	)

	events, err := ReadAttemptEvents(tmpDir, runID, developmentLineageNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	got := events[0]
	if got.Attempt != 0 {
		t.Errorf("Attempt = %d, want 0", got.Attempt)
	}
	if got.NodeKind != NodeKindDevelopment {
		t.Errorf("NodeKind = %q, want %q", got.NodeKind, NodeKindDevelopment)
	}
	if got.Scope != "subtask:api" {
		t.Errorf("Scope = %q, want subtask:api", got.Scope)
	}
	if got.Role != "codex" {
		t.Errorf("Role = %q, want codex", got.Role)
	}
	if got.FailureClass != FailureClassNone {
		t.Errorf("FailureClass = %q, want %q", got.FailureClass, FailureClassNone)
	}

	indexPath := filepath.Join(tmpDir, ".workflow", "runs", runID, "attempts.index.jsonl")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("attempts.index.jsonl not created: %v", err)
	}
}

func TestDevelopmentSubtaskRevisionWritesLineage(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "20260630-141000"

	writeDevelopmentAttempt(tmpDir, runID, developmentLineageNodeID, "session-dev-api", "api", 0, -1, "initial", FailureClassNone, "", "initial.prompt.md", "initial.out.md", "initial.exit")
	writeDevelopmentAttempt(tmpDir, runID, developmentLineageNodeID, "session-dev-api", "api", 1, 0, "human_reject", FailureClassHumanReject, "add API validation", "revision.prompt.md", "revision.out.md", "revision.exit")

	events, err := ReadAttemptEvents(tmpDir, runID, developmentLineageNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Attempt != 0 {
		t.Errorf("initial attempt = %d, want 0", events[0].Attempt)
	}
	revision := events[1]
	if revision.Attempt != 1 {
		t.Errorf("revision attempt = %d, want 1", revision.Attempt)
	}
	if revision.Trigger != "human_reject" {
		t.Errorf("revision trigger = %q, want human_reject", revision.Trigger)
	}
	if revision.FailureClass != FailureClassHumanReject {
		t.Errorf("revision failure class = %q, want %q", revision.FailureClass, FailureClassHumanReject)
	}
	if revision.RejectionReason != "add API validation" {
		t.Errorf("revision rejection reason = %q, want add API validation", revision.RejectionReason)
	}
	if revision.PromptDelta == nil || revision.PromptDelta.AddedFeedback != "reviewer requested development revision: add API validation" {
		t.Errorf("revision prompt delta = %+v", revision.PromptDelta)
	}

	shardPath := filepath.Join(tmpDir, ".workflow", "runs", runID, developmentLineageNodeID, "attempts", "subtask_api.jsonl")
	if _, err := os.Stat(shardPath); err != nil {
		t.Fatalf("scope shard not created: %v", err)
	}
}

func TestWriteDevelopmentAttemptEmptyRunIDSkips(t *testing.T) {
	tmpDir := t.TempDir()

	writeDevelopmentAttempt(tmpDir, "", developmentLineageNodeID, "session-dev-api", "api", 0, -1, "initial", FailureClassNone, "", "prompt.md", "out.md", "exit")

	if _, err := os.Stat(filepath.Join(tmpDir, ".workflow")); !os.IsNotExist(err) {
		t.Fatalf(".workflow stat error = %v, want not exist", err)
	}
}

func TestWaitForSubtaskApprovalRejectsAndRerunsUntilApproved(t *testing.T) {
	dir := t.TempDir()
	sessionID := "subtask-agent-session"
	nodeName := "subtask:api"
	agent := &SubtaskAgent{
		SubtaskID: "api",
		Session: AgentSession{
			ID:          sessionID,
			AgentType:   "codex",
			Status:      AgentSessionPending,
			HistoryFile: filepath.Join(dir, "api.messages.jsonl"),
		},
	}
	RegisterAgentSession(agent.Session)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	approverDone := make(chan error, 1)
	go func() {
		waitForHistoryRoleCount(t, agent.Session.HistoryFile, "approval_request", 1)
		if err := RejectSession(sessionID, nodeName, ReviewDecision{
			Reason:     "add API validation",
			RejectType: RejectTypeReviseSubtask,
		}); err != nil {
			approverDone <- err
			return
		}
		waitForHistoryRoleCount(t, agent.Session.HistoryFile, "approval_request", 2)
		if err := RejectSession(sessionID, nodeName, ReviewDecision{
			Reason:     "cover validation tests",
			RejectType: RejectTypeReviseSubtask,
		}); err != nil {
			approverDone <- err
			return
		}
		waitForHistoryRoleCount(t, agent.Session.HistoryFile, "approval_request", 3)
		approverDone <- ApproveSession(sessionID, nodeName, "approved after second revision")
	}()

	executions := 0
	issues, err := waitForSubtaskApprovalRevisions(ctx, agent, sessionID, nodeName, "api", nil, func(revisionIssues []ReviewIssue, revision int) error {
		executions++
		if revision != executions {
			return errors.New("revision counter did not match execution count")
		}
		if len(revisionIssues) != executions {
			return errors.New("revision issue was not appended")
		}
		want := []string{"add API validation", "cover validation tests"}[executions-1]
		if !strings.Contains(revisionIssues[len(revisionIssues)-1].Description, want) {
			return errors.New("revision issue missing reviewer feedback")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("waitForSubtaskApprovalRevisions() error = %v", err)
	}
	if err := <-approverDone; err != nil {
		t.Fatalf("approval goroutine error = %v", err)
	}
	if executions != 2 {
		t.Fatalf("executions = %d, want 2 revision reruns", executions)
	}
	if len(issues) != 2 {
		t.Fatalf("len(issues) = %d, want 2", len(issues))
	}
	messages := readHistoryMessages(t, agent.Session.HistoryFile)
	var approvals, revisions int
	for _, msg := range messages {
		if msg.Role == "approval_request" {
			approvals++
		}
		if msg.Role == "user" && (strings.Contains(msg.Content, "add API validation") || strings.Contains(msg.Content, "cover validation tests")) {
			revisions++
		}
	}
	if approvals != 3 {
		t.Fatalf("approval requests = %d, want 3", approvals)
	}
	if revisions != 2 {
		t.Fatalf("revision messages = %d, want 2", revisions)
	}
}

func TestRunDevelopmentSubtaskUsesPTYSessionAndRegistersIt(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, "workflow"), 0755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	commandDir := t.TempDir()
	codexPath := filepath.Join(commandDir, "codex")
	if err := os.WriteFile(codexPath, []byte(`#!/bin/sh
if [ "$1" = "exec" ]; then
  printf 'exec mode not allowed\n'
  exit 42
fi
printf 'OpenAI Codex\n› '
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf 'development done\n'
      printf '%s\n' "$marker"
      ;;
  esac
done
`), 0755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", commandDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldRegistry := adapter.DefaultPTYSessionRegistry
	adapter.DefaultPTYSessionRegistry = adapter.NewPTYSessionRegistry()
	t.Cleanup(func() { adapter.DefaultPTYSessionRegistry = oldRegistry })

	plan := &Plan{TaskID: "run-dev-pty", Subtasks: []Subtask{validSubtask("api")}}
	nodeDir := filepath.Join(repoRoot, ".workflow", "runs", plan.TaskID, "node3")
	agents, err := BuildSubtaskAgents(repoRoot, nodeDir, plan)
	if err != nil {
		t.Fatalf("BuildSubtaskAgents() error = %v", err)
	}
	agent := &agents[0]

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	approvalDone := make(chan error, 1)
	go func() {
		waitForHistoryRoleCount(t, agent.Session.HistoryFile, "approval_request", 1)
		approvalDone <- ApproveSession(agent.Session.ID, "subtask:api", "approved")
	}()

	result := runDevelopmentSubtask(ctx, repoRoot, nodeDir, plan, agent, plan.Subtasks[0], 1, 0, nil)
	if err := <-approvalDone; err != nil {
		t.Fatalf("approval error = %v", err)
	}
	if result.Err != nil {
		t.Fatalf("runDevelopmentSubtask() error = %v, output=%q", result.Err, result.Output)
	}
	if result.Output != "development done" {
		t.Fatalf("output = %q, want development done", result.Output)
	}
	sessions := adapter.DefaultPTYSessionRegistry.ListByRun(plan.TaskID)
	if len(sessions) != 1 {
		t.Fatalf("registry sessions = %+v, want one session for run", sessions)
	}
	if sessions[0].SessionID != agent.Session.ID || sessions[0].SubtaskID != "api" || sessions[0].AgentType != "codex" {
		t.Fatalf("registered session = %+v, want subtask codex session", sessions[0])
	}
}

func readHistoryMessages(t *testing.T, path string) []AgentMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(history) error = %v", err)
	}
	var messages []AgentMessage
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg AgentMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("unmarshal history line %q: %v", line, err)
		}
		messages = append(messages, msg)
	}
	return messages
}

func waitForHistoryRole(t *testing.T, path, role string) {
	t.Helper()
	deadline := time.After(500 * time.Millisecond)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for history role %q", role)
		case <-tick.C:
			for _, msg := range readHistoryMessagesIfExists(t, path) {
				if msg.Role == role {
					return
				}
			}
		}
	}
}

func waitForHistoryRoleCount(t *testing.T, path, role string, want int) {
	t.Helper()
	deadline := time.After(500 * time.Millisecond)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d history role %q", want, role)
		case <-tick.C:
			count := 0
			for _, msg := range readHistoryMessagesIfExists(t, path) {
				if msg.Role == role {
					count++
				}
			}
			if count >= want {
				return
			}
		}
	}
}

func readHistoryMessagesIfExists(t *testing.T, path string) []AgentMessage {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return readHistoryMessages(t, path)
}

func validSubtask(id string) Subtask {
	return Subtask{
		ID:                 id,
		AgentType:          "codex",
		RepoPath:           "workflow",
		Description:        "Implement workflow behavior in workflow files.",
		AcceptanceCriteria: []string{"go build ./workflow/... passes"},
	}
}
