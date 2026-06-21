package loop

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/engine"
)

// Evaluator is the plugin point for loop convergence evaluation.
// Implementations assess iteration output quality and decide convergence.
// Epic 3.2: Evaluator abstraction + agent implementation.
type Evaluator interface {
	// Name returns the evaluator name/plugin key.
	Name() string

	// Evaluate assesses the current iteration's outputs and returns a score,
	// natural language feedback, and opaque metadata.
	Evaluate(iteration int, ctx *engine.Context, bodyOutputs map[string]any) (
		score float64, feedback string, metadata map[string]any, err error)
}

// ─── AgentEvaluator ──────────────────────────────────────────────────────────

// AgentEvaluatorConfig holds configuration for an AgentEvaluator.
type AgentEvaluatorConfig struct {
	AdapterKey   string  // adapter key e.g. "api", "claude-code"
	Model        string  // model name e.g. "claude-sonnet-4-20250514"
	Prompt       string  // evaluation prompt template
	ScoreThresh  float64 // score threshold for convergence (0.0-1.0)
	HigherBetter bool    // if true, higher scores are better
}

// AgentEvaluator uses an agent adapter to evaluate iteration output quality.
// Score is 0.0-1.0 based on task completion; feedback is natural language.
type AgentEvaluator struct {
	adapter      adapter.AgentAdapter
	adapterKey   string
	model        string
	prompt       string
	scoreThresh  float64
	higherBetter bool
}

// NewAgentEvaluator creates an AgentEvaluator from an adapter and config.
func NewAgentEvaluator(a adapter.AgentAdapter, cfg AgentEvaluatorConfig) *AgentEvaluator {
	return &AgentEvaluator{
		adapter:      a,
		adapterKey:   cfg.AdapterKey,
		model:        cfg.Model,
		prompt:       cfg.Prompt,
		scoreThresh:  cfg.ScoreThresh,
		higherBetter: cfg.HigherBetter,
	}
}

// Name returns the evaluator name.
func (e *AgentEvaluator) Name() string { return "agent" }

// Evaluate invokes the agent to score iteration output.
func (e *AgentEvaluator) Evaluate(iteration int, ctx *engine.Context, bodyOutputs map[string]any) (
	float64, string, map[string]any, error) {

	prompt := e.buildPrompt(iteration, ctx, bodyOutputs)

	req := adapter.AgentRequest{
		Model:  e.model,
		Prompt: prompt,
	}

	result, err := e.adapter.Invoke(req)
	if err != nil {
		return 0.0, "", nil, fmt.Errorf("agent invoke: %w", err)
	}

	score, feedback, metadata := e.parseResult(result, iteration)
	return score, feedback, metadata, nil
}

// buildPrompt constructs the evaluation prompt from template + context.
func (e *AgentEvaluator) buildPrompt(iteration int, ctx *engine.Context, outputs map[string]any) string {
	tmpl := e.prompt
	if tmpl == "" {
		tmpl = defaultAgentPrompt
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You are evaluating iteration %d of a loop.\n\n", iteration))

	// Dump context outputs.
	if ctx != nil {
		sb.WriteString("## Context so far\n")
		for stepName, outMap := range ctx.GetAll() {
			if outMap == nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("### %s\n", stepName))
			for k, v := range outMap {
				sb.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
			}
		}
	}

	// Dump body outputs from this iteration.
	if outputs != nil {
		sb.WriteString("\n## Iteration outputs\n")
		enc := json.NewEncoder(&sb)
		enc.SetIndent("", "")
		_ = enc.Encode(outputs)
	}

	sb.WriteString("\n## Task\n")
	sb.WriteString(tmpl)

	return sb.String()
}

// parseResult extracts score, feedback, metadata from agent result.
func (e *AgentEvaluator) parseResult(result *adapter.AgentResult, iteration int) (
	float64, string, map[string]any) {

	metadata := map[string]any{
		"model":       e.model,
		"adapter_key": e.adapterKey,
		"iteration":   iteration,
	}

	// Try to parse structured JSON first.
	var parsed struct {
		Score      float64 `json:"score"`
		Feedback   string  `json:"feedback"`
		Converged  bool    `json:"converged"`
		Confidence float64 `json:"confidence"`
		TokensUsed int     `json:"tokens_used"`
	}

	if len(result.RawJSON) > 0 {
		if err := json.Unmarshal(result.RawJSON, &parsed); err == nil {
			metadata["tokens_used"] = parsed.TokensUsed
			metadata["confidence"] = parsed.Confidence
			return clampScore(parsed.Score), parsed.Feedback, metadata
		}
	}

	// Fall back to text extraction: look for "score: X" patterns.
	score := e.extractScoreFromText(result.Output)
	feedback := result.Summary
	if feedback == "" {
		feedback = result.Output
	}

	return clampScore(score), feedback, metadata
}

