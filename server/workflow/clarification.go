package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ming-agents/server/adapter"
)

type clarificationRun struct {
	AgentType  string
	SessionID  string
	Prompt     string
	PromptFile string
	OutFile    string
	ExitFile   string
}

type clarificationOutput struct {
	AgentType string
	SessionID string
	Status    string
	ExitCode  int
	Output    string
	Err       error
}

func RunClarification(ctx context.Context, repoRoot, userInput string) (string, error) {
	runID := time.Now().Format("20060102-150405")
	runRoot := filepath.Join(repoRoot, ".workflow", "runs", runID)
	nodeDir := filepath.Join(runRoot, "node1")
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		return "", err
	}
	_ = writeWorkflowState(repoRoot, runID, map[string]NodeStatus{
		"node1": NodeRunning,
		"node2": NodePending,
		"node3": NodePending,
	}, nil)

	runs := []clarificationRun{
		newClarificationRun(runID, nodeDir, "codex", userInput, 1),
		newClarificationRun(runID, nodeDir, "claude-code", userInput, 2),
	}
	for _, run := range runs {
		if err := writeTextAtomic(run.PromptFile, run.Prompt); err != nil {
			return "", err
		}
	}

	outputs := make([]clarificationOutput, len(runs))
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outputs[i] = executeClarificationAgent(ctx, repoRoot, runs[i])
		}(i)
	}
	wg.Wait()

	merged := mergeClarificationOutputs(runID, outputs)
	target := filepath.Join(repoRoot, "docs", "requirements-clarity.md")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", err
	}
	if err := writeTextAtomic(target, merged); err != nil {
		return "", err
	}

	state := NodeCompleted
	err := error(nil)
	if allClarificationAgentsFailed(outputs) {
		state = NodeFailed
		err = fmt.Errorf("all clarification agents failed")
	}
	_ = writeWorkflowState(repoRoot, runID, map[string]NodeStatus{
		"node1": state,
		"node2": NodePending,
		"node3": NodePending,
	}, map[string]any{"clarification_file": target})
	return target, err
}

func newClarificationRun(runID, nodeDir, agentType, userInput string, index int) clarificationRun {
	sessionID := NewPTYSessionID(runID, "node1", agentType, index)
	base := agentType
	return clarificationRun{
		AgentType:  agentType,
		SessionID:  sessionID,
		Prompt:     renderClarificationPrompt(agentType, userInput),
		PromptFile: filepath.Join(nodeDir, base+".prompt.md"),
		OutFile:    filepath.Join(nodeDir, base+".out.md"),
		ExitFile:   filepath.Join(nodeDir, base+".exit"),
	}
}

func executeClarificationAgent(ctx context.Context, repoRoot string, run clarificationRun) clarificationOutput {
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	out := clarificationOutput{
		AgentType: run.AgentType,
		SessionID: run.SessionID,
		Status:    "completed",
		ExitCode:  0,
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
