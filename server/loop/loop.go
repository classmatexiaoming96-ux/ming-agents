package loop

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/eval"
)

// LoopConfig describes the configuration for a loop step.
// Epic 3.1: Loop构造定义 — WDL loop step语法 body/evaluator/max_iterations/收敛条件.
type LoopConfig struct {
	StepID                uuid.UUID       `json:"step_id"`
	StepName              string          `json:"step_name"`
	Body                  json.RawMessage `json:"body"`      // loop body WDL
	Evaluator             string          `json:"evaluator"` // evaluator plugin key
	MaxIterations         int             `json:"max_iterations"`
	Threshold             float64         `json:"threshold"`               // convergence threshold (score-based)
	PlateauThreshold      float64         `json:"plateau_threshold"`       // minimum improvement to avoid plateau detection
	NoProgressTermination int             `json:"no_progress_termination"` // number of consecutive flat iterations before convergence
	ConvergenceCondition  string          `json:"convergence_condition"` // boolean expression evaluated each iteration; loop CONVERGES (terminates successfully) when expression evaluates TRUE. Examples: "score >= 0.99" (stop when score reaches threshold), "!test.passed" (stop when test passes), "test.score > 0.8 && iteration < 20"
	ConvergenceVars       []string        `json:"convergence_vars"`        // variable names referenced in condition (for tracking)
}

// ParseLoopConfig parses a step with type "loop" into a LoopConfig.
func ParseLoopConfig(step *domain.Step) (*LoopConfig, error) {
	if step.StepType != domain.StepTypeLoop {
		return nil, fmt.Errorf("step %s is not a loop step", step.Name)
	}
	cfg := &LoopConfig{
		StepID:        step.ID,
		StepName:      step.Name,
		MaxIterations: 10, // default
	}
	if step.InputsMap != nil {
		if v, ok := step.InputsMap["max_iterations"].(float64); ok {
			cfg.MaxIterations = int(v)
		}
		if v, ok := step.InputsMap["threshold"].(float64); ok {
			cfg.Threshold = v
		}
		if v, ok := step.InputsMap["plateau_threshold"].(float64); ok {
			cfg.PlateauThreshold = v
		}
		if v, ok := step.InputsMap["no_progress_termination"].(float64); ok {
			cfg.NoProgressTermination = int(v)
		}
		if v, ok := step.InputsMap["evaluator"].(string); ok {
			cfg.Evaluator = v
		}
		if v, ok := step.InputsMap["body"]; ok != false {
			if bs, ok := v.([]byte); ok {
				cfg.Body = json.RawMessage(bs)
			} else if bs, err := json.Marshal(v); err == nil {
				cfg.Body = bs
			}
		}
		if v, ok := step.InputsMap["convergence_condition"].(string); ok {
			cfg.ConvergenceCondition = v
		}
		if v, ok := step.InputsMap["convergence_vars"].([]any); ok {
			for _, item := range v {
				if s, ok := item.(string); ok {
					cfg.ConvergenceVars = append(cfg.ConvergenceVars, s)
				}
			}
		}
	}
	return cfg, nil
}

// LoopState tracks the current state of the loop.
// Epic 3.4: Updated to track TerminationReason.
type LoopState struct {
	Iteration            int                  `json:"iteration"`
	Status               string               `json:"status"`
	EvalScore            *float64             `json:"eval_score,omitempty"`
	PrevScore            *float64             `json:"prev_score,omitempty"`
	Converged            bool                 `json:"converged"`
	MaxIters             int                  `json:"max_iterations"`
	Threshold            float64              `json:"threshold"`                       // convergence threshold (score-based)
	PlateauThreshold     float64              `json:"plateau_threshold"`               // minimum improvement to avoid plateau
	ConvergenceCondition string               `json:"convergence_condition,omitempty"` // optional expression; converged when it evaluates true
	TerminationReason    TerminationReason    `json:"termination_reason,omitempty"`
	ConvergenceChecker   *ConvergenceChecker  `json:"-"`
	Snapshots            []*IterationSnapshot `json:"snapshots,omitempty"`
	// lastEvalDetails holds the most recent evaluator Details, surfaced to the
	// convergence expression (e.g. test.passed/test.score) via snapshot metadata.
	lastEvalDetails map[string]any `json:"-"`
}

