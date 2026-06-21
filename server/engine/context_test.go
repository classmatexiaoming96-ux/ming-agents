package engine

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ming-agents/server/domain"
)

// ─── Context & Template Rendering Tests ─────────────────────────────────────

func TestContextSetAndGetOutput(t *testing.T) {
	ctx := NewContext()

	// Set a simple output.
	ctx.SetOutput("step_a", "result", "hello world")
	v, ok := ctx.GetOutput("step_a", "result")
	if !ok {
		t.Fatal("expected to get output for step_a.result")
	}
	if v != "hello world" {
		t.Errorf("expected 'hello world', got %v", v)
	}
}

func TestContextGetOutputNonexistent(t *testing.T) {
	ctx := NewContext()
	_, ok := ctx.GetOutput("nonexistent", "key")
	if ok {
		t.Error("expected not found for nonexistent step")
	}
}

func TestContextGetOutputs(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("step_a", "x", 1)
	ctx.SetOutput("step_a", "y", 2)

	outputs, ok := ctx.GetOutputs("step_a")
	if !ok {
		t.Fatal("expected to get outputs for step_a")
	}
	if len(outputs) != 2 {
		t.Errorf("expected 2 outputs, got %d", len(outputs))
	}
	if outputs["x"] != 1 || outputs["y"] != 2 {
		t.Errorf("unexpected outputs: %+v", outputs)
	}
}

func TestContextGetOutputsNonexistent(t *testing.T) {
	ctx := NewContext()
	_, ok := ctx.GetOutputs("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent step")
	}
}

func TestRenderTemplateSimpleString(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("upstream", "name", "Alice")

	rendered := ctx.RenderTemplate("Hello, ${upstream.name}!")
	if rendered != "Hello, Alice!" {
		t.Errorf("expected 'Hello, Alice!', got %q", rendered)
	}
}

func TestRenderTemplateInteger(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("count_step", "count", 42)

	rendered := ctx.RenderTemplate("Found ${count_step.count} items")
	if rendered != "Found 42 items" {
		t.Errorf("expected 'Found 42 items', got %q", rendered)
	}
}

func TestRenderTemplateBoolean(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("check_step", "passed", true)

	rendered := ctx.RenderTemplate("Tests passed: ${check_step.passed}")
	if rendered != "Tests passed: true" {
		t.Errorf("expected 'Tests passed: true', got %q", rendered)
	}
}

func TestRenderTemplateJSONObject(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("meta_step", "data", map[string]any{"name": "test", "value": 123})

	rendered := ctx.RenderTemplate("Data: ${meta_step.data}")
	// JSON marshal converts map to JSON string.
	if !strings.Contains(rendered, `"name":"test"`) {
		t.Errorf("expected JSON object in rendered output, got %q", rendered)
	}
}

func TestRenderTemplateArray(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("list_step", "items", []any{"a", "b", "c"})

	rendered := ctx.RenderTemplate("Items: ${list_step.items}")
	// JSON marshal converts array to JSON string.
	if !strings.Contains(rendered, `"a"`) || !strings.Contains(rendered, `"b"`) {
		t.Errorf("expected JSON array in rendered output, got %q", rendered)
	}
}

func TestRenderTemplateMultiplePlaceholders(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("step_a", "x", "hello")
	ctx.SetOutput("step_b", "y", "world")

	rendered := ctx.RenderTemplate("${step_a.x} ${step_b.y}")
	if rendered != "hello world" {
		t.Errorf("expected 'hello world', got %q", rendered)
	}
}

func TestRenderTemplateUnresolvedPlaceholder(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("step_a", "x", "value")

	// "step_a.missing" is not set.
	rendered := ctx.RenderTemplate("Hello ${step_a.x}, missing ${step_a.missing}")
	if rendered != "Hello value, missing ${step_a.missing}" {
		t.Errorf("unresolved placeholder should remain as-is, got %q", rendered)
	}
}

func TestRenderTemplateNoPlaceholders(t *testing.T) {
	ctx := NewContext()
	rendered := ctx.RenderTemplate("No placeholders here")
	if rendered != "No placeholders here" {
		t.Errorf("expected unchanged string, got %q", rendered)
	}
}

func TestRenderTemplateEmptyString(t *testing.T) {
	ctx := NewContext()
	rendered := ctx.RenderTemplate("")
	if rendered != "" {
		t.Errorf("expected empty string, got %q", rendered)
	}
}

