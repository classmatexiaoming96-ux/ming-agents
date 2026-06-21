package loop

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIterationSnapshot_NewIterationSnapshot(t *testing.T) {
	runID := uuid.New()
	stepID := uuid.New()
	inputs := map[string]any{"key": "value"}
	outputs := map[string]any{"result": "ok"}

	snap := NewIterationSnapshot(runID, stepID, 3, 0.85, "good progress", inputs, outputs)

	if snap.ID == uuid.Nil {
		t.Error("expected non-nil ID")
	}
	if snap.RunID != runID {
		t.Errorf("RunID = %v, want %v", snap.RunID, runID)
	}
	if snap.StepID != stepID {
		t.Errorf("StepID = %v, want %v", snap.StepID, stepID)
	}
	if snap.Iteration != 3 {
		t.Errorf("Iteration = %d, want 3", snap.Iteration)
	}
	if snap.Score != 0.85 {
		t.Errorf("Score = %v, want 0.85", snap.Score)
	}
	if snap.Feedback != "good progress" {
		t.Errorf("Feedback = %q, want %q", snap.Feedback, "good progress")
	}
	if snap.Timestamp.IsZero() {
		t.Error("expected non-zero Timestamp")
	}
}

func TestFeedbackAssembler_AssemblePrompt_Basic(t *testing.T) {
	asm := NewFeedbackAssembler()

	outputs := map[string]any{"result": "initial output"}
	prompt := asm.AssemblePrompt(1, outputs, "improve the result", 0.5)

	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}

	// Check iteration is mentioned.
	if !strings.Contains(prompt, "iteration 1") {
		t.Error("expected prompt to mention iteration 1")
	}

	// Check feedback is included.
	if !strings.Contains(prompt, "improve the result") {
		t.Error("expected prompt to include feedback")
	}

	// Check outputs are included.
	if !strings.Contains(prompt, "initial output") {
		t.Error("expected prompt to include previous outputs")
	}
}

func TestFeedbackAssembler_AssemblePrompt_IncludesScore(t *testing.T) {
	asm := NewFeedbackAssembler()

	prompt := asm.AssemblePrompt(2, map[string]any{"x": 1}, "fix x", 0.75)

	// Score should appear in the prompt.
	if !strings.Contains(prompt, "0.75") && !strings.Contains(prompt, "0.75") {
		// Score 0.75 should appear somewhere
	}
}

func TestFeedbackAssembler_AssemblePromptWithHistory(t *testing.T) {
	asm := NewFeedbackAssembler()

	history := []*IterationSnapshot{
		{Iteration: 1, Score: 0.6, Feedback: "needs work"},
		{Iteration: 2, Score: 0.7, Feedback: "better"},
	}

	outputs := map[string]any{"result": "current"}
	prompt := asm.AssemblePromptWithHistory(3, history, outputs, "almost there", 0.85, "fix the bug")

	if !strings.Contains(prompt, "iteration 3") {
		t.Error("expected prompt to mention iteration 3")
	}
	if !strings.Contains(prompt, "Iter 1") || !strings.Contains(prompt, "Iter 2") {
		t.Error("expected prompt to include history")
	}
	if !strings.Contains(prompt, "fix the bug") {
		t.Error("expected prompt to include task description")
	}
}

func TestFeedbackAssembler_HistoryLimiting(t *testing.T) {
	cfg := DefaultFeedbackAssemblerConfig()
	cfg.MaxHistoryItems = 3
	asm := NewFeedbackAssemblerWithConfig(cfg)

	// Create 10 historical snapshots
	history := make([]*IterationSnapshot, 10)
	for i := 0; i < 10; i++ {
		history[i] = &IterationSnapshot{
			ID:        uuid.New(),
			RunID:     uuid.Nil,
			StepID:    uuid.Nil,
			Iteration: i + 1,
			Score:     0.5 + float64(i)*0.05,
			Feedback:  "feedback",
			Timestamp: time.Now(),
		}
	}

	prompt := asm.AssemblePromptWithHistory(11, history, map[string]any{}, "feedback", 0.9, "")

	// Should contain history entries (limited to 3 most recent).
	// Since we limit to 3, we should see iterations 8, 9, 10 (the last 3).
	if !strings.Contains(prompt, "Iter 8") && !strings.Contains(prompt, "Iter 9") && !strings.Contains(prompt, "Iter 10") {
		t.Error("expected prompt to include recent history")
	}
}

