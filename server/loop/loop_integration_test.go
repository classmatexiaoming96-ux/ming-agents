package loop

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/eval"
)

// ─── Mock Types for Integration Testing ──────────────────────────────────────

type MockEvalEvaluator struct {
	NameVal    string
	Results    []eval.EvalResult
	CurrentIdx int
	CallCount  int
	Calls      []EvalCall
}

type EvalCall struct {
	Ctx       any
	Iteration int
}

func newMockEvalEvaluator(results ...eval.EvalResult) *MockEvalEvaluator {
	return &MockEvalEvaluator{
		NameVal: "mock-eval",
		Results: results,
	}
}

func (e *MockEvalEvaluator) Key() string { return e.NameVal }

func (e *MockEvalEvaluator) Evaluate(ctx any, iteration int) (*eval.EvalResult, error) {
	e.CallCount++
	e.Calls = append(e.Calls, EvalCall{Ctx: ctx, Iteration: iteration})

	if e.CurrentIdx >= len(e.Results) {
		return &eval.EvalResult{
			Iteration: iteration,
			Score:     0.5,
			Feedback:  "no more results",
			Converged: true, // Signal that evaluator has exhausted its results
		}, nil
	}

	result := e.Results[e.CurrentIdx]
	result.Iteration = iteration
	e.CurrentIdx++
	return &result, nil
}

// ─── TestLoopConvergesOnThreshold ─────────────────────────────────────────────
//
// KEY INSIGHT: Threshold is used for BOTH score-based convergence AND plateau
// detection (UpdateWithScore sets Converged when improvement < Threshold).
//
// To avoid premature plateau convergence, we must use a Threshold that is:
// - High enough that improvements don't accidentally trigger it
// - Low enough that scores can actually reach it for convergence
//
// We use Threshold=0.99 so improvements of 0.15 don't trigger plateau
// convergence (0.15 < 0.99 is false), but actual convergence at score>=0.99 works.

func TestLoopConvergesOnThreshold(t *testing.T) {
	tests := []struct {
		name           string
		threshold      float64
		scores         []float64
		wantIterations int
		wantConverged  bool
	}{
		{
			name:           "converges at threshold 0.99",
			threshold:      0.99,
			scores:         []float64{0.5, 0.65, 0.8, 0.99},
			wantIterations: 4,
			wantConverged:  true,
		},
		{
			name:           "converges above threshold",
			threshold:      0.99,
			scores:         []float64{0.5, 0.7, 0.95, 1.0},
			wantIterations: 4,
			wantConverged:  true,
		},
		{
			name:           "does not converge below threshold",
			threshold:      0.99,
			scores:         []float64{0.5, 0.6, 0.7, 0.8, 0.85},
			wantIterations: 5,
			wantConverged:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make([]eval.EvalResult, len(tt.scores))
			for i, score := range tt.scores {
				results[i] = eval.EvalResult{
					Iteration: i,
					Score:     score,
					Feedback:  fmt.Sprintf("feedback %d", i),
					Converged: false,
				}
			}

			mockEval := newMockEvalEvaluator(results...)

			cfg := &LoopConfig{
				StepID:               uuid.New(),
				StepName:             "test",
				MaxIterations:        10,
				Threshold:            tt.threshold,
				ConvergenceCondition: fmt.Sprintf("score >= %v", tt.threshold),
			}

			controller := NewLoopController(cfg, nil, mockEval, nil)
			state := NewLoopState(cfg)

			iterations := 0
			for state.ShouldContinue() {
				result, err := controller.RunIteration(state)
				if err != nil {
					t.Fatalf("RunIteration error: %v", err)
				}
				iterations++
				if result.Done {
					break
				}
			}

			if iterations != tt.wantIterations {
				t.Errorf("ran %d iterations, want %d", iterations, tt.wantIterations)
			}
			if state.Converged != tt.wantConverged {
				t.Errorf("Converged=%v, want %v", state.Converged, tt.wantConverged)
			}
		})
	}
}

// ─── TestLoopTerminatesOnMaxIterations ──────────────────────────────────────
//
// Use Threshold=0.99 to avoid plateau convergence, so loop runs all iterations.

