package engine

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/ming-agents/server/domain"
)

// RunRecorder captures resolved parameters during run execution.
// Epic 2.12: recordable and replayable dynamic decisions.
// Epic 2.13: also captures degradation events for alerting.
type RunRecorder struct {
	store              RunRecordStore
	degradationStore   DegradationStore
	runID              uuid.UUID
	templateName       string
	resolvedParams     map[string]map[string]any // stepName → inputs map
	assertions         []AssertionResult
	thresholds         map[string]float64
	skippedSteps       []SkippedStep
	totalSteps         int
}

// NewRunRecorder creates a new run recorder.
// degradationStore is optional (may be nil) - if provided, degradation events will be captured.
func NewRunRecorder(store RunRecordStore, runID uuid.UUID, templateName string, totalSteps int, degradationStore DegradationStore) *RunRecorder {
	return &RunRecorder{
		store:             store,
		degradationStore: degradationStore,
		runID:             runID,
		templateName:      templateName,
		resolvedParams:    make(map[string]map[string]any),
		thresholds:        make(map[string]float64),
		totalSteps:        totalSteps,
	}
}

// RecordResolvedParams records the resolved inputs for a step after template rendering.
func (r *RunRecorder) RecordResolvedParams(stepName string, params map[string]any) {
	r.resolvedParams[stepName] = params
}

// RecordAssertion records an assertion result for a step.
func (r *RunRecorder) RecordAssertion(stepName, assertion string, passed bool, actual any, err error) {
	result := AssertionResult{
		StepName:    stepName,
		Assertion:   assertion,
		Passed:      passed,
		ActualValue: actual,
	}
	if err != nil {
		result.Error = err.Error()
	}
	r.assertions = append(r.assertions, result)
}

// RecordThreshold records an effective threshold value.
func (r *RunRecorder) RecordThreshold(name string, value float64) {
	r.thresholds[name] = value
}

// RecordSkippedStep records a step that was skipped.
func (r *RunRecorder) RecordSkippedStep(stepName, reason string) {
	r.skippedSteps = append(r.skippedSteps, SkippedStep{
		StepName: stepName,
		Reason:   reason,
	})
}

// GetResolvedParams returns the recorded resolved params.
func (r *RunRecorder) GetResolvedParams() map[string]map[string]any {
	return r.resolvedParams
}

// GetAssertions returns the recorded assertions.
func (r *RunRecorder) GetAssertions() []AssertionResult {
	return r.assertions
}

// GetThresholds returns the recorded effective thresholds.
func (r *RunRecorder) GetThresholds() map[string]float64 {
	return r.thresholds
}

// GetSkippedSteps returns the recorded skipped steps.
func (r *RunRecorder) GetSkippedSteps() []SkippedStep {
	return r.skippedSteps
}

// RecordDegradation records a degradation event (parameter fallback).
// Epic 2.13
func (r *RunRecorder) RecordDegradation(evt DegradationEvent) error {
	evt.RunID = r.runID
	if evt.EventID == uuid.Nil {
		evt.EventID = uuid.New()
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	// Persist to degradation store if available.
	if r.degradationStore != nil {
		if err := r.degradationStore.SaveDegradation(evt); err != nil {
			return err
		}
	}
	return nil
}

// GetDegradationAlert returns the degradation alert for the recorded run.
// Returns nil if no degradation store is configured.
func (r *RunRecorder) GetDegradationAlert() *DegradationAlert {
	if r.degradationStore == nil {
		return nil
	}
	alert, err := r.degradationStore.GetDegradationAlert(r.runID)
	if err != nil {
		return nil
	}
	return &alert
}

// Save saves the recorded data as a RunRecord.
// Includes degradation alert if degradation store was configured.
// CriticallyResults are included for Epic 2.14.
func (r *RunRecorder) Save(runStatus string, criticallyResults []CriticallyResult) error {
	rec := RunRecord{
		RunID:               r.runID,
		TemplateName:        r.templateName,
		Timestamp:           time.Now().UTC(),
		ResolvedParams:      r.resolvedParams,
		EvaluatedAssertions: r.assertions,
		EffectiveThresholds: r.thresholds,
		SkippedSteps:        r.skippedSteps,
		RunStatus:           runStatus,
		TotalSteps:          r.totalSteps,
		CriticallyResults:  criticallyResults, // Epic 2.14
	}

	// Include degradation alert if available (Epic 2.13).
	if alert := r.GetDegradationAlert(); alert != nil && alert.DegradationCount > 0 {
		rec.DegradationAlert = alert
	}

	return r.store.SaveRecord(rec)
}

// ReplayParams extracts the resolved parameters from a RunRecord.
// Used by Scheduler.ReplayParams to restore parameters from a record.
func ReplayParams(rec RunRecord) map[string]map[string]any {
	return rec.ResolvedParams
}

// ReplayThresholds extracts the effective thresholds from a RunRecord.
func ReplayThresholds(rec RunRecord) map[string]float64 {
	return rec.EffectiveThresholds
}

// replayAssertions extracts assertion results from a RunRecord.
func ReplayAssertions(rec RunRecord) []AssertionResult {
	return rec.EvaluatedAssertions
}

// replaySkippedSteps extracts skipped step info from a RunRecord.
func ReplaySkippedSteps(rec RunRecord) []SkippedStep {
	return rec.SkippedSteps
}

// recordDriver is a helper that hooks into the driver to record resolved params.
// It wraps a RunRecorder and provides methods to be called at key execution points.
type recordDriver struct {
	recorder *RunRecorder
	ctx      *Context
}

// newRecordDriver creates a record driver for a given run.
func newRecordDriver(rec *RunRecorder) *recordDriver {
	return &recordDriver{recorder: rec}
}

// onStepTranslate is called when a step is translated into tasks.
// It captures the resolved inputs after template rendering.
func (rd *recordDriver) onStepTranslate(step *domain.Step, resolvedInputs map[string]any) {
	// Only record if there are resolved inputs.
	if len(resolvedInputs) > 0 {
		rd.recorder.RecordResolvedParams(step.Name, resolvedInputs)
	}
}

// onStepSkip is called when a step is skipped due to when-condition.
func (rd *recordDriver) onStepSkip(stepName, reason string) {
	rd.recorder.RecordSkippedStep(stepName, reason)
}

// onStepComplete is called when a step completes with its outputs.
func (rd *recordDriver) onStepComplete(stepName string, outputs map[string]any) {
	// outputs are already captured in resolved params during translation.
	// This hook can be used for additional recording if needed.
}

// resolveInputsForRecord resolves step inputs for recording purposes.
// This is called after the translator resolves inputs with templates.
func resolveInputsForRecord(step *domain.Step, ctx *Context) (map[string]any, error) {
	if !step.InputsJSON.Valid {
		return make(map[string]any), nil
	}
	var inputs map[string]any
	if err := json.Unmarshal([]byte(step.InputsJSON.String), &inputs); err != nil {
		return nil, err
	}
	rendered := make(map[string]any)
	for k, v := range inputs {
		if s, ok := v.(string); ok {
			rendered[k] = ctx.RenderTemplate(s)
		} else {
			rendered[k] = v
		}
	}
	return rendered, nil
}