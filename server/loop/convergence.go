package loop

import (
	"fmt"
	"strconv"
	"strings"
)

type TerminationReason string

const (
	TerminationReasonConverged     TerminationReason = "converged"
	TerminationReasonMaxIterations TerminationReason = "max_iter"
	TerminationReasonNoProgress    TerminationReason = "no_progress"
	TerminationReasonError         TerminationReason = "error"
	TerminationReasonNotYet        TerminationReason = "not_yet"
)

// ConvergenceChecker checks loop termination conditions.
// Epic 3.4: 分数阈值 / max_iter / 无进展（no-progress）三条件.
type ConvergenceChecker struct {
	scoreThreshold      float64
	maxIterations       int
	noProgressWindow    int
	minImprovementDelta float64
}

// NewConvergenceChecker creates a ConvergenceChecker with thresholds.
func NewConvergenceChecker(scoreThreshold float64, maxIterations, noProgressWindow int) *ConvergenceChecker {
	return &ConvergenceChecker{
		scoreThreshold:      scoreThreshold,
		maxIterations:       maxIterations,
		noProgressWindow:    noProgressWindow,
		minImprovementDelta: 0.01, // default: 1% minimum improvement to avoid plateau detection
	}
}

// CheckScoreThreshold returns true if score meets or exceeds threshold.
func (c *ConvergenceChecker) CheckScoreThreshold(score, threshold float64) bool {
	return score >= threshold
}

// CheckMaxIterations returns true if current iteration count meets or exceeds max.
func (c *ConvergenceChecker) CheckMaxIterations(current, max int) bool {
	return current >= max
}

// CheckNoProgress returns true if there has been no meaningful score improvement
// in the last N iterations (window). All consecutive pairs in the window must have
// improvement < minImprovementDelta for it to return true.
func (c *ConvergenceChecker) CheckNoProgress(snapshots []IterationSnapshot, window int) bool {
	if window <= 0 || len(snapshots) < window+1 {
		return false
	}
	recent := snapshots[len(snapshots)-window-1:]
	for i := 1; i < len(recent); i++ {
		if recent[i].Score-recent[i-1].Score >= c.minImprovementDelta {
			return false
		}
	}
	return true
}

// TerminateIf determines whether the loop should terminate based on current state.
func (c *ConvergenceChecker) TerminateIf(snapshots []IterationSnapshot, currentIteration int, currentScore float64) (bool, TerminationReason) {
	// Check score threshold convergence.
	if c.scoreThreshold > 0 && c.CheckScoreThreshold(currentScore, c.scoreThreshold) {
		return true, TerminationReasonConverged
	}

	// Check max iterations after score convergence so a final successful
	// iteration at the limit is reported as converged.
	if c.CheckMaxIterations(currentIteration, c.maxIterations) {
		return true, TerminationReasonMaxIterations
	}

	// Check no progress.
	if c.noProgressWindow > 0 && c.CheckNoProgress(snapshots, c.noProgressWindow) {
		return true, TerminationReasonNoProgress
	}

	return false, TerminationReasonNotYet
}

// ─── Expression Parser ────────────────────────────────────────────────────────

// ExpressionParser parses and evaluates convergence condition expressions.
// Supports: score >= 0.9, !test.passed, score > prev_score, score < threshold
// Epic 3.4: Parse convergence condition expression.
type ExpressionParser struct{}

// NewExpressionParser creates a new expression parser.
func NewExpressionParser() *ExpressionParser {
	return &ExpressionParser{}
}

// EvaluateExpression evaluates a boolean expression against variable map.
// Supports operators: ==, !=, >, <, >=, <=, &&, ||, !
// Supports variables: score, prev_score, test.passed, test.score, etc.
func (p *ExpressionParser) EvaluateExpression(expr string, vars map[string]any) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false, fmt.Errorf("empty expression")
	}

	result, err := p.parseExpr(expr, vars)
	if err != nil {
		return false, err
	}
	return result, nil
}