func TestLoopTerminatesOnMaxIterations(t *testing.T) {
	tests := []struct {
		name           string
		maxIterations int
		threshold     float64
		scores        []float64
		wantStatus    string
	}{
		{
			name:           "terminates at max",
			maxIterations: 5,
			threshold:     0.99,
			scores:        []float64{0.5, 0.6, 0.7, 0.8, 0.85},
			wantStatus:    domain.IterationStatusMaxIterations,
		},
		{
			name:           "terminates at max=1",
			maxIterations: 1,
			threshold:     0.99,
			scores:        []float64{0.5},
			wantStatus:    domain.IterationStatusMaxIterations,
		},
		{
			name:           "terminates at max=3",
			maxIterations: 3,
			threshold:     0.99,
			scores:        []float64{0.5, 0.6, 0.7},
			wantStatus:    domain.IterationStatusMaxIterations,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make([]eval.EvalResult, len(tt.scores))
			for i, score := range tt.scores {
				results[i] = eval.EvalResult{
					Iteration: i,
					Score:     score,
					Feedback:  "not converging",
					Converged: false,
				}
			}

			mockEval := newMockEvalEvaluator(results...)

			cfg := &LoopConfig{
				StepID:        uuid.New(),
				StepName:      "test",
				MaxIterations: tt.maxIterations,
				Threshold:     tt.threshold,
			}

			controller := NewLoopController(cfg, nil, mockEval, nil)
			state := NewLoopState(cfg)

			for state.ShouldContinue() {
				result, err := controller.RunIteration(state)
				if err != nil {
					t.Fatalf("RunIteration error: %v", err)
				}
				if result.Done {
					break
				}
			}

			if state.Status != tt.wantStatus {
				t.Errorf("status=%q, want %q", state.Status, tt.wantStatus)
			}
			if state.TerminationReason != TerminationReasonMaxIterations {
				t.Errorf("TerminationReason=%q, want %q", state.TerminationReason, TerminationReasonMaxIterations)
			}
		})
	}
}

// ─── TestLoopDetectsNoProgress ───────────────────────────────────────────────
//
// UpdateWithScore sets Converged=true when improvement <= 0 (flat/declining).
// With Threshold=0.99, improvements of 0.15 won't trigger plateau convergence.

func TestLoopDetectsNoProgress(t *testing.T) {
	tests := []struct {
		name           string
		scores         []float64
		threshold      float64
		wantIterations int
		wantConverged  bool
	}{
		{
			name:           "flat scores stop with no progress",
			scores:         []float64{0.5, 0.5},
			threshold:      0.99,
			wantIterations: 2,
			wantConverged:  false, // not converged — score below threshold
		},
		{
			name:           "declining scores stop with no progress",
			scores:         []float64{0.7, 0.5},
			threshold:      0.99,
			wantIterations: 2,
			wantConverged:  false, // not converged — score below threshold
		},
		{
			name:           "improving scores run all iterations",
			scores:         []float64{0.5, 0.65, 0.8, 0.95},
			threshold:      0.99,
			wantIterations: 4,
			wantConverged:  false, // never converges
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := make([]eval.EvalResult, len(tt.scores))
			for i, score := range tt.scores {
				results[i] = eval.EvalResult{
					Iteration: i,
					Score:     score,
					Feedback:  "feedback",
					Converged: false,
				}
			}

			mockEval := newMockEvalEvaluator(results...)

			cfg := &LoopConfig{
				StepID:               uuid.New(),
				StepName:             "test",
				MaxIterations:        10,
				Threshold:            tt.threshold,
				ConvergenceCondition: "score >= 0.99",
			}

			controller := NewLoopController(cfg, nil, mockEval, nil)
			state := NewLoopState(cfg)

			iterations := 0
			for state.ShouldContinue() {
				result, err := controller.RunIteration(state)
				if err != nil {
					t.Fatalf("RunIteration error: %v", err)
				}
				iterations++
				if result.Done {
					break
				}
			}

			if iterations != tt.wantIterations {
				t.Errorf("ran %d iterations, want %d", iterations, tt.wantIterations)
			}
			if state.Converged != tt.wantConverged {
				t.Errorf("Converged=%v, want %v", state.Converged, tt.wantConverged)
			}
		})
	}
}