func TestHasUnresolvedPlaceholder(t *testing.T) {
	ctx := NewContext()

	// HasUnresolvedPlaceholder just checks if the ${...} pattern exists.
	// It does NOT check if the placeholder is actually resolved in context.
	if !ctx.HasUnresolvedPlaceholder("Hello ${name}") {
		t.Error("expected true for string with ${...} pattern")
	}
	if ctx.HasUnresolvedPlaceholder("Hello world") {
		t.Error("expected false for string without placeholders")
	}
	// Even if context has the key, the pattern still exists in the string.
	ctx.SetOutput("step", "name", "Alice")
	if !ctx.HasUnresolvedPlaceholder("Hello ${step.name}") {
		t.Error("expected true - pattern exists even if context has the key")
	}
}

// ─── RenderTemplateWithValidation Tests ────────────────────────────────────

func TestRenderTemplateWithValidationSuccess(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("step_a", "value", "resolved")

	rendered, err := ctx.RenderTemplateWithValidation("${step_a.value}")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if rendered != "resolved" {
		t.Errorf("expected 'resolved', got %q", rendered)
	}
}

func TestRenderTemplateWithValidationUnresolvedError(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("step_a", "value", "resolved")

	_, err := ctx.RenderTemplateWithValidation("${step_a.value} and ${step_a.missing}")
	if err == nil {
		t.Fatal("expected error for unresolved placeholder")
	}
	if !strings.Contains(err.Error(), "unresolved placeholder") {
		t.Errorf("expected 'unresolved placeholder' error, got %v", err)
	}
}

func TestRenderTemplateWithValidationNoPlaceholders(t *testing.T) {
	ctx := NewContext()
	_, err := ctx.RenderTemplateWithValidation("No placeholders")
	if err != nil {
		t.Errorf("expected no error for string without placeholders, got %v", err)
	}
}

// ─── Fan-Out Index Access Tests ─────────────────────────────────────────────

func TestGetFanOutItem(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("locate", "files", []any{"file1.txt", "file2.txt", "file3.txt"})

	item, ok := ctx.GetFanOutItem("locate", "files", 1)
	if !ok {
		t.Fatal("expected to get item at index 1")
	}
	if item != "file2.txt" {
		t.Errorf("expected 'file2.txt', got %v", item)
	}
}

func TestGetFanOutItemOutOfBounds(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("locate", "files", []any{"file1.txt", "file2.txt"})

	_, ok := ctx.GetFanOutItem("locate", "files", 10)
	if ok {
		t.Error("expected out-of-bounds to return false")
	}

	_, ok = ctx.GetFanOutItem("locate", "files", -1)
	if ok {
		t.Error("expected negative index to return false")
	}
}

func TestGetFanOutItemNonArray(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("step", "value", "not an array")

	_, ok := ctx.GetFanOutItem("step", "value", 0)
	if ok {
		t.Error("expected non-array to return false")
	}
}

func TestGetFanOutItemNonexistentOutput(t *testing.T) {
	ctx := NewContext()
	_, ok := ctx.GetFanOutItem("nonexistent", "key", 0)
	if ok {
		t.Error("expected nonexistent step to return false")
	}
}

// ─── Fan-Out Scenario Integration Tests ────────────────────────────────────

func TestFanOutIndexResolution(t *testing.T) {
	// Simulate a fan-out scenario:
	// - Upstream "locate" step produces a list of files.
	// - Downstream "fix" step iterates over each file.
	// - For each fan-out iteration, the downstream should access the specific file.

	ctx := NewContext()
	ctx.SetOutput("locate", "files", []any{"src/main.go", "src/util.go", "src/helpers.go"})

	// Simulate fan-out iterations 0, 1, 2.
	for i := 0; i < 3; i++ {
		item, ok := ctx.GetFanOutItem("locate", "files", i)
		if !ok {
			t.Fatalf("iteration %d: expected to get item", i)
		}
		expected := []string{"src/main.go", "src/util.go", "src/helpers.go"}[i]
		if item != expected {
			t.Errorf("iteration %d: expected %q, got %v", i, expected, item)
		}
	}
}