// parseExpr handles && and || at the top level.
func (p *ExpressionParser) parseExpr(expr string, vars map[string]any) (bool, error) {
	expr = strings.TrimSpace(expr)

	// Handle negation first.
	if strings.HasPrefix(expr, "!") {
		sub := strings.TrimSpace(expr[1:])
		res, err := p.parseExpr(sub, vars)
		return !res, err
	}

	// Handle &&.
	if idx := findTopLevelOp(expr, "&&"); idx >= 0 {
		left, right := strings.TrimSpace(expr[:idx]), strings.TrimSpace(expr[idx+2:])
		l, err := p.parseExpr(left, vars)
		if err != nil {
			return false, err
		}
		r, err := p.parseExpr(right, vars)
		return l && r, err
	}

	// Handle ||.
	if idx := findTopLevelOp(expr, "||"); idx >= 0 {
		left, right := strings.TrimSpace(expr[:idx]), strings.TrimSpace(expr[idx+2:])
		l, err := p.parseExpr(left, vars)
		if err != nil {
			return false, err
		}
		r, err := p.parseExpr(right, vars)
		return l || r, err
	}

	// Handle comparison operators.
	return p.parseComparison(expr, vars)
}

// parseComparison handles ==, !=, >, <, >=, <=.
func (p *ExpressionParser) parseComparison(expr string, vars map[string]any) (bool, error) {
	expr = strings.TrimSpace(expr)

	// Check for comparison operators in order of specificity.
	// Use regex to find the operator.
	// Pattern: var op value
	compOps := []string{"==", "!=", ">=", "<=", ">", "<"}
	for _, op := range compOps {
		if idx := strings.Index(expr, op); idx >= 0 {
			left := strings.TrimSpace(expr[:idx])
			right := strings.TrimSpace(expr[idx+len(op):])
			return p.evalComparison(left, op, right, vars)
		}
	}

	// No comparison operator found - treat as variable existence/truthiness check.
	val := p.resolveVar(leftVar(expr), vars)
	return isTruthy(val), nil
}

// evalComparison evaluates a comparison between left var and right value.
func (p *ExpressionParser) evalComparison(leftVarName, op, rightStr string, vars map[string]any) (bool, error) {
	leftVal := p.resolveVar(leftVarName, vars)
	rightVal := p.parseValue(rightStr, vars)

	switch op {
	case "==":
		return compareEq(leftVal, rightVal), nil
	case "!=":
		return !compareEq(leftVal, rightVal), nil
	case ">":
		return compareNum(leftVal, rightVal) > 0, nil
	case ">=":
		return compareNum(leftVal, rightVal) >= 0, nil
	case "<":
		return compareNum(leftVal, rightVal) < 0, nil
	case "<=":
		return compareNum(leftVal, rightVal) <= 0, nil
	default:
		return false, fmt.Errorf("unknown operator: %s", op)
	}
}

// resolveVar resolves a variable name from vars map.
// Supports dot notation: test.passed -> vars["test"]["passed"] or vars["test.passed"]
func (p *ExpressionParser) resolveVar(name string, vars map[string]any) any {
	name = strings.TrimSpace(name)
	// Try direct key first.
	if v, ok := vars[name]; ok {
		return v
	}
	// Try dot notation.
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		if raw, ok := vars[parts[0]]; ok {
			if subMap, ok := raw.(map[string]any); ok {
				return subMap[parts[1]]
			}
		}
	}
	return nil
}

// parseValue parses a value string into an appropriate Go type.
// If the string matches a variable name in vars, it resolves that variable.
func (p *ExpressionParser) parseValue(s string, vars map[string]any) any {
	s = strings.TrimSpace(s)
	// Remove quotes.
	s = strings.Trim(s, "\"'")

	// Try bool.
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}

	// Try float64.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}

	// Check if it's a variable reference.
	if val := p.resolveVar(s, vars); val != nil {
		return val
	}

	// Return as string.
	return s
}