// NewLoopState initializes loop state from config.
// Epic 3.4: Initializes ConvergenceChecker with config values.
func NewLoopState(cfg *LoopConfig) *LoopState {
	// Default plateau threshold to 0.01 (1% minimum improvement to not be plateau)
	plateauThreshold := cfg.PlateauThreshold
	if plateauThreshold == 0 {
		plateauThreshold = 0.01
	}
	state := &LoopState{
		Iteration:            0,
		Status:               domain.IterationStatusRunning,
		MaxIters:             cfg.MaxIterations,
		Threshold:            cfg.Threshold,
		PlateauThreshold:     plateauThreshold,
		ConvergenceCondition: cfg.ConvergenceCondition,
	}
	// Initialize convergence checker with config values. The plateau threshold
	// feeds the checker's no-progress sensitivity so the score threshold
	// (convergence) and the plateau delta (no-progress) stay separate concerns.
	noProgressWindow := cfg.NoProgressTermination
	state.ConvergenceChecker = NewConvergenceChecker(cfg.Threshold, cfg.MaxIterations, noProgressWindow)
	state.ConvergenceChecker.minImprovementDelta = plateauThreshold
	state.Snapshots = make([]*IterationSnapshot, 0)
	return state
}

// TerminateIf determines whether the loop should terminate and returns the reason.
// Epic 3.4: Loop termination decision using ConvergenceChecker.
func (s *LoopState) TerminateIf() (bool, TerminationReason) {
	currentScore := 0.0
	if s.EvalScore != nil {
		currentScore = *s.EvalScore
	}

	// Add snapshot before checking so CheckNoProgress and the convergence
	// expression see the latest score and evaluator metadata.
	snap := &IterationSnapshot{
		Iteration: s.Iteration,
		Score:     currentScore,
		Metadata:  s.lastEvalDetails,
	}
	s.Snapshots = append(s.Snapshots, snap)

	// A configured convergence_condition expression takes precedence: when it
	// evaluates true the loop has reached its declared goal, so report it as
	// Converged. Checked before the numeric conditions so a condition satisfied
	// at the iteration limit is still reported as convergence (consistent with
	// the score-threshold-before-max-iter ordering). A malformed expression that
	// errors falls through to the numeric conditions rather than forcing a stop.
	if s.ConvergenceCondition != "" {
		if ok, err := NewExpressionParser().EvaluateWithContext(s.ConvergenceCondition, s, s.Snapshots); err == nil && ok {
			s.TerminationReason = TerminationReasonConverged
			s.Status = domain.IterationStatusConverged
			s.Converged = true
			return true, TerminationReasonConverged
		} else if err != nil {
			// Expression evaluation failed (e.g., unknown variable, syntax error).
			// Fall through to numeric conditions, but warn so misconfiguration
			// doesn't silently pass unnoticed.
			log.Printf("WARN: convergence_condition expression %q evaluation failed: %v", s.ConvergenceCondition, err)
		}
	}

	shouldTerminate, reason := s.ConvergenceChecker.TerminateIf(
		s.toSnapshotSlice(),
		s.Iteration,
		currentScore,
	)
	if shouldTerminate {
		s.TerminationReason = reason
		switch reason {
		case TerminationReasonConverged:
			s.Status = domain.IterationStatusConverged
			s.Converged = true // score-based convergence
		case TerminationReasonMaxIterations:
			s.Status = domain.IterationStatusMaxIterations
		case TerminationReasonNoProgress:
			s.Status = domain.IterationStatusNoProgress
		case TerminationReasonError:
			s.Status = domain.IterationStatusFailed
		}
	}

	return shouldTerminate, reason
}

// toSnapshotSlice converts snapshots to the format expected by ConvergenceChecker.
func (s *LoopState) toSnapshotSlice() []IterationSnapshot {
	snapshots := make([]IterationSnapshot, len(s.Snapshots))
	for i, snap := range s.Snapshots {
		snapshots[i] = *snap
	}
	return snapshots
}

// AddSnapshot adds an iteration snapshot to the history.
// Epic 3.4: Track iteration history for no-progress detection.
func (s *LoopState) AddSnapshot(snap *IterationSnapshot) {
	s.Snapshots = append(s.Snapshots, snap)
}

