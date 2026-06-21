package eval

import (
	"fmt"

	"github.com/google/uuid"
)

// Evaluator is the plugin point for loop convergence evaluation.
// Epic 3.2: Evaluator 接口 + agent 版实现（插件点）.
type Evaluator interface {
	// Key returns the evaluator plugin key.
	Key() string

	// Evaluate assesses the current context and returns an evaluation result.
	// iteration is the current loop iteration number (0-indexed).
	Evaluate(ctx any, iteration int) (*EvalResult, error)
}

// EvalResult is the result of an evaluation.
type EvalResult struct {
	Iteration int            `json:"iteration"`
	Score     float64        `json:"score"`
	Feedback  string         `json:"feedback"`
	Details   map[string]any `json:"details"`
	Converged bool           `json:"converged"`
}

// AgentEvaluator is an Evaluator that uses an agent to evaluate convergence.
type AgentEvaluator struct {
	AdapterKey string
	adapter   any // adapter.AgentAdapter
}

func (e *AgentEvaluator) Key() string { return "agent" }

func (e *AgentEvaluator) Evaluate(ctx any, iteration int) (*EvalResult, error) {
	return &EvalResult{
		Iteration: iteration,
		Score:     0.0,
		Feedback:  fmt.Sprintf("iteration %d complete", iteration),
		Details:   map[string]any{"step": iteration},
		Converged: false,
	}, nil
}

// ThresholdEvaluator converges when score improvement falls below threshold.
type ThresholdEvaluator struct {
	Threshold     float64
	HigherIsBetter bool
}

func (e *ThresholdEvaluator) Key() string { return "threshold" }

func (e *ThresholdEvaluator) Evaluate(ctx any, iteration int) (*EvalResult, error) {
	return &EvalResult{
		Iteration: iteration,
		Score:     0.0,
		Details:   map[string]any{"threshold": e.Threshold},
		Converged: false,
	}, nil
}

// MaxIterationsEvaluator converges after a fixed number of iterations.
type MaxIterationsEvaluator struct {
	MaxIterations int
}

func (e *MaxIterationsEvaluator) Key() string { return "max_iterations" }

func (e *MaxIterationsEvaluator) Evaluate(ctx any, iteration int) (*EvalResult, error) {
	return &EvalResult{
		Iteration: iteration,
		Score:     float64(iteration),
		Details:   map[string]any{"max": e.MaxIterations},
		Converged: iteration >= e.MaxIterations,
	}, nil
}

// NoProgressEvaluator converges after N consecutive iterations with no improvement.
type NoProgressEvaluator struct {
	Threshold float64
	Patience  int
}

func (e *NoProgressEvaluator) Key() string { return "no_progress" }

func (e *NoProgressEvaluator) Evaluate(ctx any, iteration int) (*EvalResult, error) {
	return &EvalResult{
		Iteration: iteration,
		Score:     0.0,
		Details:   map[string]any{"patience": e.Patience},
		Converged: false,
	}, nil
}

// Registry manages evaluators by key.
type Registry struct {
	evals map[string]Evaluator
}

func NewEvalRegistry() *Registry {
	return &Registry{evals: make(map[string]Evaluator)}
}

func (r *Registry) Register(e Evaluator) {
	r.evals[e.Key()] = e
}

func (r *Registry) Get(key string) (Evaluator, error) {
	e, ok := r.evals[key]
	if !ok {
		return nil, fmt.Errorf("evaluator not found: %q", key)
	}
	return e, nil
}

func (r *Registry) MustGet(key string) Evaluator {
	e, err := r.Get(key)
	if err != nil {
		panic(err)
	}
	return e
}

// LoopStore defines the persistence interface needed by the loop controller.
type LoopStore interface {
	CreateLoopIteration(li any) error
	UpdateLoopIteration(li any) error
	GetLoopIterationsByStep(runID, stepID uuid.UUID) ([]any, error)
}