// leftVar extracts the variable name from an expression (everything before the operator).
// This is a helper for the case where no operator was found.
func leftVar(expr string) string {
	return strings.TrimSpace(expr)
}

// isTruthy returns true if val is truthy (non-nil, non-false, non-zero).
func isTruthy(val any) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case int:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}

// compareEq compares two values for equality.
func compareEq(a, b any) bool {
	if a == nil || b == nil {
		return a == b
	}
	// Numeric comparison.
	if aNum, ok := toFloat64(a); ok {
		if bNum, ok := toFloat64(b); ok {
			return aNum == bNum
		}
	}
	// String comparison.
	if aStr, ok := a.(string); ok {
		if bStr, ok := b.(string); ok {
			return aStr == bStr
		}
	}
	// Generic comparison.
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// compareNum compares two numeric values.
func compareNum(a, b any) float64 {
	aNum, aOK := toFloat64(a)
	bNum, bOK := toFloat64(b)
	if !aOK || !bOK {
		return 0
	}
	if aNum > bNum {
		return 1
	}
	if aNum < bNum {
		return -1
	}
	return 0
}

// toFloat64 converts a value to float64 if possible.
func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// findTopLevelOp finds an operator at the top level (not inside nested parens).
// This is a simplified implementation that doesn't handle parentheses.
func findTopLevelOp(expr, op string) int {
	// Find the operator that is not inside a quote.
	inQuote := false
	quoteChar := byte(0)
	for i := 0; i <= len(expr)-len(op); i++ {
		c := expr[i]
		if !inQuote && (c == '"' || c == '\'') {
			inQuote = true
			quoteChar = c
		} else if inQuote && c == quoteChar {
			inQuote = false
		}
		if !inQuote && strings.HasPrefix(expr[i:], op) {
			return i
		}
	}
	return -1
}

// ─── Expression Evaluator with built-in variables ──────────────────────────────

// EvalContext holds context variables for expression evaluation.
type EvalContext struct {
	Score      float64
	PrevScore  *float64
	Iteration  int
	MaxIter    int
	TestPassed bool
	TestScore  float64
	Metadata   map[string]any
}

// NewEvalContext creates an EvalContext from a loop state and snapshots.
func NewEvalContext(state *LoopState, snapshots []*IterationSnapshot) *EvalContext {
	ec := &EvalContext{
		Iteration: state.Iteration,
		MaxIter:   state.MaxIters,
		Score:     0.0,
		Metadata:  make(map[string]any),
	}
	if state.EvalScore != nil {
		ec.Score = *state.EvalScore
	}
	if state.PrevScore != nil {
		ec.PrevScore = state.PrevScore
	}
	// Get test info from latest snapshot metadata.
	if len(snapshots) > 0 {
		latest := snapshots[len(snapshots)-1]
		if latest.Metadata != nil {
			if passed, ok := latest.Metadata["test_passed"].(bool); ok {
				ec.TestPassed = passed
			}
			if score, ok := latest.Metadata["test_score"].(float64); ok {
				ec.TestScore = score
			}
		}
	}
	return ec
}

// ToMap converts EvalContext to a map for expression evaluation.
func (ec *EvalContext) ToMap() map[string]any {
	m := map[string]any{
		"score":     ec.Score,
		"iteration": float64(ec.Iteration),
		"max_iter":  float64(ec.MaxIter),
	}
	if ec.PrevScore != nil {
		m["prev_score"] = *ec.PrevScore
	}
	m["test.passed"] = ec.TestPassed
	m["test.score"] = ec.TestScore
	for k, v := range ec.Metadata {
		m[k] = v
	}
	return m
}

// EvaluateWithContext evaluates an expression using loop state context.
func (p *ExpressionParser) EvaluateWithContext(expr string, state *LoopState, snapshots []*IterationSnapshot) (bool, error) {
	ctx := NewEvalContext(state, snapshots)
	return p.EvaluateExpression(expr, ctx.ToMap())
}