// ─── TestLoopFeedbackImprovesOutput ─────────────────────────────────────────

func TestLoopFeedbackImprovesOutput(t *testing.T) {
	results := []eval.EvalResult{
		{Iteration: 0, Score: 0.5, Feedback: "improve 0", Converged: false},
		{Iteration: 1, Score: 0.65, Feedback: "improve 1", Converged: false},
		{Iteration: 2, Score: 0.8, Feedback: "improve 2", Converged: false},
	}

	mockEval := newMockEvalEvaluator(results...)

	cfg := &LoopConfig{
		StepID:        uuid.New(),
		StepName:      "test",
		MaxIterations: 5,
		Threshold:     0.99, // high threshold
	}

	controller := NewLoopController(cfg, nil, mockEval, nil)
	state := NewLoopState(cfg)

	var feedbacks []string
	var scores []float64

	for state.ShouldContinue() {
		result, err := controller.RunIteration(state)
		if err != nil {
			t.Fatalf("RunIteration error: %v", err)
		}
		feedbacks = append(feedbacks, result.Feedback)
		if state.EvalScore != nil {
			scores = append(scores, *state.EvalScore)
		}
		if result.Done {
			break
		}
	}

	if len(feedbacks) != 3 {
		t.Errorf("got %d feedbacks, want 3", len(feedbacks))
	}

	for i := 1; i < len(scores); i++ {
		if scores[i] <= scores[i-1] {
			t.Errorf("score[%d]=%.2f not > score[%d]=%.2f", i, scores[i], i-1, scores[i-1])
		}
	}

	for i, fb := range feedbacks {
		if !strings.Contains(fb, fmt.Sprintf("iteration %d", i)) {
			t.Errorf("feedback[%d] missing iteration %d", i, i)
		}
	}
}

// ─── TestLoopWithMixedEvaluator ────────────────────────────────────────────────

func TestLoopWithMixedEvaluator(t *testing.T) {
	tests := []struct {
		name        string
		assertions  []json.RawMessage
		outputs     []map[string]any
		wantScores  []float64
		wantIters   int
	}{
		{
			name: "passes then fails then passes",
			assertions: []json.RawMessage{
				testMustMarshal(map[string]any{"type": "field_exists", "field": "result"}),
			},
			outputs: []map[string]any{
				{"result": "ok"},
				{"other": "data"},
				{"result": "done"},
			},
			wantScores: []float64{1.0, 0.0, 1.0},
			wantIters:  3,
		},
		{
			name: "all pass",
			assertions: []json.RawMessage{
				testMustMarshal(map[string]any{"type": "field_exists", "field": "ok"}),
			},
			outputs: []map[string]any{
				{"ok": true},
				{"ok": true},
			},
			wantScores: []float64{1.0, 1.0},
			wantIters:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rbe := NewRuleBasedEvaluator(tt.assertions, 0.8)
			mockEval := &ruleBasedEvalWrapper{rbe: rbe, outputs: tt.outputs, idx: 0}

			cfg := &LoopConfig{
				StepID:        uuid.New(),
				StepName:      "test",
				MaxIterations: 10,
				Threshold:     0.99,
			}

			controller := NewLoopController(cfg, nil, mockEval, nil)
			state := NewLoopState(cfg)

			var scores []float64
			for state.ShouldContinue() {
				result, err := controller.RunIteration(state)
				if err != nil {
					t.Fatalf("RunIteration error: %v", err)
				}
				if state.EvalScore != nil {
					scores = append(scores, *state.EvalScore)
				}
				if result.Done {
					break
				}
			}

			if len(scores) != len(tt.wantScores) {
				t.Errorf("got %d scores, want %d", len(scores), len(tt.wantScores))
			}
			for i, want := range tt.wantScores {
				if i < len(scores) && scores[i] != want {
					t.Errorf("scores[%d]=%.2f, want %.2f", i, scores[i], want)
				}
			}
		})
	}
}

type ruleBasedEvalWrapper struct {
	rbe     *RuleBasedEvaluator
	outputs []map[string]any
	idx     int
}

func (w *ruleBasedEvalWrapper) Key() string { return "rule-based" }