func TestFanOutWithTemplateResolution(t *testing.T) {
	// Test that template rendering works correctly within fan-out context.
	ctx := NewContext()
	ctx.SetOutput("locate", "files", []any{"a.txt", "b.txt", "c.txt"})
	ctx.SetOutput("locate", "_index", 1) // Simulating index tracking in context

	// Template should resolve the list to JSON array string.
	rendered := ctx.RenderTemplate("${locate.files}")
	if !strings.Contains(rendered, "a.txt") {
		t.Errorf("expected list to be rendered, got %q", rendered)
	}

	// Individual item access via fan-out index.
	item, _ := ctx.GetFanOutItem("locate", "files", 0)
	renderedItem := ctx.RenderTemplate(item.(string) + "_processed")
	if renderedItem != "a.txt_processed" {
		t.Errorf("expected 'a.txt_processed', got %q", renderedItem)
	}
}

// ─── Input Resolution Tests (using translator's resolveInputs) ────────────

func TestResolveInputsSimple(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("upstream", "name", "Alice")

	// Simulate step inputs.
	inputs := map[string]any{"greeting": "${upstream.name}"}

	// Use RenderTemplate to resolve inputs (like translator.resolveInputs does).
	resolved := make(map[string]any)
	for k, v := range inputs {
		if s, ok := v.(string); ok {
			resolved[k] = ctx.RenderTemplate(s)
		} else {
			resolved[k] = v
		}
	}

	if resolved["greeting"] != "Alice" {
		t.Errorf("expected 'Alice', got %v", resolved["greeting"])
	}
}

