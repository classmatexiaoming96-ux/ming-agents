package engine

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
	"github.com/ming-agents/server/workflow"
)

// Engine is the core execution engine for WDL runs.
type Engine struct {
	store    *store.Store
	registry *adapter.Registry
	ctx      *Context // shared execution context
}

// NewEngine creates a new execution engine.
func NewEngine(s *store.Store, reg *adapter.Registry) *Engine {
	return &Engine{
		store:    s,
		registry: reg,
		ctx:      NewContext(),
	}
}

// CompileResult is the result of compiling a WDL into a run plan.
type CompileResult struct {
	Run   *domain.Run
	Steps []*domain.Step
	DAG   *workflow.DAG
}

// Compile parses and compiles a WDL document into a run plan.
// It validates the WDL, builds the DAG, and creates run+step DB records.
func (e *Engine) Compile(wdlSrc string) (*CompileResult, error) {
	var wdl workflow.WDL
	if err := json.Unmarshal([]byte(wdlSrc), &wdl); err != nil {
		return nil, fmt.Errorf("parse WDL: %w", err)
	}
	if err := wdl.Validate(); err != nil {
		return nil, fmt.Errorf("validate WDL: %w", err)
	}

	// Build DAG.
	dag := workflow.NewDAG()
	if err := dag.BuildFromWDL(wdl.Steps); err != nil {
		return nil, fmt.Errorf("build DAG: %w", err)
	}
	if dag.DetectCycle() {
		return nil, fmt.Errorf("cyclic dependency detected")
	}

	// Create run.
	run := &domain.Run{
		ID:          uuid.New(),
		Name:        "run-" + uuid.New().String()[:8],
		WDLVersion:  wdl.Version,
		Status:      domain.RunStatusPending,
		MaxParallel: 4,
	}
	run.WDLSource = sql.NullString{String: wdlSrc, Valid: true}

	if err := e.store.CreateRun(run); err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}

	// Create steps.
	steps := make([]*domain.Step, 0, len(wdl.Steps))
	for _, ws := range wdl.Steps {
		st := &domain.Step{
			ID:         uuid.New(),
			RunID:      run.ID,
			Name:       ws.Name,
			StepType:   domain.StepType(ws.StepType),
			AdapterKey: ws.Adapter,
			Status:     domain.StepStatusPending,
			Iteration:  0,
			Attempt:    1,
		}
		if ws.Inputs != nil {
			inputs := make(map[string]any, len(ws.Inputs)+1)
			for k, v := range ws.Inputs {
				inputs[k] = v
			}
			if ws.Adapter != "" {
				inputs["_adapter"] = ws.Adapter
			}
			st.InputsJSON = sql.NullString{String: toJSON(inputs), Valid: true}
		} else if ws.Adapter != "" {
			st.InputsJSON = sql.NullString{String: toJSON(map[string]any{"_adapter": ws.Adapter}), Valid: true}
		}
		if ws.When != nil && *ws.When != "" {
			st.When = sql.NullString{String: *ws.When, Valid: true}
		}
		if err := e.store.CreateStep(st); err != nil {
			return nil, fmt.Errorf("create step: %w", err)
		}
		steps = append(steps, st)
	}

	return &CompileResult{Run: run, Steps: steps, DAG: dag}, nil
}

// Context holds the shared execution context (step outputs for binding).
type Context struct {
	mu        sync.RWMutex
	outputs   map[string]map[string]any  // stepName → outputKey → value
	artifacts map[string]*store.Artifact // "stepName/key" → artifact
}

func NewContext() *Context {
	return &Context{
		outputs:   make(map[string]map[string]any),
		artifacts: make(map[string]*store.Artifact),
	}
}

// SetOutput sets an output value in the context.
func (c *Context) SetOutput(stepName, key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.outputs[stepName] == nil {
		c.outputs[stepName] = make(map[string]any)
	}
	c.outputs[stepName][key] = value
}

// GetOutput retrieves an output value from the context.
func (c *Context) GetOutput(stepName, key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.outputs[stepName] == nil {
		return nil, false
	}
	v, ok := c.outputs[stepName][key]
	return v, ok
}

// RenderTemplate renders a string with ${ref} template references using the context.
func (c *Context) RenderTemplate(s string) string {
	re := regexp.MustCompile(`\$\{([^}]+)\}`)
	return re.ReplaceAllStringFunc(s, func(match string) string {
		ref := match[2 : len(match)-1]
		parts := strings.SplitN(ref, ".", 2)
		if len(parts) != 2 {
			return match
		}
		stepName, key := parts[0], parts[1]
		if v, ok := c.GetOutput(stepName, key); ok {
			if vs, ok := v.(string); ok {
				return vs
			}
			if bs, err := json.Marshal(v); err == nil {
				return string(bs)
			}
		}
		return match
	})
}

