package loop

import (
	"encoding/json"
	"testing"

	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/engine"
)

func TestAgentEvaluator_Name(t *testing.T) {
	ae := NewAgentEvaluator(&adapter.FakeAdapter{}, AgentEvaluatorConfig{AdapterKey: "fake"})
	if got := ae.Name(); got != "agent" {
		t.Errorf("Name() = %q, want %q", got, "agent")
	}
}

func TestAgentEvaluator_Evaluate_Invoke(t *testing.T) {
	var invokedReq adapter.AgentRequest
	spy := &spyAdapter{fn: func(req adapter.AgentRequest) (*adapter.AgentResult, error) {
		invokedReq = req
		return &adapter.AgentResult{
			Output:  "score: 0.85\nconverged: false",
			Summary: "looks good",
		}, nil
	}}

	ae := NewAgentEvaluator(spy, AgentEvaluatorConfig{
		AdapterKey: "spy",
		Model:     "test-model",
	})

	ctx := engine.NewContext()
	ctx.SetOutput("fix", "result", "patched")

	score, feedback, metadata, err := ae.Evaluate(0, ctx, map[string]any{"test": "output"})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if score != 0.85 {
		t.Errorf("score = %v, want 0.85", score)
	}
	if feedback == "" {
		t.Error("feedback empty")
	}
	if metadata == nil {
		t.Fatal("metadata nil")
	}
	if metadata["model"] != "test-model" {
		t.Errorf("model = %v", metadata["model"])
	}

	// Check prompt was built (spy was invoked).
	if invokedReq.Prompt == "" && invokedReq.RawJSON == nil {
		t.Error("adapter not invoked")
	}
}

func TestAgentEvaluator_Evaluate_JSONResult(t *testing.T) {
	spy := &spyAdapter{fn: func(req adapter.AgentRequest) (*adapter.AgentResult, error) {
		return &adapter.AgentResult{
			RawJSON: json.RawMessage(`{"score": 0.92, "feedback": "excellent", "confidence": 0.9, "tokens_used": 1500}`),
			Summary: "structured response",
		}, nil
	}}

	ae := NewAgentEvaluator(spy, AgentEvaluatorConfig{Model: "claude-3"})
	score, feedback, metadata, err := ae.Evaluate(2, nil, nil)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if score != 0.92 {
		t.Errorf("score = %v, want 0.92", score)
	}
	if feedback != "excellent" {
		t.Errorf("feedback = %q, want %q", feedback, "excellent")
	}
	if metadata["tokens_used"] != 1500 {
		t.Errorf("tokens_used = %v", metadata["tokens_used"])
	}
	if metadata["confidence"] != 0.9 {
		t.Errorf("confidence = %v", metadata["confidence"])
	}
}