func (w *ruleBasedEvalWrapper) Evaluate(ctx any, iteration int) (*eval.EvalResult, error) {
	if w.idx >= len(w.outputs) {
		w.idx = len(w.outputs) - 1
	}
	output := w.outputs[w.idx]
	w.idx++

	score, feedback, metadata, err := w.rbe.Evaluate(iteration, nil, output)
	if err != nil {
		return nil, err
	}

	return &eval.EvalResult{
		Iteration: iteration,
		Score:     score,
		Feedback:  feedback,
		Details:   metadata,
		Converged: score >= 0.8,
	}, nil
}

// ─── TestLoopTaskAssociation ──────────────────────────────────────────────────

func TestLoopTaskAssociation(t *testing.T) {
	tests := []struct {
		name          string
		taskIDs       []string
		wantTaskCount int
	}{
		{
			name:          "single task per iteration",
			taskIDs:       []string{"task-1", "task-2", "task-3"},
			wantTaskCount: 1,
		},
		{
			name:          "multiple tasks per iteration",
			taskIDs:       []string{"task-1a", "task-1b", "task-2a", "task-2b"},
			wantTaskCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewInMemoryIterationTracker()
			runID := uuid.New().String()

			numIters := len(tt.taskIDs) / tt.wantTaskCount
			results := make([]eval.EvalResult, numIters)
			for i := range results {
				results[i] = eval.EvalResult{
					Iteration: i,
					Score:     0.5 + float64(i)*0.15,
					Feedback:  "test",
					Converged: false,
				}
			}

			mockEval := newMockEvalEvaluator(results...)

			cfg := &LoopConfig{
				StepID:        uuid.New(),
				StepName:      "test",
				MaxIterations: 10,
				Threshold:     0.99,
			}

			controller := NewLoopController(cfg, tracker, mockEval, nil)
			state := NewLoopState(cfg)

			iteration := 0
			for state.ShouldContinue() {
				result, err := controller.RunIteration(state)
				if err != nil {
					t.Fatalf("RunIteration error: %v", err)
				}

				taskOffset := iteration * tt.wantTaskCount
				taskIDsForIter := make([]string, tt.wantTaskCount)
				for j := 0; j < tt.wantTaskCount && (taskOffset+j) < len(tt.taskIDs); j++ {
					taskIDsForIter[j] = tt.taskIDs[taskOffset+j]
				}

				score := 0.0
				if state.EvalScore != nil {
					score = *state.EvalScore
				}
				rec := IterationRecord{
					Iteration: iteration,
					TaskIDs:   taskIDsForIter,
					Score:     score,
					Feedback:  result.Feedback,
					Timestamp: time.Now(),
				}
				tracker.RecordIteration(runID, rec)

				iteration++
				if result.Done {
					break
				}
			}

			recs := tracker.GetIterationRecords(runID)
			if len(recs) != iteration {
				t.Errorf("got %d records, want %d", len(recs), iteration)
			}

			for i, rec := range recs {
				if len(rec.TaskIDs) != tt.wantTaskCount {
					t.Errorf("record[%d] has %d task IDs, want %d", i, len(rec.TaskIDs), tt.wantTaskCount)
				}
			}
		})
	}
}

// ─── TestLoopWithEmptyStore ───────────────────────────────────────────────────

func TestLoopWithEmptyStore(t *testing.T) {
	mockEval := newMockEvalEvaluator(
		eval.EvalResult{Iteration: 0, Score: 0.5, Feedback: "first", Converged: false},
		eval.EvalResult{Iteration: 1, Score: 0.65, Feedback: "second", Converged: false},
		eval.EvalResult{Iteration: 2, Score: 0.8, Feedback: "third", Converged: false},
	)

	cfg := &LoopConfig{
		StepID:        uuid.New(),
		StepName:      "test",
		MaxIterations: 5,
		Threshold:     0.99,
	}

	controller := NewLoopController(cfg, nil, mockEval, nil)
	state := NewLoopState(cfg)

	iterations := 0
	for state.ShouldContinue() {
		result, err := controller.RunIteration(state)
		if err != nil {
			t.Fatalf("RunIteration error: %v", err)
		}
		iterations++
		if result.Done {
			break
		}
	}

	if iterations != 3 {
		t.Errorf("ran %d iterations, want 3", iterations)
	}
	if state.Converged {
		t.Error("should not have converged (max score 0.8 < 0.99)")
	}
}