// RenderTemplateWithValidation renders a string and returns an error if any
// ${...} placeholders remain unresolved.
func (c *Context) RenderTemplateWithValidation(s string) (string, error) {
	re := regexp.MustCompile(`\$\{([^}]+)\}`)
	matches := re.FindAllString(s, -1)
	rendered := c.RenderTemplate(s)
	for _, match := range matches {
		if strings.Contains(rendered, match) {
			return rendered, fmt.Errorf("unresolved placeholder: %s", match)
		}
	}
	return rendered, nil
}

// HasUnresolvedPlaceholder checks if a string contains any ${...} placeholders.
func (c *Context) HasUnresolvedPlaceholder(s string) bool {
	re := regexp.MustCompile(`\$\{([^}]+)\}`)
	return re.MatchString(s)
}

// GetOutputs returns all outputs for a given step.
func (c *Context) GetOutputs(stepName string) (map[string]any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.outputs[stepName] == nil {
		return nil, false
	}
	out := make(map[string]any, len(c.outputs[stepName]))
	for k, v := range c.outputs[stepName] {
		out[k] = v
	}
	return out, true
}

// GetAll returns a copy of all step outputs (for evaluation/debugging).
func (c *Context) GetAll() map[string]map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]map[string]any)
	for k, v := range c.outputs {
		copied := make(map[string]any, len(v))
		for outKey, outVal := range v {
			copied[outKey] = outVal
		}
		out[k] = copied
	}
	return out
}

// GetFanOutItem resolves a specific index from an upstream fan-out output.
// Used when a downstream step needs the i-th item from an upstream array output.
func (c *Context) GetFanOutItem(stepName, key string, index int) (any, bool) {
	v, ok := c.GetOutput(stepName, key)
	if !ok {
		return nil, false
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	if index < 0 || index >= len(arr) {
		return nil, false
	}
	return arr[index], true
}

// ─── Condition Evaluation ─────────────────────────────────────────────────────
// Epic 2.7: Conditional step / skip propagation.

// EvaluateCondition evaluates a when-expression and returns true if the step should proceed.
// It supports:
//   - ${step.output} references
//   - Comparison operators: ==, !=, >, <, >=, <=
//   - Logical operators: &&, ||, !
//   - Existence checks: exists, !exists
//   - String, number, and boolean literals
//   - Unresolved references evaluate to false
func (c *Context) EvaluateCondition(when string) (bool, error) {
	if when == "" {
		return true, nil
	}
	return evaluateBoolExpr(when, c)
}

// evaluateBoolExpr is the recursive boolean expression evaluator.
func evaluateBoolExpr(expr string, ctx *Context) (bool, error) {
	expr = strings.TrimSpace(expr)

	// Handle parentheses by finding matching pairs
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		return evaluateBoolExpr(expr[1:len(expr)-1], ctx)
	}

	// Handle negation.
	if strings.HasPrefix(expr, "!") {
		sub := strings.TrimSpace(expr[1:])
		res, err := evaluateBoolExpr(sub, ctx)
		return !res, err
	}

	// Handle || at top level (lowest precedence — must be checked before &&).
	if idx := findOpOutsideParens(expr, "||"); idx >= 0 {
		left, right := strings.TrimSpace(expr[:idx]), strings.TrimSpace(expr[idx+2:])
		l, err := evaluateBoolExpr(left, ctx)
		if err != nil {
			return false, err
		}
		r, err := evaluateBoolExpr(right, ctx)
		return l || r, err
	}

	// Handle && (higher precedence than ||).
	if idx := findOpOutsideParens(expr, "&&"); idx >= 0 {
		left, right := strings.TrimSpace(expr[:idx]), strings.TrimSpace(expr[idx+2:])
		l, err := evaluateBoolExpr(left, ctx)
		if err != nil {
			return false, err
		}
		r, err := evaluateBoolExpr(right, ctx)
		return l && r, err
	}

	// Handle exists / !exists
	if strings.HasPrefix(expr, "exists ") {
		varName := strings.TrimSpace(expr[7:])
		v := resolveVar(varName, ctx)
		return v != nil, nil
	}
	if strings.HasPrefix(expr, "!exists ") {
		varName := strings.TrimSpace(expr[8:])
		v := resolveVar(varName, ctx)
		return v == nil, nil
	}

	// Handle >= comparison
	if idx := findOpOutsideParens(expr, ">="); idx >= 0 {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+2:])
		return compareValues(left, right, ">=", ctx)
	}

	// Handle <= comparison
	if idx := findOpOutsideParens(expr, "<="); idx >= 0 {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+2:])
		return compareValues(left, right, "<=", ctx)
	}

	// Handle > comparison (but not >=)
	if idx := findOpOutsideParens(expr, ">"); idx >= 0 {
		// Make sure it's not >=
		if idx+1 < len(expr) && expr[idx+1] == '=' {
			// This is >=, skip
		} else {
			left := strings.TrimSpace(expr[:idx])
			right := strings.TrimSpace(expr[idx+1:])
			return compareValues(left, right, ">", ctx)
		}
	}

	// Handle < comparison (but not <=)
	if idx := findOpOutsideParens(expr, "<"); idx >= 0 {
		if idx+1 < len(expr) && expr[idx+1] == '=' {
			// This is <=, skip
		} else {
			left := strings.TrimSpace(expr[:idx])
			right := strings.TrimSpace(expr[idx+1:])
			return compareValues(left, right, "<", ctx)
		}
	}

	// Handle == comparison
	if idx := findOpOutsideParens(expr, "=="); idx >= 0 {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+2:])
		return compareValues(left, right, "==", ctx)
	}

	// Handle != comparison
	if idx := findOpOutsideParens(expr, "!="); idx >= 0 {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+2:])
		return compareValues(left, right, "!=", ctx)
	}

	// Treat as variable existence check (for simple boolean variables).
	v := resolveVar(expr, ctx)
	if v == nil {
		return false, nil
	}
	if b, ok := v.(bool); ok {
		return b, nil
	}
	// Non-nil, non-bool value is truthy.
	return true, nil
}