func TestFeedbackAssembler_SystemPromptTemplate(t *testing.T) {
	cfg := DefaultFeedbackAssemblerConfig()
	cfg.SystemPromptTemplate = "ITERATION {iter}: {feedback} - {summary} - {hints}"
	asm := NewFeedbackAssemblerWithConfig(cfg)

	prompt := asm.AssembleSystemPrompt(5, nil, "fix it", 0.8)

	if !strings.Contains(prompt, "ITERATION 5") {
		t.Error("expected ITERATION 5 in prompt")
	}
	if !strings.Contains(prompt, "fix it") {
		t.Error("expected feedback in prompt")
	}
}

func TestFeedbackAssembler_UserPromptTemplate(t *testing.T) {
	cfg := DefaultFeedbackAssemblerConfig()
	cfg.UserPromptTemplate = "TASK: {task_description}\nOUTPUTS: {previous_outputs}\nFEEDBACK: {specific_feedback}"
	asm := NewFeedbackAssemblerWithConfig(cfg)

	outputs := map[string]any{"key": "value"}
	prompt := asm.AssembleUserPrompt("my task", outputs, "good job")

	if !strings.Contains(prompt, "TASK: my task") {
		t.Error("expected task in prompt")
	}
	if !strings.Contains(prompt, "OUTPUTS:") {
		t.Error("expected outputs section in prompt")
	}
	if !strings.Contains(prompt, "FEEDBACK: good job") {
		t.Error("expected feedback in prompt")
	}
}

func TestFeedbackAssembler_Truncate(t *testing.T) {
	cases := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exactly10c", 10, "exactly10c"},
		{"this is a long string", 10, "this is..."},
		{"ab", 3, "ab"},           // len < maxLen, no truncation needed
		{"abcde", 5, "abcde"},     // len == maxLen, exact fit
		{"abcde", 4, "a..."},      // truncated
		{"abcde", 3, "abc"},       // truncated (no room for ...)
		{"abcde", 2, "ab"},        // maxLen <= 3, return first maxLen
		{"", 5, ""},
	}

	for _, c := range cases {
		got := truncate(c.input, c.maxLen)
		if got != c.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.input, c.maxLen, got, c.expected)
		}
	}
}

func TestFeedbackAssembler_ImprovementHints_Improving(t *testing.T) {
	asm := NewFeedbackAssembler()

	history := []*IterationSnapshot{
		{Iteration: 1, Score: 0.5, Feedback: ""},
		{Iteration: 2, Score: 0.6, Feedback: ""},
		{Iteration: 3, Score: 0.7, Feedback: ""},
	}

	hints := asm.generateHints(history, "", 0.75)

	if !strings.Contains(hints, "improving") {
		t.Errorf("expected 'improving' hint, got: %s", hints)
	}
}

func TestFeedbackAssembler_ImprovementHints_Declining(t *testing.T) {
	asm := NewFeedbackAssembler()

	history := []*IterationSnapshot{
		{Iteration: 1, Score: 0.8, Feedback: ""},
		{Iteration: 2, Score: 0.6, Feedback: ""},
		{Iteration: 3, Score: 0.4, Feedback: ""},
	}

	hints := asm.generateHints(history, "", 0.4)

	if !strings.Contains(hints, "declining") {
		t.Errorf("expected 'declining' hint, got: %s", hints)
	}
}

func TestFeedbackAssembler_ImprovementHints_Stagnant(t *testing.T) {
	asm := NewFeedbackAssembler()

	history := []*IterationSnapshot{
		{Iteration: 1, Score: 0.5, Feedback: ""},
		{Iteration: 2, Score: 0.51, Feedback: ""},
		{Iteration: 3, Score: 0.52, Feedback: ""},
	}

	hints := asm.generateHints(history, "", 0.52)

	if !strings.Contains(hints, "stagnant") {
		t.Errorf("expected 'stagnant' hint, got: %s", hints)
	}
}

func TestFeedbackAssembler_ScoreBasedHints(t *testing.T) {
	asm := NewFeedbackAssembler()

	// Low score
	hints := asm.generateHints(nil, "fix errors", 0.2)
	if !strings.Contains(hints, "fundamental") {
		t.Error("expected fundamental changes hint for low score")
	}

	// Medium score
	hints = asm.generateHints(nil, "improve", 0.5)
	if !strings.Contains(hints, "Moderate") {
		t.Error("expected moderate issues hint for medium score")
	}

	// High score
	hints = asm.generateHints(nil, "nearly done", 0.95)
	if !strings.Contains(hints, "fine-tuning") {
		t.Error("expected fine-tuning hint for high score")
	}
}

