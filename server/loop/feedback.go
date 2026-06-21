package loop

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// IterationSnapshot records the complete state of a loop iteration.
// Epic 3.3: IterationSnapshot tracks iteration history for feedback assembly.
type IterationSnapshot struct {
	ID        uuid.UUID       `json:"id"`
	RunID     uuid.UUID       `json:"run_id"`
	StepID    uuid.UUID       `json:"step_id"`
	Iteration int             `json:"iteration"`
	Score     float64         `json:"score"`
	Feedback  string          `json:"feedback"`
	Inputs    map[string]any  `json:"inputs"`
	Outputs   map[string]any  `json:"outputs"`
	Metadata  map[string]any  `json:"metadata,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// NewIterationSnapshot creates a new iteration snapshot.
func NewIterationSnapshot(runID, stepID uuid.UUID, iteration int, score float64, feedback string, inputs, outputs map[string]any) *IterationSnapshot {
	return &IterationSnapshot{
		ID:        uuid.New(),
		RunID:     runID,
		StepID:    stepID,
		Iteration: iteration,
		Score:     score,
		Feedback:  feedback,
		Inputs:    inputs,
		Outputs:   outputs,
		Metadata:  make(map[string]any),
		Timestamp: time.Now().UTC(),
	}
}

// FeedbackAssembler assembles evaluation results into prompts for the next iteration.
// Epic 3.3: 把评估结果转成下一轮迭代的输入/prompt 增强.
type FeedbackAssembler struct {
	systemPromptTemplate string
	userPromptTemplate   string
	maxHistoryItems      int
}

// FeedbackAssemblerConfig configures the feedback assembler.
type FeedbackAssemblerConfig struct {
	// SystemPromptTemplate is the template for the system prompt.
	// Placeholders: {iter}, {summary}, {feedback}, {hints}
	SystemPromptTemplate string
	// UserPromptTemplate is the template for the user prompt.
	// Placeholders: {task_description}, {previous_outputs}, {specific_feedback}
	UserPromptTemplate string
	// MaxHistoryItems limits how many historical iterations to include in prompt.
	MaxHistoryItems int
	// IncludeImprovementHints enables automatic improvement hint generation.
	IncludeImprovementHints bool
}

// DefaultFeedbackAssemblerConfig returns sensible defaults.
func DefaultFeedbackAssemblerConfig() FeedbackAssemblerConfig {
	return FeedbackAssemblerConfig{
		SystemPromptTemplate: `You are iteration {iter} of a feedback loop.
Previous work summary: {summary}
Specific feedback from previous iteration: {feedback}
{hints}
Continue improving based on this feedback.`,
		UserPromptTemplate: `## Task
{task_description}

## Previous Outputs
{previous_outputs}

## Specific Feedback
{specific_feedback}

## Instructions
Analyze the feedback and previous outputs. Make improvements to address the feedback.
Output your improved solution.`,
		MaxHistoryItems:           5,
		IncludeImprovementHints:   true,
	}
}

// NewFeedbackAssembler creates a new feedback assembler with default config.
func NewFeedbackAssembler() *FeedbackAssembler {
	return NewFeedbackAssemblerWithConfig(DefaultFeedbackAssemblerConfig())
}

// NewFeedbackAssemblerWithConfig creates a feedback assembler with custom config.
func NewFeedbackAssemblerWithConfig(cfg FeedbackAssemblerConfig) *FeedbackAssembler {
	if cfg.SystemPromptTemplate == "" {
		cfg.SystemPromptTemplate = DefaultFeedbackAssemblerConfig().SystemPromptTemplate
	}
	if cfg.UserPromptTemplate == "" {
		cfg.UserPromptTemplate = DefaultFeedbackAssemblerConfig().UserPromptTemplate
	}
	if cfg.MaxHistoryItems <= 0 {
		cfg.MaxHistoryItems = 5
	}
	return &FeedbackAssembler{
		systemPromptTemplate: cfg.SystemPromptTemplate,
		userPromptTemplate:   cfg.UserPromptTemplate,
		maxHistoryItems:      cfg.MaxHistoryItems,
	}
}

// AssemblePrompt assembles a prompt for the next iteration.
// iter: current iteration number
// previousOutputs: outputs from the previous iteration
// feedback: feedback from the evaluator
// score: evaluation score from the previous iteration
func (a *FeedbackAssembler) AssemblePrompt(iter int, previousOutputs map[string]any, feedback string, score float64) string {
	return a.AssemblePromptWithHistory(iter, nil, previousOutputs, feedback, score, "")
}

// AssemblePromptWithHistory assembles a prompt with full iteration history.
// history: previous iteration snapshots (most recent last)
// taskDescription: the original task being worked on
func (a *FeedbackAssembler) AssemblePromptWithHistory(iter int, history []*IterationSnapshot, previousOutputs map[string]any, feedback string, score float64, taskDescription string) string {
	var sb strings.Builder

	// Build summary from history
	summary := a.buildSummary(history, previousOutputs, score)

	// Build improvement hints
	hints := ""
	if a.maxHistoryItems > 0 {
		hints = a.generateHints(history, feedback, score)
	}

	// Format system prompt
	systemPrompt := a.formatSystemPrompt(iter, summary, feedback, hints)
	sb.WriteString("SYSTEM:\n")
	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n")

	// Format user prompt
	userPrompt := a.formatUserPrompt(taskDescription, previousOutputs, feedback)
	sb.WriteString("USER:\n")
	sb.WriteString(userPrompt)

	return sb.String()
}

// buildSummary creates a summary string from history and current state.
func (a *FeedbackAssembler) buildSummary(history []*IterationSnapshot, currentOutputs map[string]any, currentScore float64) string {
	var sb strings.Builder

	// Include recent history (limited)
	if len(history) > 0 {
		start := 0
		if len(history) > a.maxHistoryItems {
			start = len(history) - a.maxHistoryItems
		}
		recentHistory := history[start:]
		
		sb.WriteString("Iteration history:\n")
		for _, snap := range recentHistory {
			sb.WriteString(fmt.Sprintf("  - Iter %d: score=%.2f, feedback=%q\n", 
				snap.Iteration, snap.Score, truncate(snap.Feedback, 50)))
		}
	}

	// Current state
	if currentOutputs != nil {
		sb.WriteString(fmt.Sprintf("Current iteration outputs (score=%.2f):\n", currentScore))
		enc := json.NewEncoder(&sb)
		enc.SetIndent("  ", "  ")
		_ = enc.Encode(currentOutputs)
	}

	return sb.String()
}

// generateHints generates improvement hints based on history and feedback.
func (a *FeedbackAssembler) generateHints(history []*IterationSnapshot, feedback string, score float64) string {
	var sb strings.Builder

	sb.WriteString("## Improvement guidance\n")

	// Analyze score trend
	if len(history) >= 2 {
		trend := analyzeScoreTrend(history)
		switch trend {
		case "improving":
			sb.WriteString("- Continue with current approach; scores are improving\n")
		case "declining":
			sb.WriteString("- Consider changing strategy; scores have been declining\n")
		case "stagnant":
			sb.WriteString("- Current approach appears stagnant; try alternative approaches\n")
		}
	}

	// Score-based hints
	if score < 0.3 {
		sb.WriteString("- Significant issues detected; fundamental changes may be needed\n")
	} else if score < 0.6 {
		sb.WriteString("- Moderate issues; focus on specific improvements mentioned in feedback\n")
	} else if score >= 0.9 {
		sb.WriteString("- Near convergence; focus on fine-tuning and edge cases\n")
	}

	// Feedback-based hints
	if feedback != "" {
		sb.WriteString(fmt.Sprintf("- Key focus areas: %s\n", truncate(feedback, 100)))
	}

	return sb.String()
}

// analyzeScoreTrend determines the trend from score history.
func analyzeScoreTrend(history []*IterationSnapshot) string {
	if len(history) < 2 {
		return "unknown"
	}
	
	// Use last 5 iterations for trend analysis
	recent := history
	if len(recent) > 5 {
		recent = recent[len(recent)-5:]
	}

	var improvements, declines int
	for i := 1; i < len(recent); i++ {
		diff := recent[i].Score - recent[i-1].Score
		if diff > 0.05 {
			improvements++
		} else if diff < -0.05 {
			declines++
		}
	}

	if improvements > declines*2 {
		return "improving"
	} else if declines > improvements*2 {
		return "declining"
	}
	return "stagnant"
}

// formatSystemPrompt applies templates to the system prompt.
func (a *FeedbackAssembler) formatSystemPrompt(iter int, summary, feedback, hints string) string {
	s := a.systemPromptTemplate
	s = strings.ReplaceAll(s, "{iter}", fmt.Sprintf("%d", iter))
	s = strings.ReplaceAll(s, "{summary}", summary)
	s = strings.ReplaceAll(s, "{feedback}", feedback)
	s = strings.ReplaceAll(s, "{hints}", hints)
	return s
}

// formatUserPrompt applies templates to the user prompt.
func (a *FeedbackAssembler) formatUserPrompt(taskDescription string, previousOutputs map[string]any, specificFeedback string) string {
	s := a.userPromptTemplate
	s = strings.ReplaceAll(s, "{task_description}", taskDescription)
	
	// Format previous outputs as readable string
	var outputsStr string
	if previousOutputs != nil {
		var sb strings.Builder
		enc := json.NewEncoder(&sb)
		enc.SetIndent("  ", "  ")
		_ = enc.Encode(previousOutputs)
		outputsStr = sb.String()
	}
	s = strings.ReplaceAll(s, "{previous_outputs}", outputsStr)
	s = strings.ReplaceAll(s, "{specific_feedback}", specificFeedback)
	return s
}

// AssembleSystemPrompt returns just the system prompt portion.
func (a *FeedbackAssembler) AssembleSystemPrompt(iter int, history []*IterationSnapshot, feedback string, score float64) string {
	summary := a.buildSummary(history, nil, score)
	hints := a.generateHints(history, feedback, score)
	return a.formatSystemPrompt(iter, summary, feedback, hints)
}

// AssembleUserPrompt returns just the user prompt portion.
func (a *FeedbackAssembler) AssembleUserPrompt(taskDescription string, previousOutputs map[string]any, feedback string) string {
	return a.formatUserPrompt(taskDescription, previousOutputs, feedback)
}

// truncate truncates a string to maxLen characters, adding ellipsis if needed.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}