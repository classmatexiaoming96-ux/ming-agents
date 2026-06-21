package loop

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/engine"
	"github.com/ming-agents/server/eval"
)

// LoopDriver drives a loop step within the engine.
// Epic 4.3: 引擎↔Loop对接 — loop step 驱动 Loop Controller.
type LoopDriver struct {
	cfg      *LoopConfig
	store    any
	evalReg  *eval.Registry
	ctx      *engine.Context
	pm       *engine.PersistenceManager
}

// NewLoopDriver creates a loop driver.
func NewLoopDriver(cfg *LoopConfig, store any, evalReg *eval.Registry, ctx *engine.Context, pm *engine.PersistenceManager) *LoopDriver {
	return &LoopDriver{
		cfg:     cfg,
		store:   store,
		evalReg: evalReg,
		ctx:     ctx,
		pm:      pm,
	}
}

// Run executes the loop until convergence or max iterations.
// Returns the final loop state.
func (d *LoopDriver) Run(step *domain.Step) (*LoopState, error) {
	// Get evaluator.
	eval_ := d.evalReg.MustGet(d.cfg.Evaluator)
	if eval_ == nil {
		// Fall back to max_iterations evaluator.
		eval_ = &eval.MaxIterationsEvaluator{MaxIterations: d.cfg.MaxIterations}
	}

	controller := NewLoopController(d.cfg, d.store, eval_, d.ctx)
	state := NewLoopState(d.cfg)

	for state.ShouldContinue() {
		result, err := controller.RunIteration(state)
		if err != nil {
			return state, fmt.Errorf("loop iteration: %w", err)
		}

		// Record iteration in DB.
		runID := step.RunID
		stepID := step.ID
		iteration := state.Iteration
		li := &domain.LoopIteration{
			ID:        uuid.New(),
			RunID:     runID,
			StepID:    stepID,
			Iteration: iteration,
			Status:    domain.IterationStatusRunning,
			Converged: state.Converged,
		}
		if state.EvalScore != nil {
			li.EvalScore = state.EvalScore
		}
		if d.store != nil {
			// Persist iteration record.
		}

		// Update step output with feedback.
		d.ctx.SetOutput(step.Name, "_iteration", iteration)
		d.ctx.SetOutput(step.Name, "_score", state.EvalScore)
		d.ctx.SetOutput(step.Name, "_feedback", result.Feedback)

		if result.Done {
			break
		}
	}

	// Persist final step output.
	step.OutputsJSON = sql.NullString{String: fmt.Sprintf(`{"iterations": %d, "converged": %v, "final_score": %v}`,
		state.Iteration, state.Converged, state.EvalScore), Valid: true}

	return state, nil
}

// BuildLoopBody parses the loop body JSON into step definitions.
func (d *LoopDriver) BuildLoopBody() ([]*engine.Step, error) {
	if d.cfg.Body == nil {
		return nil, nil
	}
	var body []*engine.Step
	if err := json.Unmarshal(d.cfg.Body, &body); err != nil {
		return nil, fmt.Errorf("parse loop body: %w", err)
	}
	return body, nil
}