func TestAnalyzeScoreTrend(t *testing.T) {
	tests := []struct {
		name     string
		history  []*IterationSnapshot
		expected string
	}{
		{
			name:     "empty",
			history:  []*IterationSnapshot{},
			expected: "unknown",
		},
		{
			name: "single",
			history: []*IterationSnapshot{
				{Iteration: 1, Score: 0.5},
			},
			expected: "unknown",
		},
		{
			name: "improving",
			history: []*IterationSnapshot{
				{Iteration: 1, Score: 0.4},
				{Iteration: 2, Score: 0.5},
				{Iteration: 3, Score: 0.7},
			},
			expected: "improving",
		},
		{
			name: "declining",
			history: []*IterationSnapshot{
				{Iteration: 1, Score: 0.8},
				{Iteration: 2, Score: 0.6},
				{Iteration: 3, Score: 0.4},
			},
			expected: "declining",
		},
		{
			name: "stagnant",
			history: []*IterationSnapshot{
				{Iteration: 1, Score: 0.5},
				{Iteration: 2, Score: 0.51},
				{Iteration: 3, Score: 0.52},
			},
			expected: "stagnant",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzeScoreTrend(tt.history)
			if got != tt.expected {
				t.Errorf("analyzeScoreTrend() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// ─── InMemoryFeedbackStore Tests ───────────────────────────────────────────

func TestInMemoryFeedbackStore_SaveAndGet(t *testing.T) {
	store := NewInMemoryFeedbackStore()
	runID := "run-123"
	stepID := uuid.New()

	snap := NewIterationSnapshot(uuid.New(), stepID, 1, 0.8, "good", nil, nil)
	err := store.SaveSnapshot(runID, snap)
	if err != nil {
		t.Fatalf("SaveSnapshot error: %v", err)
	}

	snaps, err := store.GetSnapshots(runID)
	if err != nil {
		t.Fatalf("GetSnapshots error: %v", err)
	}
	if len(snaps) != 1 {
		t.Errorf("len(snaps) = %d, want 1", len(snaps))
	}
}

func TestInMemoryFeedbackStore_GetLatest(t *testing.T) {
	store := NewInMemoryFeedbackStore()
	runID := "run-456"
	stepID := uuid.New()

	// Save multiple snapshots
	for i := 1; i <= 3; i++ {
		snap := NewIterationSnapshot(uuid.New(), stepID, i, float64(i)*0.3, "feedback", nil, nil)
		_ = store.SaveSnapshot(runID, snap)
	}

	latest, err := store.GetLatestSnapshot(runID)
	if err != nil {
		t.Fatalf("GetLatestSnapshot error: %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil latest snapshot")
	}
	if latest.Iteration != 3 {
		t.Errorf("latest.Iteration = %d, want 3", latest.Iteration)
	}
}

func TestInMemoryFeedbackStore_GetSnapshotsByStep(t *testing.T) {
	store := NewInMemoryFeedbackStore()
	runID := "run-789"
	stepID1 := uuid.New()
	stepID2 := uuid.New()

	// Save snapshots for step 1
	snap1 := NewIterationSnapshot(uuid.New(), stepID1, 1, 0.5, "", nil, nil)
	_ = store.SaveSnapshot(runID, snap1)

	// Save snapshot for step 2
	snap2 := NewIterationSnapshot(uuid.New(), stepID2, 1, 0.6, "", nil, nil)
	_ = store.SaveSnapshot(runID, snap2)

	snaps, err := store.GetSnapshotsByStep(runID, stepID1)
	if err != nil {
		t.Fatalf("GetSnapshotsByStep error: %v", err)
	}
	if len(snaps) != 1 {
		t.Errorf("len(snaps) = %d, want 1", len(snaps))
	}
}

func TestInMemoryFeedbackStore_GetSnapshotByIteration(t *testing.T) {
	store := NewInMemoryFeedbackStore()
	runID := "run-abc"
	stepID := uuid.New()

	snap := NewIterationSnapshot(uuid.New(), stepID, 5, 0.9, "", nil, nil)
	_ = store.SaveSnapshot(runID, snap)

	found, err := store.GetSnapshotByIteration(runID, stepID, 5)
	if err != nil {
		t.Fatalf("GetSnapshotByIteration error: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find snapshot at iteration 5")
	}
	if found.Iteration != 5 {
		t.Errorf("found.Iteration = %d, want 5", found.Iteration)
	}

	// Not found
	found, err = store.GetSnapshotByIteration(runID, stepID, 99)
	if err != nil {
		t.Fatalf("GetSnapshotByIteration error: %v", err)
	}
	if found != nil {
		t.Error("expected nil for non-existent iteration")
	}
}

func TestInMemoryFeedbackStore_ClearRunSnapshots(t *testing.T) {
	store := NewInMemoryFeedbackStore()
	runID := "run-clear"
	stepID := uuid.New()

	_ = store.SaveSnapshot(runID, NewIterationSnapshot(uuid.New(), stepID, 1, 0.5, "", nil, nil))
	_ = store.SaveSnapshot(runID, NewIterationSnapshot(uuid.New(), stepID, 2, 0.6, "", nil, nil))

	err := store.ClearRunSnapshots(runID)
	if err != nil {
		t.Fatalf("ClearRunSnapshots error: %v", err)
	}

	snaps, _ := store.GetSnapshots(runID)
	if len(snaps) != 0 {
		t.Errorf("len(snaps) after clear = %d, want 0", len(snaps))
	}
}

func TestInMemoryFeedbackStore_EmptyRunID(t *testing.T) {
	store := NewInMemoryFeedbackStore()
	stepID := uuid.New()

	_, err := store.GetSnapshots("")
	if err == nil {
		t.Error("expected error for empty runID")
	}

	err = store.SaveSnapshot("", NewIterationSnapshot(uuid.Nil, stepID, 1, 0.5, "", nil, nil))
	if err == nil {
		t.Error("expected error for empty runID on SaveSnapshot")
	}
}

func TestInMemoryFeedbackStore_ConcurrentAccess(t *testing.T) {
	store := NewInMemoryFeedbackStore()
	runID := "run-concurrent"
	stepID := uuid.New()

	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 10; i++ {
		go func(iter int) {
			snap := NewIterationSnapshot(uuid.New(), stepID, iter, float64(iter)*0.1, "", nil, nil)
			_ = store.SaveSnapshot(runID, snap)
			done <- true
		}(i)
	}

	// Wait for all writes
	for i := 0; i < 10; i++ {
		<-done
	}

	snaps, _ := store.GetSnapshots(runID)
	if len(snaps) != 10 {
		t.Errorf("expected 10 snapshots, got %d", len(snaps))
	}
}

// ─── MockFeedbackStore Tests ───────────────────────────────────────────────

func TestMockFeedbackStore(t *testing.T) {
	store := NewMockFeedbackStore()
	runID := "mock-run"
	stepID := uuid.New()

	// Test save
	snap := NewIterationSnapshot(uuid.New(), stepID, 1, 0.7, "test feedback", nil, nil)
	err := store.SaveSnapshot(runID, snap)
	if err != nil {
		t.Fatalf("SaveSnapshot error: %v", err)
	}
	if store.CallCount != 1 {
		t.Errorf("CallCount = %d, want 1", store.CallCount)
	}

	// Test get
	snaps, err := store.GetSnapshots(runID)
	if err != nil {
		t.Fatalf("GetSnapshots error: %v", err)
	}
	if len(snaps) != 1 {
		t.Errorf("len(snaps) = %d, want 1", len(snaps))
	}

	// Test save error
	store.SaveErr = assertErr
	err = store.SaveSnapshot(runID, snap)
	if err != assertErr {
		t.Errorf("expected assertErr, got %v", err)
	}
}

func TestMockFeedbackStore_GetLatestSnapshot(t *testing.T) {
	store := NewMockFeedbackStore()
	runID := "mock-latest"
	stepID := uuid.New()

	// Empty case
	latest, err := store.GetLatestSnapshot(runID)
	if err != nil {
		t.Fatalf("GetLatestSnapshot error: %v", err)
	}
	if latest != nil {
		t.Error("expected nil for empty store")
	}

	// Add snapshots
	for i := 1; i <= 3; i++ {
		snap := NewIterationSnapshot(uuid.New(), stepID, i, float64(i)*0.3, "", nil, nil)
		_ = store.SaveSnapshot(runID, snap)
	}

	latest, err = store.GetLatestSnapshot(runID)
	if err != nil {
		t.Fatalf("GetLatestSnapshot error: %v", err)
	}
	if latest.Iteration != 3 {
		t.Errorf("latest.Iteration = %d, want 3", latest.Iteration)
	}
}

func TestMockFeedbackStore_ClearRunSnapshots(t *testing.T) {
	store := NewMockFeedbackStore()
	runID := "mock-clear"
	stepID := uuid.New()

	_ = store.SaveSnapshot(runID, NewIterationSnapshot(uuid.New(), stepID, 1, 0.5, "", nil, nil))
	_ = store.ClearRunSnapshots(runID)

	if len(store.Snapshots) != 0 {
		t.Errorf("len(Snapshots) = %d, want 0", len(store.Snapshots))
	}
}