// ─── TestLoopWithFeedbackAssembler ─────────────────────────────────────────────

func TestLoopWithFeedbackAssembler(t *testing.T) {
	asm := NewFeedbackAssembler()

	results := []eval.EvalResult{
		{Iteration: 0, Score: 0.5, Feedback: "improve X", Converged: false},
		{Iteration: 1, Score: 0.65, Feedback: "improve Y", Converged: false},
		{Iteration: 2, Score: 0.8, Feedback: "good", Converged: false},
	}

	mockEval := newMockEvalEvaluator(results...)

	cfg := &LoopConfig{
		StepID:        uuid.New(),
		StepName:      "test",
		MaxIterations: 5,
		Threshold:     0.99,
	}

	controller := NewLoopController(cfg, nil, mockEval, nil)
	state := NewLoopState(cfg)

	var assembledPrompts []string

	for state.ShouldContinue() {
		result, err := controller.RunIteration(state)
		if err != nil {
			t.Fatalf("RunIteration error: %v", err)
		}

		if state.Iteration > 0 && state.EvalScore != nil {
			prompt := asm.AssemblePrompt(state.Iteration, nil, result.Feedback, *state.EvalScore)
			assembledPrompts = append(assembledPrompts, prompt)
		}

		if result.Done {
			break
		}
	}

	if len(assembledPrompts) != 2 {
		t.Errorf("got %d prompts, want 2", len(assembledPrompts))
	}

	for i, prompt := range assembledPrompts {
		if prompt == "" {
			t.Errorf("prompt[%d] is empty", i)
		}
		if !strings.Contains(prompt, "iteration") {
			t.Errorf("prompt[%d] missing 'iteration'", i)
		}
	}
}

// ─── TestLoopFullIterationLifecycle ─────────────────────────────────────────

func TestLoopFullIterationLifecycle(t *testing.T) {
	runID := uuid.New().String()
	tracker := NewInMemoryIterationTracker()

	results := []eval.EvalResult{
		{Iteration: 0, Score: 0.4, Feedback: "poor", Converged: false},
		{Iteration: 1, Score: 0.55, Feedback: "better", Converged: false},
		{Iteration: 2, Score: 0.7, Feedback: "good", Converged: false},
		{Iteration: 3, Score: 0.85, Feedback: "almost there", Converged: false},
		{Iteration: 4, Score: 0.99, Feedback: "converged!", Converged: true},
	}

	mockEval := newMockEvalEvaluator(results...)

	cfg := &LoopConfig{
		StepID:        uuid.New(),
		StepName:      "bug-fix",
		MaxIterations: 10,
		Threshold:     0.99,
	}

	controller := NewLoopController(cfg, tracker, mockEval, nil)
	state := NewLoopState(cfg)

	for state.ShouldContinue() {
		result, err := controller.RunIteration(state)
		if err != nil {
			t.Fatalf("RunIteration error: %v", err)
		}

		score := 0.0
		if state.EvalScore != nil {
			score = *state.EvalScore
		}
		rec := IterationRecord{
			Iteration: state.Iteration,
			TaskIDs:   []string{fmt.Sprintf("task-%d", state.Iteration)},
			Score:     score,
			Feedback:  result.Feedback,
			Timestamp: time.Now(),
		}
		tracker.RecordIteration(runID, rec)

		if result.Done {
			break
		}
	}

	if state.Iteration != 5 {
		t.Errorf("final iteration=%d, want 5", state.Iteration)
	}

	recs := tracker.GetIterationRecords(runID)
	if len(recs) != 5 {
		t.Errorf("tracker has %d records, want 5", len(recs))
	}

	scores := GetScoreHistory(tracker, runID)
	if len(scores) != 5 {
		t.Errorf("score history has %d entries, want 5", len(scores))
	}

	trend := GetProgressTrend(tracker, runID)
	if trend != "improving" {
		t.Errorf("trend=%q, want improving", trend)
	}
}

// ─── Helper ─────────────────────────────────────────────────────────────────────

func testMustMarshal(v any) json.RawMessage {
	bs, _ := json.Marshal(v)
	return bs
}