func TestResolveInputsWithArray(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("locate", "files", []any{"file1", "file2"})

	inputs := map[string]any{"target": "${locate.files}"}

	resolved := make(map[string]any)
	for k, v := range inputs {
		if s, ok := v.(string); ok {
			resolved[k] = ctx.RenderTemplate(s)
		} else {
			resolved[k] = v
		}
	}

	// The resolved value should be a JSON string array.
	itemsStr, ok := resolved["target"].(string)
	if !ok {
		t.Fatalf("expected string, got %T", resolved["target"])
	}
	if !strings.Contains(itemsStr, "file1") {
		t.Errorf("expected array string to contain file1, got %q", itemsStr)
	}

	// Parse back to verify it's valid JSON.
	var items []any
	if err := json.Unmarshal([]byte(itemsStr), &items); err != nil {
		t.Fatalf("expected valid JSON array, got %q: %v", itemsStr, err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestResolveInputsWithUnresolved(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("step", "value", "val")

	inputs := map[string]any{"missing": "${step.nonexistent}"}

	resolved := make(map[string]any)
	for k, v := range inputs {
		if s, ok := v.(string); ok {
			resolved[k] = ctx.RenderTemplate(s)
		} else {
			resolved[k] = v
		}
	}

	// Unresolved placeholders remain as-is.
	if resolved["missing"] != "${step.nonexistent}" {
		t.Errorf("expected unresolved placeholder, got %v", resolved["missing"])
	}
}

func TestResolveInputsMultipleReferences(t *testing.T) {
	ctx := NewContext()
	ctx.SetOutput("step_a", "x", "hello")
	ctx.SetOutput("step_b", "y", "world")

	inputs := map[string]any{"combined": "${step_a.x} ${step_b.y}"}

	resolved := make(map[string]any)
	for k, v := range inputs {
		if s, ok := v.(string); ok {
			resolved[k] = ctx.RenderTemplate(s)
		} else {
			resolved[k] = v
		}
	}

	if resolved["combined"] != "hello world" {
		t.Errorf("expected 'hello world', got %v", resolved["combined"])
	}
}

// ─── Conditional Evaluator Tests ───────────────────────────────────────────

func TestConditionalEvaluatorShouldSkip(t *testing.T) {
	ctx := NewContext()
	evaluator := NewConditionalEvaluator(ctx)

	// No _when expression means don't skip.
	step := &domain.Step{
		Name:       "test_step",
		StepType:   domain.StepTypeTask,
		InputsJSON: toNullString(map[string]any{"prompt": "hello"}),
	}

	skip, _, err := evaluator.ShouldSkip(step)
	if err != nil {
		t.Fatalf("ShouldSkip failed: %v", err)
	}
	if skip {
		t.Error("expected skip=false when no _when expression")
	}
}

func TestConditionalEvaluatorShouldSkipWhenTrue(t *testing.T) {
	ctx := NewContext()
	evaluator := NewConditionalEvaluator(ctx)

	// Set flag to true.
	ctx.SetOutput("check", "flag", true)

	step := &domain.Step{
		Name:       "test_step",
		StepType:   domain.StepTypeTask,
		InputsJSON: toNullString(map[string]any{"_when": "check.flag"}),
	}

	skip, _, err := evaluator.ShouldSkip(step)
	if err != nil {
		t.Fatalf("ShouldSkip failed: %v", err)
	}
	if skip {
		t.Error("expected skip=false when condition is true")
	}
}

func TestConditionalEvaluatorShouldSkipWhenFalse(t *testing.T) {
	ctx := NewContext()
	evaluator := NewConditionalEvaluator(ctx)

	// Set flag to false.
	ctx.SetOutput("check", "flag", false)

	step := &domain.Step{
		Name:       "test_step",
		StepType:   domain.StepTypeTask,
		InputsJSON: toNullString(map[string]any{"_when": "check.flag"}),
	}

	skip, reason, err := evaluator.ShouldSkip(step)
	if err != nil {
		t.Fatalf("ShouldSkip failed: %v", err)
	}
	if !skip {
		t.Error("expected skip=true when condition is false")
	}
	if !strings.Contains(reason, "check.flag") {
		t.Errorf("expected reason to mention the variable, got %q", reason)
	}
}

func TestConditionalEvaluatorEquality(t *testing.T) {
	ctx := NewContext()
	evaluator := NewConditionalEvaluator(ctx)

	ctx.SetOutput("check", "status", "ready")

	step := &domain.Step{
		Name:       "test_step",
		StepType:   domain.StepTypeConditional,
		InputsJSON: toNullString(map[string]any{"_when": "check.status == ready"}),
	}

	skip, _, err := evaluator.ShouldSkip(step)
	if err != nil {
		t.Fatalf("ShouldSkip failed: %v", err)
	}
	if skip {
		t.Error("expected skip=false when status == ready")
	}
}

func TestConditionalEvaluatorInequality(t *testing.T) {
	ctx := NewContext()
	evaluator := NewConditionalEvaluator(ctx)

	ctx.SetOutput("check", "status", "pending")

	step := &domain.Step{
		Name:       "test_step",
		StepType:   domain.StepTypeConditional,
		InputsJSON: toNullString(map[string]any{"_when": "check.status == ready"}),
	}

	skip, _, err := evaluator.ShouldSkip(step)
	if err != nil {
		t.Fatalf("ShouldSkip failed: %v", err)
	}
	if !skip {
		t.Error("expected skip=true when status != ready")
	}
}

func TestConditionalEvaluatorGreaterThan(t *testing.T) {
	// NOTE: The current evalBoolExpr implementation does not fully support > and < operators.
	// It falls through to variable existence check. This test documents current behavior.
	ctx := NewContext()
	evaluator := NewConditionalEvaluator(ctx)

	ctx.SetOutput("check", "count", 10)

	step := &domain.Step{
		Name:       "test_step",
		StepType:   domain.StepTypeConditional,
		InputsJSON: toNullString(map[string]any{"_when": "check.count > 5"}),
	}

	skip, _, err := evaluator.ShouldSkip(step)
	if err != nil {
		t.Fatalf("ShouldSkip failed: %v", err)
	}
	// Current behavior: falls through to existence check, which is true.
	if !skip {
		t.Error("expected current behavior: skip=true (falls through to variable existence)")
	}
}

func TestConditionalEvaluatorLessThan(t *testing.T) {
	// NOTE: The current evalBoolExpr implementation does not fully support < operator.
	ctx := NewContext()
	evaluator := NewConditionalEvaluator(ctx)

	ctx.SetOutput("check", "count", 3)

	step := &domain.Step{
		Name:       "test_step",
		StepType:   domain.StepTypeConditional,
		InputsJSON: toNullString(map[string]any{"_when": "check.count < 5"}),
	}

	skip, _, err := evaluator.ShouldSkip(step)
	if err != nil {
		t.Fatalf("ShouldSkip failed: %v", err)
	}
	// Current behavior: falls through to existence check, which is true.
	if !skip {
		t.Error("expected current behavior: skip=true (falls through to variable existence)")
	}
}

// ─── Helper Functions ───────────────────────────────────────────────────────

// toNullString converts a map to a sql.NullString for testing.
func toNullString(v map[string]any) sql.NullString {
	bs, _ := json.Marshal(v)
	return sql.NullString{String: string(bs), Valid: true}
}