// extractScoreFromText tries to find a numeric score in plain text output.
func (e *AgentEvaluator) extractScoreFromText(text string) float64 {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(strings.ToLower(line), "score") {
			for _, tok := range strings.Fields(line) {
				tok = strings.Trim(tok, ":=is")
				if f, err := strconv.ParseFloat(tok, 64); err == nil && f >= 0 && f <= 1 {
					return f
				}
			}
		}
	}
	return 0.5 // neutral default when no score found
}

// clampScore ensures score is in [0.0, 1.0].
func clampScore(s float64) float64 {
	if s < 0 {
		return 0
	}
	if s > 1 {
		return 1
	}
	return s
}

var defaultAgentPrompt = `Evaluate the iteration output for task completion quality.

Respond with a JSON object:
{
  "score": 0.0-1.0,        // 1.0 = perfect, 0.0 = failed
  "feedback": "...",       // natural language description of what needs improvement
  "converged": true/false, // whether this iteration is good enough to stop
  "confidence": 0.0-1.0,  // how confident you are in this score
  "tokens_used": 0         // approximate tokens consumed (optional)
}`

// ─── RuleBasedEvaluator ───────────────────────────────────────────────────────

// RuleBasedEvaluator checks assertions and output criteria for binary pass/fail.
type RuleBasedEvaluator struct {
	Assertions    []json.RawMessage // assertion JSON blobs
	PassThreshold float64           // fraction of assertions that must pass (0.0-1.0)
}

// NewRuleBasedEvaluator creates a RuleBasedEvaluator.
func NewRuleBasedEvaluator(assertions []json.RawMessage, passThreshold float64) *RuleBasedEvaluator {
	if passThreshold <= 0 {
		passThreshold = 1.0
	}
	return &RuleBasedEvaluator{
		Assertions:    assertions,
		PassThreshold: passThreshold,
	}
}

// Name returns the evaluator name.
func (e *RuleBasedEvaluator) Name() string { return "rule-based" }

// Evaluate checks all assertions against the body outputs.
func (e *RuleBasedEvaluator) Evaluate(iteration int, ctx *engine.Context, bodyOutputs map[string]any) (
	float64, string, map[string]any, error) {

	if len(e.Assertions) == 0 {
		return 1.0, "no assertions defined — treating as pass", nil, nil
	}

	var passed, failed int
	var reasons []string

	for i, raw := range e.Assertions {
		var assertion assertionSpec
		if err := json.Unmarshal(raw, &assertion); err != nil {
			failed++
			reasons = append(reasons, fmt.Sprintf("assertion[%d]: malformed: %v", i, err))
			continue
		}

		ok, reason := e.checkAssertion(assertion, bodyOutputs)
		if ok {
			passed++
		} else {
			failed++
			if reason != "" {
				reasons = append(reasons, reason)
			} else {
				reasons = append(reasons, fmt.Sprintf("assertion[%d](%s) failed", i, assertion.Type))
			}
		}
	}

	total := len(e.Assertions)
	score := float64(passed) / float64(total)
	thresholdMet := score >= e.PassThreshold

	feedback := "all assertions passed"
	if failed > 0 {
		feedback = fmt.Sprintf("%d/%d assertions passed: %s", passed, total, strings.Join(reasons, "; "))
	}

	metadata := map[string]any{
		"passed":        passed,
		"failed":        failed,
		"total":         total,
		"threshold":     e.PassThreshold,
		"threshold_met": thresholdMet,
	}

	return score, feedback, metadata, nil
}

// assertionSpec describes a single assertion.
type assertionSpec struct {
	Type    string `json:"type"`    // "field_exists", "field_equals", "field_matches", "field_compare"
	Expr    string `json:"expr"`   // regex pattern for field_matches
	Message string `json:"message"` // custom message on failure
	Field   string `json:"field"`  // dot-notation path in bodyOutputs
	Value   any    `json:"value"`  // expected value
	Op      string `json:"op"`    // comparison operator: "eq", "ne", "gt", "lt", "ge", "le"
}