func TestAgentEvaluator_ClampScore(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{-0.5, 0.0},
		{0.5, 0.5},
		{1.5, 1.0},
		{0.0, 0.0},
		{1.0, 1.0},
	}
	for _, c := range cases {
		if got := clampScore(c.in); got != c.want {
			t.Errorf("clampScore(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ─── RuleBasedEvaluator ───────────────────────────────────────────────────────

func TestRuleBasedEvaluator_Name(t *testing.T) {
	rbe := NewRuleBasedEvaluator(nil, 1.0)
	if got := rbe.Name(); got != "rule-based" {
		t.Errorf("Name() = %q, want %q", got, "rule-based")
	}
}

func TestRuleBasedEvaluator_NoAssertions(t *testing.T) {
	rbe := NewRuleBasedEvaluator(nil, 1.0)
	score, feedback, _, err := rbe.Evaluate(0, nil, map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if score != 1.0 {
		t.Errorf("score = %v, want 1.0", score)
	}
	if feedback == "" {
		t.Error("feedback empty")
	}
}

func TestRuleBasedEvaluator_PassThreshold(t *testing.T) {
	assertions := []json.RawMessage{
		mustMarshal(map[string]any{"type": "field_exists", "field": "ok"}),
		mustMarshal(map[string]any{"type": "field_exists", "field": "missing"}),
	}
	rbe := NewRuleBasedEvaluator(assertions, 0.5) // require 50% pass
	score, _, metadata, err := rbe.Evaluate(0, nil, map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if score != 0.5 {
		t.Errorf("score = %v, want 0.5", score)
	}
	if metadata["threshold_met"] != true {
		t.Errorf("threshold_met = %v, want true", metadata["threshold_met"])
	}
}

func TestRuleBasedEvaluator_AllPass(t *testing.T) {
	assertions := []json.RawMessage{
		mustMarshal(map[string]any{"type": "field_exists", "field": "key"}),
		mustMarshal(map[string]any{"type": "field_equals", "field": "val", "value": 42}),
	}
	rbe := NewRuleBasedEvaluator(assertions, 1.0)
	score, _, metadata, err := rbe.Evaluate(0, nil, map[string]any{"key": "exists", "val": float64(42)})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if score != 1.0 {
		t.Errorf("score = %v, want 1.0", score)
	}
	if metadata["failed"].(int) != 0 {
		t.Errorf("failed = %v", metadata["failed"])
	}
}

func TestRuleBasedEvaluator_FieldExists(t *testing.T) {
	assertions := []json.RawMessage{
		mustMarshal(map[string]any{"type": "field_exists", "field": "result"}),
	}
	rbe := NewRuleBasedEvaluator(assertions, 1.0)
	_, _, _, err := rbe.Evaluate(0, nil, map[string]any{"result": "ok"})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	// Field missing.
	_, _, _, err = rbe.Evaluate(0, nil, map[string]any{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
}

func TestRuleBasedEvaluator_FieldEquals(t *testing.T) {
	assertions := []json.RawMessage{
		mustMarshal(map[string]any{"type": "field_equals", "field": "status", "value": "success"}),
	}
	rbe := NewRuleBasedEvaluator(assertions, 1.0)

	_, _, _, err := rbe.Evaluate(0, nil, map[string]any{"status": "success"})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
}

func TestRuleBasedEvaluator_FieldMatches(t *testing.T) {
	assertions := []json.RawMessage{
		mustMarshal(map[string]any{"type": "field_matches", "field": "msg", "expr": "error"}),
	}
	rbe := NewRuleBasedEvaluator(assertions, 1.0)

	_, _, _, err := rbe.Evaluate(0, nil, map[string]any{"msg": "no error found"})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
}

func TestRuleBasedEvaluator_FieldCompare(t *testing.T) {
	assertions := []json.RawMessage{
		mustMarshal(map[string]any{"type": "field_compare", "field": "score", "op": "ge", "value": 0.8}),
	}
	rbe := NewRuleBasedEvaluator(assertions, 1.0)

	_, _, _, err := rbe.Evaluate(0, nil, map[string]any{"score": 0.9})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
}

func TestRuleBasedEvaluator_FieldCompare_Numeric(t *testing.T) {
	assertions := []json.RawMessage{
		mustMarshal(map[string]any{"type": "field_compare", "field": "count", "op": "gt", "value": 5}),
	}
	rbe := NewRuleBasedEvaluator(assertions, 1.0)

	_, _, metadata, err := rbe.Evaluate(0, nil, map[string]any{"count": 10})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if metadata["passed"].(int) != 1 {
		t.Errorf("passed = %v", metadata["passed"])
	}
}

func TestRuleBasedEvaluator_MultipleAssertions(t *testing.T) {
	assertions := []json.RawMessage{
		mustMarshal(map[string]any{"type": "field_exists", "field": "a"}),
		mustMarshal(map[string]any{"type": "field_exists", "field": "b"}),
		mustMarshal(map[string]any{"type": "field_exists", "field": "c"}),
	}
	rbe := NewRuleBasedEvaluator(assertions, 0.66) // require 2/3

	_, _, meta, _ := rbe.Evaluate(0, nil, map[string]any{"a": 1, "b": 2}) // 2/3 pass
	if meta["passed"].(int) != 2 {
		t.Errorf("passed = %v, want 2", meta["passed"])
	}
	if meta["threshold_met"] != true {
		t.Errorf("threshold_met = %v, want true", meta["threshold_met"])
	}
}

// ─── MockEvaluator ───────────────────────────────────────────────────────────

func TestMockEvaluator_Name(t *testing.T) {
	me := NewMockEvaluator()
	if got := me.Name(); got != "mock" {
		t.Errorf("Name() = %q, want %q", got, "mock")
	}
}

func TestMockEvaluator_Evaluate(t *testing.T) {
	me := NewMockEvaluator()
	me.ScoreVal = 0.88
	me.FeedbackVal = "looks great"

	ctx := engine.NewContext()
	outputs := map[string]any{"result": "done"}

	score, _, _, err := me.Evaluate(3, ctx, outputs)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if score != 0.88 {
		t.Errorf("score = %v, want 0.88", score)
	}
	if me.CallCount != 1 {
		t.Errorf("CallCount = %d, want 1", me.CallCount)
	}
	if len(me.Calls) != 1 || me.Calls[0].Iteration != 3 {
		t.Errorf("Calls = %v", me.Calls)
	}
}

func TestMockEvaluator_CallRecording(t *testing.T) {
	me := NewMockEvaluator()

	ctx := engine.NewContext()
	outputs := map[string]any{"k": "v"}

	me.Evaluate(0, ctx, outputs)
	me.Evaluate(1, ctx, outputs)

	if me.CallCount != 2 {
		t.Errorf("CallCount = %d, want 2", me.CallCount)
	}
	if len(me.Calls) != 2 {
		t.Errorf("len(Calls) = %d, want 2", len(me.Calls))
	}
	if me.Calls[0].Iteration != 0 || me.Calls[1].Iteration != 1 {
		t.Errorf("iteration mismatch: %v", me.Calls)
	}
}

func TestMockEvaluator_Error(t *testing.T) {
	me := NewMockEvaluator()
	me.ErrVal = assertErr

	_, _, _, err := me.Evaluate(0, nil, nil)
	if err != assertErr {
		t.Errorf("err = %v, want %v", err, assertErr)
	}
}

var assertErr = assertErrType{}

type assertErrType struct{}

func (assertErrType) Error() string { return "assert error" }

// ─── Registry ────────────────────────────────────────────────────────────────

func TestEvaluatorRegistry(t *testing.T) {
	reg := NewEvaluatorRegistry()
	me := NewMockEvaluator()
	reg.Register(me)

	got, err := reg.Get("mock")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name() != "mock" {
		t.Errorf("got.Name() = %q", got.Name())
	}

	_, err = reg.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent key")
	}
}

func TestEvaluatorRegistry_MustGet(t *testing.T) {
	reg := NewEvaluatorRegistry()
	reg.Register(NewMockEvaluator())

	got := reg.MustGet("mock")
	if got.Name() != "mock" {
		t.Errorf("got.Name() = %q", got.Name())
	}
}

// ─── SpyAdapter ──────────────────────────────────────────────────────────────

type spyAdapter struct {
	fn func(adapter.AgentRequest) (*adapter.AgentResult, error)
}

func (s *spyAdapter) Key() string   { return "spy" }
func (s *spyAdapter) Invoke(req adapter.AgentRequest) (*adapter.AgentResult, error) {
	return s.fn(req)
}

func mustMarshal(v any) json.RawMessage {
	bs, _ := json.Marshal(v)
	return bs
}