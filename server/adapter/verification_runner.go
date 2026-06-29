package adapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ArtifactIndex is the development artifact index passed to the independent evaluator.
type ArtifactIndex struct {
	PlanPath    string
	OutputPath  string
	CodeDirs    []string
	EvidenceDir string
}

type VerificationPlan struct {
	TaskID   string
	Subtasks []VerificationSubtask
}

type VerificationSubtask struct {
	ID                 string
	Description        string
	AcceptanceCriteria []string
}

type VerificationResult struct {
	RunID       string
	EvaluatedAt time.Time
	Evidence    []VerificationEvidenceRef
	Verdict     string
	Reason      string
	RawOutput   string
}

type VerificationEvidenceRef struct {
	Type string
	Path string
}

// VerificationRunner executes independent validation in a fresh Codex PTY session.
type VerificationRunner struct {
	manager *CodexSessionManager
}

func NewVerificationRunner(manager *CodexSessionManager) *VerificationRunner {
	return &VerificationRunner{manager: manager}
}

func (r *VerificationRunner) Run(ctx context.Context, runID string, plan VerificationPlan, artifacts ArtifactIndex) (*VerificationResult, error) {
	if r == nil || r.manager == nil {
		return nil, fmt.Errorf("verification runner requires a CodexSessionManager")
	}
	prompt := r.buildVerificationPrompt(runID, plan, artifacts)
	runDir := verificationRunDir(runID, artifacts)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return nil, err
	}
	promptPath := filepath.Join(runDir, "evaluator_prompt.md")
	if err := os.WriteFile(promptPath, []byte(prompt), 0644); err != nil {
		return nil, err
	}

	session, err := r.manager.StartSession(ctx, verificationWorkDir(artifacts))
	if err != nil {
		return nil, fmt.Errorf("failed to start evaluator session: %w", err)
	}
	defer session.Close()

	return r.runEvaluatorSession(ctx, session, runID, promptPath, artifacts)
}

func (r *VerificationRunner) buildVerificationPrompt(runID string, plan VerificationPlan, artifacts ArtifactIndex) string {
	var lines []string
	lines = append(lines, "# Verification Task")
	lines = append(lines, "")
	lines = append(lines, "## Run")
	lines = append(lines, runID)
	lines = append(lines, "")
	lines = append(lines, "## Task")
	lines = append(lines, plan.TaskID)
	lines = append(lines, "")
	lines = append(lines, "## Acceptance Criteria")
	for _, st := range plan.Subtasks {
		lines = append(lines, fmt.Sprintf("### %s", st.ID))
		if strings.TrimSpace(st.Description) != "" {
			lines = append(lines, st.Description)
		}
		for _, criterion := range st.AcceptanceCriteria {
			lines = append(lines, "- "+criterion)
		}
	}
	lines = append(lines, "")
	lines = append(lines, "## Artifact Paths")
	lines = append(lines, "Read only these artifact files and directories. Do not use development session history.")
	lines = append(lines, fmt.Sprintf("- Plan file: %s", artifacts.PlanPath))
	lines = append(lines, fmt.Sprintf("- Output document: %s", artifacts.OutputPath))
	for _, dir := range artifacts.CodeDirs {
		lines = append(lines, fmt.Sprintf("- Code directory: %s", dir))
	}
	lines = append(lines, "")
	lines = append(lines, "## Verification Requirements")
	lines = append(lines, "1. Base the result only on the artifact paths above.")
	lines = append(lines, "2. Check whether docs/output.md satisfies the acceptance criteria.")
	lines = append(lines, "3. Check whether expected code artifacts exist.")
	lines = append(lines, "4. Return a structured verdict.")
	lines = append(lines, "")
	lines = append(lines, "## Return Format")
	lines = append(lines, "VERDICT: PASS | FAIL")
	lines = append(lines, "REASON: <specific reason>")
	lines = append(lines, "ISSUES: <specific issues if FAIL>")
	return strings.Join(lines, "\n")
}

func (r *VerificationRunner) runEvaluatorSession(ctx context.Context, session *CodexSession, runID, promptPath string, artifacts ArtifactIndex) (*VerificationResult, error) {
	result := &VerificationResult{
		RunID:       runID,
		EvaluatedAt: time.Now(),
	}

	prompt := fmt.Sprintf("Read the verification prompt at %s and return only the requested verdict format.", promptPath)
	output, err := session.SendPrompt(ctx, prompt)
	if err != nil {
		result.Verdict = "ERROR"
		result.Reason = err.Error()
		return result, nil
	}

	result.RawOutput = output
	result.Verdict = r.parseVerdict(output)
	result.Reason = parseVerdictReason(output)
	result.Evidence = r.collectEvidence(artifacts.EvidenceDir)
	return result, nil
}

func (r *VerificationRunner) parseVerdict(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "VERDICT:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseVerdictReason(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "REASON:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (r *VerificationRunner) collectEvidence(evidenceDir string) []VerificationEvidenceRef {
	if evidenceDir == "" {
		return nil
	}
	var refs []VerificationEvidenceRef
	patterns := []struct{ t, p string }{
		{"build_log", "build.log"},
		{"test_log", "test.log"},
		{"coverage", "coverage.out"},
	}
	for _, p := range patterns {
		path := filepath.Join(evidenceDir, p.p)
		if _, err := os.Stat(path); err == nil {
			refs = append(refs, VerificationEvidenceRef{Type: p.t, Path: path})
		}
	}
	return refs
}

func verificationRunDir(runID string, artifacts ArtifactIndex) string {
	if artifacts.EvidenceDir != "" {
		return artifacts.EvidenceDir
	}
	return filepath.Join(".workflow", "runs", runID)
}

func verificationWorkDir(artifacts ArtifactIndex) string {
	for _, path := range []string{artifacts.OutputPath, artifacts.PlanPath} {
		if path == "" {
			continue
		}
		dir := filepath.Dir(path)
		if filepath.Base(dir) == "docs" {
			return filepath.Dir(dir)
		}
		return dir
	}
	if artifacts.EvidenceDir != "" {
		return filepath.Dir(filepath.Dir(filepath.Dir(artifacts.EvidenceDir)))
	}
	return "."
}
