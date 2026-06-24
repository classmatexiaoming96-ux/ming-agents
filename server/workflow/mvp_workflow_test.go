package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestParseReviewReportDetectsBlockingIssues(t *testing.T) {
	md := `# Review

## Summary
Two criteria are not satisfied yet.

## Issues
- severity: blocking
  subtask_id: one
  session_id: pty-run-node3-codex-1
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

func validSubtask(id string) Subtask {
	return Subtask{
		ID:                 id,
		AgentType:          "codex",
		RepoPath:           "workflow",
		Description:        "Implement workflow behavior in workflow files.",
		AcceptanceCriteria: []string{"go build ./workflow/... passes"},
	}
}
