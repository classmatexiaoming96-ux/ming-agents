package loop

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// ─── ConvergenceChecker Tests ─────────────────────────────────────────────────

func TestConvergenceChecker_CheckScoreThreshold(t *testing.T) {
	c := NewConvergenceChecker(0.9, 10, 3)

	tests := []struct {
		name      string
		score     float64
		threshold float64
		want      bool
	}{
		{"score equals threshold", 0.9, 0.9, true},
		{"score above threshold", 0.95, 0.9, true},
		{"score below threshold", 0.85, 0.9, false},
		{"score far below threshold", 0.5, 0.9, false},
		{"zero threshold", 0.0, 0.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.CheckScoreThreshold(tt.score, tt.threshold)
			if got != tt.want {
				t.Errorf("CheckScoreThreshold(%v, %v) = %v, want %v", tt.score, tt.threshold, got, tt.want)
			}
		})
	}
}

func TestConvergenceChecker_CheckMaxIterations(t *testing.T) {
	c := NewConvergenceChecker(0.9, 10, 3)

	tests := []struct {
		name    string
		current int
		max     int
		want    bool
	}{
		{"at max", 10, 10, true},
		{"above max", 11, 10, true},
		{"below max", 5, 10, false},
		{"zero max", 0, 0, true},
		{"zero current", 0, 10, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.CheckMaxIterations(tt.current, tt.max)
			if got != tt.want {
				t.Errorf("CheckMaxIterations(%v, %v) = %v, want %v", tt.current, tt.max, got, tt.want)
			}
		})
	}
}