// checkAssertion evaluates a single assertion against outputs.
func (e *RuleBasedEvaluator) checkAssertion(a assertionSpec, outputs map[string]any) (bool, string) {
	switch a.Type {
	case "field_exists":
		if a.Field == "" {
			return false, "field_exists requires 'field'"
		}
		val, ok := outputs[a.Field]
		if !ok || val == nil {
			return false, fmt.Sprintf("field %q does not exist", a.Field)
		}
		return true, ""

	case "field_equals":
		if a.Field == "" {
			return false, "field_equals requires 'field'"
		}
		val, ok := outputs[a.Field]
		if !ok {
			return false, fmt.Sprintf("field %q not found", a.Field)
		}
		if fmt.Sprintf("%v", val) != fmt.Sprintf("%v", a.Value) {
			return false, fmt.Sprintf("field %q: got %v, want %v", a.Field, val, a.Value)
		}
		return true, ""

	case "field_matches":
		if a.Field == "" || a.Expr == "" {
			return false, "field_matches requires 'field' and 'expr'"
		}
		val, ok := outputs[a.Field]
		if !ok {
			return false, fmt.Sprintf("field %q not found", a.Field)
		}
		valStr, ok := val.(string)
		if !ok {
			return false, fmt.Sprintf("field %q is not a string", a.Field)
		}
		if !strings.Contains(valStr, a.Expr) {
			return false, fmt.Sprintf("field %q does not match %q", a.Field, a.Expr)
		}
		return true, ""

	case "field_compare":
		if a.Field == "" || a.Op == "" {
			return false, "field_compare requires 'field' and 'op'"
		}
		val, ok := outputs[a.Field]
		if !ok {
			return false, fmt.Sprintf("field %q not found", a.Field)
		}
		ok2, reason := compare(val, a.Op, a.Value)
		if !ok2 {
			return false, fmt.Sprintf("field %q: %s", a.Field, reason)
		}
		return true, ""

	default:
		return false, fmt.Sprintf("unknown assertion type: %s", a.Type)
	}
}

// compare compares a value using an operator.
func compare(val any, op string, expected any) (bool, string) {
	var valF float64
	var ok bool

	switch v := val.(type) {
	case float64:
		valF = v
		ok = true
	case int:
		valF = float64(v)
		ok = true
	case int64:
		valF = float64(v)
		ok = true
	case string:
		if op == "eq" {
			return v == expected.(string), ""
		}
		if op == "ne" {
			return v != expected.(string), ""
		}
		return false, fmt.Sprintf("op %q not supported for string", op)
	}
	if !ok {
		return false, fmt.Sprintf("cannot compare type %T", val)
	}

	var expF float64
	switch e := expected.(type) {
	case float64:
		expF = e
	case int:
		expF = float64(e)
	case string:
		var err error
		expF, err = strconv.ParseFloat(e, 64)
		if err != nil {
			return false, fmt.Sprintf("cannot parse expected value %q", e)
		}
	}

	switch op {
	case "eq":
		return valF == expF, ""
	case "ne":
		return valF != expF, ""
	case "gt":
		return valF > expF, ""
	case "ge":
		return valF >= expF, ""
	case "lt":
		return valF < expF, ""
	case "le":
		return valF <= expF, ""
	default:
		return false, fmt.Sprintf("unknown op: %s", op)
	}
}

// ─── MockEvaluator ───────────────────────────────────────────────────────────

// MockEvaluator is a test-friendly evaluator with programmable responses.
type MockEvaluator struct {
	NameVal     string
	ScoreVal    float64
	FeedbackVal string
	MetadataVal map[string]any
	ErrVal      error
	CallCount   int
	Calls       []MockEvalCall
}

// MockEvalCall records a single Evaluate invocation.
type MockEvalCall struct {
	Iteration   int
	Context     *engine.Context
	BodyOutputs map[string]any
}

// NewMockEvaluator creates a MockEvaluator with default values.
func NewMockEvaluator() *MockEvaluator {
	return &MockEvaluator{
		NameVal:     "mock",
		ScoreVal:    0.75,
		FeedbackVal: "mock feedback",
		MetadataVal: map[string]any{"mock": true},
	}
}

// Name returns the mock name.
func (e *MockEvaluator) Name() string { return e.NameVal }

// Evaluate records the call and returns the programmed response.
func (e *MockEvaluator) Evaluate(iteration int, ctx *engine.Context, bodyOutputs map[string]any) (
	float64, string, map[string]any, error) {
	e.CallCount++
	e.Calls = append(e.Calls, MockEvalCall{
		Iteration:   iteration,
		Context:     ctx,
		BodyOutputs: bodyOutputs,
	})
	return e.ScoreVal, e.FeedbackVal, e.MetadataVal, e.ErrVal
}

// ─── Registry ───────────────────────────────────────────────────────────────

// Registry manages Evaluator plugins by name.
type Registry struct {
	evals map[string]Evaluator
}

// NewEvaluatorRegistry creates an empty evaluator registry.
func NewEvaluatorRegistry() *Registry {
	return &Registry{evals: make(map[string]Evaluator)}
}

// Register adds an evaluator to the registry.
func (r *Registry) Register(e Evaluator) {
	r.evals[e.Name()] = e
}

// Get returns the named evaluator.
func (r *Registry) Get(name string) (Evaluator, error) {
	e, ok := r.evals[name]
	if !ok {
		return nil, fmt.Errorf("evaluator not found: %q", name)
	}
	return e, nil
}

// MustGet returns the named evaluator, panics if not found.
func (r *Registry) MustGet(name string) Evaluator {
	e, err := r.Get(name)
	if err != nil {
		panic(err)
	}
	return e
}