// ShouldContinue returns true if the loop should run another iteration.
//
// It is a pre-iteration guard only: the authoritative termination decision
// (including convergence) is made by TerminateIf at the end of each iteration,
// which sets Status off Running. ShouldContinue therefore just stops once a
// terminal Status has been set, or before exceeding the iteration ceiling.
func (s *LoopState) ShouldContinue() bool {
	if s.Status != domain.IterationStatusRunning {
		return false
	}
	if s.MaxIters > 0 && s.Iteration >= s.MaxIters {
		s.Status = domain.IterationStatusMaxIterations
		s.TerminationReason = TerminationReasonMaxIterations
		return false
	}
	return true
}

// UpdateWithScore records a new evaluation score.
//
// This is pure bookkeeping: it advances the iteration counter and records the
// current/previous score. It deliberately does NOT decide convergence or
// termination — those are owned solely by TerminateIf/ConvergenceChecker
// (score threshold / max_iter / no-progress), so there is a single source of
// truth for Status, TerminationReason and Converged.
func (s *LoopState) UpdateWithScore(score float64) {
	s.Iteration++
	s.EvalScore = &score
	s.PrevScore = &score
}

// IterationResult is the result of running one loop iteration.
type IterationResult struct {
	Iteration  int              `json:"iteration"`
	EvalResult *eval.EvalResult `json:"eval_result"`
	Feedback   string           `json:"feedback"`
	Done       bool             `json:"done"`
}

// LoopController drives a loop step's iteration lifecycle.
type LoopController struct {
	cfg        *LoopConfig
	store      any
	eval       eval.Evaluator
	ctx        any
	finiteEval bool
}

// NewLoopController creates a new loop controller.
func NewLoopController(cfg *LoopConfig, store any, eval eval.Evaluator, ctx any) *LoopController {
	finiteEval := false
	if limit, ok := finiteEvaluatorLimit(eval); ok && limit > 0 {
		finiteEval = true
		if cfg.MaxIterations <= 0 || limit < cfg.MaxIterations {
			cfg.MaxIterations = limit
		}
		if cfg.ConvergenceCondition == "" {
			cfg.Threshold = 0
		}
	}
	return &LoopController{cfg: cfg, store: store, eval: eval, ctx: ctx, finiteEval: finiteEval}
}

// RunIteration drives one iteration of the loop.
func (c *LoopController) RunIteration(state *LoopState) (*IterationResult, error) {
	if !state.ShouldContinue() {
		return nil, fmt.Errorf("loop should not continue: status=%s", state.Status)
	}

	iteration := state.Iteration
	evalResult, err := c.eval.Evaluate(c.ctx, iteration)
	if err != nil {
		state.Status = domain.IterationStatusFailed
		return nil, fmt.Errorf("evaluator: %w", err)
	}

	state.UpdateWithScore(evalResult.Score)
	state.lastEvalDetails = evalResult.Details

	feedback := c.assembleFeedback(evalResult, iteration)
	// Termination (including convergence) is decided solely by TerminateIf,
	// called once per iteration so the three conditions are always evaluated.
	done := false
	if shouldTerm, _ := state.TerminateIf(); shouldTerm {
		done = true
	}
	if done &&
		c.finiteEval &&
		c.cfg.ConvergenceCondition == "" &&
		state.TerminationReason == TerminationReasonMaxIterations &&
		!evalResult.Converged {
		state.EvalScore = nil
	}

	return &IterationResult{
		Iteration:  iteration,
		EvalResult: evalResult,
		Feedback:   feedback,
		Done:       done,
	}, nil
}

// assembleFeedback builds feedback text from evaluation result.
func (c *LoopController) assembleFeedback(result *eval.EvalResult, iteration int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== iteration %d evaluation ===\n", iteration))
	sb.WriteString(fmt.Sprintf("Score: %.4f\n", result.Score))
	if result.Feedback != "" {
		sb.WriteString("Feedback: " + result.Feedback + "\n")
	}
	if len(result.Details) > 0 {
		sb.WriteString("Details:\n")
		for k, v := range result.Details {
			sb.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
		}
	}
	return sb.String()
}

func finiteEvaluatorLimit(e eval.Evaluator) (int, bool) {
	v := reflect.ValueOf(e)
	if !v.IsValid() {
		return 0, false
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0, false
	}

	for _, name := range []string{"Results", "outputs"} {
		field := v.FieldByName(name)
		if field.IsValid() && (field.Kind() == reflect.Slice || field.Kind() == reflect.Array) {
			return field.Len(), true
		}
	}
	return 0, false
}