func TestConvergenceChecker_CheckNoProgress(t *testing.T) {
	runID := uuid.New()
	stepID := uuid.New()
	makeSnap := func(iter int, score float64) IterationSnapshot {
		return IterationSnapshot{
			ID:        uuid.New(),
			RunID:     runID,
			StepID:    stepID,
			Iteration: iter,
			Score:     score,
			Feedback:  "test",
			Timestamp: time.Now(),
		}
	}

	tests := []struct {
		name      string
		snapshots []IterationSnapshot
		window    int
		want      bool
	}{
		{
			name:      "improvement in window",
			snapshots: []IterationSnapshot{makeSnap(0, 0.5), makeSnap(1, 0.6), makeSnap(2, 0.7)},
			window:    2,
			want:      false,
		},
		{
			name:      "no improvement in window",
			snapshots: []IterationSnapshot{makeSnap(0, 0.5), makeSnap(1, 0.5), makeSnap(2, 0.5)},
			window:    2,
			want:      true,
		},
		{
			name:      "score declining",
			snapshots: []IterationSnapshot{makeSnap(0, 0.7), makeSnap(1, 0.6), makeSnap(2, 0.5)},
			window:    2,
			want:      true,
		},
		{
			name:      "not enough snapshots",
			snapshots: []IterationSnapshot{makeSnap(0, 0.5)},
			window:    2,
			want:      false,
		},
		{
			name:      "improvement then decline",
			snapshots: []IterationSnapshot{makeSnap(0, 0.5), makeSnap(1, 0.7), makeSnap(2, 0.6)},
			window:    2,
			want:      false, // still had improvement
		},
		{
			name:      "single snapshot in window",
			snapshots: []IterationSnapshot{makeSnap(0, 0.5), makeSnap(1, 0.4)},
			window:    1,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewConvergenceChecker(0, 100, tt.window).CheckNoProgress(tt.snapshots, tt.window)
			if got != tt.want {
				t.Errorf("CheckNoProgress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConvergenceChecker_TerminateIf(t *testing.T) {
	runID := uuid.New()
	stepID := uuid.New()
	makeSnap := func(iter int, score float64) IterationSnapshot {
		return IterationSnapshot{
			ID:        uuid.New(),
			RunID:     runID,
			StepID:    stepID,
			Iteration: iter,
			Score:     score,
			Feedback:  "test",
			Timestamp: time.Now(),
		}
	}

	tests := []struct {
		name              string
		snapshots         []IterationSnapshot
		currentIteration  int
		currentScore     float64
		scoreThreshold    float64
		maxIterations     int
		noProgressWindow  int
		wantTerminate     bool
		wantReason        TerminationReason
	}{
		{
			name:             "score threshold met",
			snapshots:        []IterationSnapshot{makeSnap(0, 0.9)},
			currentIteration: 1,
			currentScore:     0.95,
			scoreThreshold:   0.9,
			maxIterations:    10,
			noProgressWindow: 3,
			wantTerminate:     true,
			wantReason:        TerminationReasonConverged,
		},
		{
			name:             "max iterations reached",
			snapshots:        []IterationSnapshot{},
			currentIteration: 10,
			currentScore:     0.5,
			scoreThreshold:   0.9,
			maxIterations:    10,
			noProgressWindow: 3,
			wantTerminate:     true,
			wantReason:        TerminationReasonMaxIterations,
		},
		{
			name:             "no progress detected",
			snapshots:        []IterationSnapshot{makeSnap(0, 0.5), makeSnap(1, 0.5), makeSnap(2, 0.5)},
			currentIteration: 3,
			currentScore:     0.5,
			scoreThreshold:   0.9,
			maxIterations:    10,
			noProgressWindow: 2, // reduced from 3 so 3 snapshots is enough (need window+1=3)
			wantTerminate:     true,
			wantReason:        TerminationReasonNoProgress,
		},
		{
			name:             "continue iterating",
			snapshots:        []IterationSnapshot{makeSnap(0, 0.5), makeSnap(1, 0.6)},
			currentIteration: 2,
			currentScore:     0.65,
			scoreThreshold:   0.9,
			maxIterations:    10,
			noProgressWindow: 3,
			wantTerminate:     false,
			wantReason:        TerminationReasonNotYet,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewConvergenceChecker(tt.scoreThreshold, tt.maxIterations, tt.noProgressWindow)
			gotTerminate, gotReason := c.TerminateIf(tt.snapshots, tt.currentIteration, tt.currentScore)
			if gotTerminate != tt.wantTerminate {
				t.Errorf("TerminateIf() terminate = %v, want %v", gotTerminate, tt.wantTerminate)
			}
			if gotReason != tt.wantReason {
				t.Errorf("TerminateIf() reason = %v, want %v", gotReason, tt.wantReason)
			}
		})
	}
}

// ─── ExpressionParser Tests ──────────────────────────────────────────────────

func TestExpressionParser_EvaluateExpression(t *testing.T) {
	p := NewExpressionParser()

	tests := []struct {
		name    string
		expr    string
		vars    map[string]any
		want    bool
		wantErr bool
	}{
		{
			name:    "simple score >= threshold",
			expr:    "score >= 0.9",
			vars:    map[string]any{"score": 0.95},
			want:    true,
			wantErr: false,
		},
		{
			name:    "score below threshold",
			expr:    "score >= 0.9",
			vars:    map[string]any{"score": 0.85},
			want:    false,
			wantErr: false,
		},
		{
			name:    "negation",
			expr:    "!test.passed",
			vars:    map[string]any{"test.passed": false},
			want:    true,
			wantErr: false,
		},
		{
			name:    "negation false",
			expr:    "!test.passed",
			vars:    map[string]any{"test.passed": true},
			want:    false,
			wantErr: false,
		},
		{
			name:    "score > prev_score",
			expr:    "score > prev_score",
			vars:    map[string]any{"score": 0.8, "prev_score": 0.7},
			want:    true,
			wantErr: false,
		},
		{
			name:    "score == prev_score",
			expr:    "score == prev_score",
			vars:    map[string]any{"score": 0.7, "prev_score": 0.7},
			want:    true,
			wantErr: false,
		},
		{
			name:    "score != prev_score",
			expr:    "score != prev_score",
			vars:    map[string]any{"score": 0.8, "prev_score": 0.7},
			want:    true,
			wantErr: false,
		},
		{
			name:    "logical AND",
			expr:    "score >= 0.8 && test.passed",
			vars:    map[string]any{"score": 0.85, "test.passed": true},
			want:    true,
			wantErr: false,
		},
		{
			name:    "logical AND false",
			expr:    "score >= 0.8 && test.passed",
			vars:    map[string]any{"score": 0.85, "test.passed": false},
			want:    false,
			wantErr: false,
		},
		{
			name:    "logical OR",
			expr:    "score >= 0.9 || test.passed",
			vars:    map[string]any{"score": 0.5, "test.passed": true},
			want:    true,
			wantErr: false,
		},
		{
			name:    "logical OR both false",
			expr:    "score >= 0.9 || test.passed",
			vars:    map[string]any{"score": 0.5, "test.passed": false},
			want:    false,
			wantErr: false,
		},
		{
			name:    "comparison <",
			expr:    "score < 0.5",
			vars:    map[string]any{"score": 0.3},
			want:    true,
			wantErr: false,
		},
		{
			name:    "comparison <=",
			expr:    "score <= 0.5",
			vars:    map[string]any{"score": 0.5},
			want:    true,
			wantErr: false,
		},
		{
			name:    "comparison >",
			expr:    "iteration > 5",
			vars:    map[string]any{"iteration": float64(6)},
			want:    true,
			wantErr: false,
		},
		{
			name:    "truthy variable",
			expr:    "test.passed",
			vars:    map[string]any{"test.passed": true},
			want:    true,
			wantErr: false,
		},
		{
			name:    "falsy variable",
			expr:    "test.passed",
			vars:    map[string]any{"test.passed": false},
			want:    false,
			wantErr: false,
		},
		{
			name:    "empty expression",
			expr:    "",
			vars:    map[string]any{},
			want:    false,
			wantErr: true,
		},
		{
			name:    "dot notation nested",
			expr:    "test.score > 0.5",
			vars:    map[string]any{"test": map[string]any{"score": 0.8}},
			want:    true,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.EvaluateExpression(tt.expr, tt.vars)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateExpression() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("EvaluateExpression(%q, %v) = %v, want %v", tt.expr, tt.vars, got, tt.want)
			}
		})
	}
}

func TestExpressionParser_EvaluateWithContext(t *testing.T) {
	p := NewExpressionParser()

	tests := []struct {
		name        string
		state       *LoopState
		snapshots   []*IterationSnapshot
		expr        string
		want        bool
		wantErr     bool
	}{
		{
			name: "score threshold with state",
			state: &LoopState{
				Iteration: 3,
				MaxIters:  10,
				EvalScore: float64Ptr(0.95),
			},
			snapshots: []*IterationSnapshot{},
			expr:      "score >= 0.9",
			want:      true,
			wantErr:   false,
		},
		{
			name: "score below threshold",
			state: &LoopState{
				Iteration: 3,
				MaxIters:  10,
				EvalScore: float64Ptr(0.5),
			},
			snapshots: []*IterationSnapshot{},
			expr:      "score >= 0.9",
			want:      false,
			wantErr:   false,
		},
		{
			name: "prev_score comparison",
			state: &LoopState{
				Iteration: 3,
				MaxIters:  10,
				EvalScore: float64Ptr(0.8),
				PrevScore: float64Ptr(0.7),
			},
			snapshots: []*IterationSnapshot{},
			expr:      "score > prev_score",
			want:      true,
			wantErr:   false,
		},
		{
			name: "iteration count",
			state: &LoopState{
				Iteration: 8,
				MaxIters:  10,
			},
			snapshots: []*IterationSnapshot{},
			expr:      "iteration >= max_iter",
			want:      false,
			wantErr:   false,
		},
		{
			name: "complex expression",
			state: &LoopState{
				Iteration: 5,
				MaxIters:  10,
				EvalScore: float64Ptr(0.75),
				PrevScore: float64Ptr(0.7),
			},
			snapshots: []*IterationSnapshot{},
			expr:      "score > prev_score && score >= 0.7",
			want:      true,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.EvaluateWithContext(tt.expr, tt.state, tt.snapshots)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateWithContext() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("EvaluateWithContext(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// ─── LoopState Termination Tests ─────────────────────────────────────────────

func TestLoopState_TerminateIf(t *testing.T) {
	makeSnap := func(iter int, score float64) *IterationSnapshot {
		return &IterationSnapshot{
			ID:        uuid.New(),
			RunID:     uuid.New(),
			StepID:    uuid.New(),
			Iteration: iter,
			Score:     score,
			Feedback:  "test",
			Timestamp: time.Now(),
		}
	}

	tests := []struct {
		name        string
		state       *LoopState
		snapshots   []*IterationSnapshot
		wantTerm    bool
		wantReason  TerminationReason
		wantStatus  string
	}{
		{
			name: "converged by score threshold",
			state: &LoopState{
				Iteration: 3,
				MaxIters:  10,
				EvalScore: float64Ptr(0.95),
				Snapshots: []*IterationSnapshot{},
			},
			snapshots:  []*IterationSnapshot{makeSnap(0, 0.5), makeSnap(1, 0.7), makeSnap(2, 0.9)},
			wantTerm:   true,
			wantReason: TerminationReasonConverged,
			wantStatus: "converged",
		},
		{
			name: "max iterations",
			state: &LoopState{
				Iteration: 10,
				MaxIters:  10,
				EvalScore: float64Ptr(0.5),
				Snapshots: []*IterationSnapshot{},
			},
			snapshots:  []*IterationSnapshot{},
			wantTerm:   true,
			wantReason: TerminationReasonMaxIterations,
			wantStatus: "max_iterations",
		},
		{
			name: "no progress",
			state: &LoopState{
				Iteration: 5,
				MaxIters:  10,
				EvalScore: float64Ptr(0.5),
				Snapshots: []*IterationSnapshot{},
			},
			snapshots:  []*IterationSnapshot{makeSnap(0, 0.5), makeSnap(1, 0.5), makeSnap(2, 0.5), makeSnap(3, 0.5)},
			wantTerm:   true,
			wantReason: TerminationReasonNoProgress,
			wantStatus: "no_progress",
		},
		{
			name: "continue iterating",
			state: &LoopState{
				Iteration: 2,
				MaxIters:  10,
				EvalScore: float64Ptr(0.6),
				Snapshots: []*IterationSnapshot{},
			},
			snapshots:  []*IterationSnapshot{makeSnap(0, 0.4), makeSnap(1, 0.5)},
			wantTerm:   false,
			wantReason: TerminationReasonNotYet,
			wantStatus: "running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up convergence checker manually since NewLoopState uses config.
			tt.state.ConvergenceChecker = NewConvergenceChecker(0.9, tt.state.MaxIters, 3)
			tt.state.Snapshots = tt.snapshots

			gotTerm, gotReason := tt.state.TerminateIf()
			if gotTerm != tt.wantTerm {
				t.Errorf("TerminateIf() = %v, want %v", gotTerm, tt.wantTerm)
			}
			if gotReason != tt.wantReason {
				t.Errorf("TerminateIf() reason = %v, want %v", gotReason, tt.wantReason)
			}
			if tt.wantTerm && tt.state.Status != tt.wantStatus {
				t.Errorf("TerminateIf() status = %v, want %v", tt.state.Status, tt.wantStatus)
			}
		})
	}
}

func TestLoopState_ShouldContinue(t *testing.T) {
	tests := []struct {
		name       string
		state      *LoopState
		wantCont   bool
		wantStatus string
	}{
		{
			name: "running state should continue",
			state: &LoopState{
				Iteration: 1,
				MaxIters:  10,
				Status:    "running",
			},
			wantCont:   true,
			wantStatus: "running",
		},
		{
			name: "already converged should not continue",
			state: &LoopState{
				Iteration: 5,
				MaxIters:  10,
				Status:    "converged",
				Converged: true,
			},
			wantCont:   false,
			wantStatus: "converged",
		},
		{
			name: "max iterations should not continue",
			state: &LoopState{
				Iteration: 10,
				MaxIters:  10,
				Status:    "running",
			},
			wantCont:   false,
			wantStatus: "max_iterations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.state.ConvergenceChecker = NewConvergenceChecker(0.9, tt.state.MaxIters, 3)
			got := tt.state.ShouldContinue()
			if got != tt.wantCont {
				t.Errorf("ShouldContinue() = %v, want %v", got, tt.wantCont)
			}
			if !got && tt.state.Status != tt.wantStatus {
				t.Errorf("ShouldContinue() status = %v, want %v", tt.state.Status, tt.wantStatus)
			}
		})
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func float64Ptr(v float64) *float64 {
	return &v
}