// findOpOutsideParens finds an operator at the top level (not inside parentheses).
func findOpOutsideParens(expr, op string) int {
	parens := 0
	for i := 0; i <= len(expr)-len(op); i++ {
		if expr[i] == '(' {
			parens++
		} else if expr[i] == ')' {
			parens--
		} else if parens == 0 && expr[i:i+len(op)] == op {
			return i
		}
	}
	return -1
}

// resolveVar resolves a variable reference from the context.
// Supports step.output_key format and bare variable names.
func resolveVar(name string, ctx *Context) any {
	name = strings.TrimSpace(name)
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		v, _ := ctx.GetOutput(parts[0], parts[1])
		return v
	}
	// Check if it's a top-level key in any step outputs.
	for _, outputs := range ctx.GetAll() {
		if v, ok := outputs[name]; ok {
			return v
		}
	}
	return nil
}

// resolveValue resolves a value from an expression, handling literals and variables.
// It supports:
//   - Boolean literals: true, false
//   - Quoted string literals: "value" or 'value'
//   - Unquoted string/number literals
//   - Variable references (step.output_key or bare name)
func resolveValue(expr string, ctx *Context) any {
	expr = strings.TrimSpace(expr)

	// Boolean literals
	if expr == "true" {
		return true
	}
	if expr == "false" {
		return false
	}

	// Quoted string literal
	if (strings.HasPrefix(expr, `"`) && strings.HasSuffix(expr, `"`)) ||
		(strings.HasPrefix(expr, `'`) && strings.HasSuffix(expr, `'`)) {
		return expr[1 : len(expr)-1]
	}

	// Numeric literal
	if f, err := strconv.ParseFloat(expr, 64); err == nil {
		return f
	}

	// Unquoted: if it looks like an identifier (contains only alphanumeric/underscore/dot),
	// try variable resolution first. Otherwise treat as an unquoted string literal.
	// This allows: check.status == ready  (ready is string literal "ready")
	//               upstream.name == Alice  (Alice is string literal "Alice")
	if isIdent(expr) {
		if v := resolveVar(expr, ctx); v != nil {
			return v
		}
	}

	// Fall through: treat as unquoted string literal
	return expr
}

// isIdent returns true if expr looks like an identifier (alphanumeric + underscore + dot).
func isIdent(expr string) bool {
	for _, c := range expr {
		if c != '_' && c != '.' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return len(expr) > 0
}

// compareValues compares two values using the given operator.
func compareValues(leftExpr, rightExpr, op string, ctx *Context) (bool, error) {
	lVal := resolveValue(leftExpr, ctx)
	rVal := resolveValue(rightExpr, ctx)

	// Handle nil values (unresolved references).
	if lVal == nil || rVal == nil {
		if op == "!=" {
			return lVal != rVal, nil
		}
		return false, nil // Unresolved references don't satisfy any comparison
	}

	// Try numeric comparison for >, <, >=, <=
	if op != "==" && op != "!=" {
		if lNum, lOK := toFloat64(lVal); lOK {
			if rNum, rOK := toFloat64(rVal); rOK {
				switch op {
				case ">":
					return lNum > rNum, nil
				case "<":
					return lNum < rNum, nil
				case ">=":
					return lNum >= rNum, nil
				case "<=":
					return lNum <= rNum, nil
				}
			}
		}
		// Cannot compare non-numerics with these operators.
		return false, nil
	}

	// String comparison for ==, !=
	lStr := fmt.Sprintf("%v", lVal)
	rStr := fmt.Sprintf("%v", rVal)
	rStr = strings.Trim(rStr, `"'`)

	switch op {
	case "==":
		return lStr == rStr, nil
	case "!=":
		return lStr != rStr, nil
	}
	return false, fmt.Errorf("unsupported operator: %s", op)
}

func toJSON(v any) string {
	bs, _ := json.Marshal(v)
	return string(bs)
}
