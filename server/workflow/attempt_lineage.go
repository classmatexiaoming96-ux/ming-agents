package workflow

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var attemptLineageLocks sync.Map

func AppendAttemptEvent(repoRoot string, event AttemptEvent) error {
	if err := validatePathID(event.RunID, "run_id"); err != nil {
		return fmt.Errorf("append attempt event: %w", err)
	}
	if err := validatePathID(event.NodeID, "node_id"); err != nil {
		return fmt.Errorf("append attempt event: %w", err)
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal attempt event: %w", err)
	}
	line := append(data, '\n')

	lock := attemptLineageLock(repoRoot, event.RunID)
	lock.Lock()
	defer lock.Unlock()

	paths := []string{
		attemptNodePath(repoRoot, event.RunID, event.NodeID),
		attemptIndexPath(repoRoot, event.RunID),
	}
	if event.Scope != "" {
		paths = append(paths, attemptScopePath(repoRoot, event.RunID, event.NodeID, event.Scope))
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("prepare attempt event path %s: %w", path, err)
		}
	}
	states, err := captureAppendStates(paths)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := appendJSONL(path, line); err != nil {
			rollbackAppends(states)
			return fmt.Errorf("append attempt event to %s: %w", path, err)
		}
	}
	return nil
}

// RecordAttemptEvent is the shared lineage write entry point.
// It validates required fields before writing and returns contextual errors so
// callers can decide whether lineage failure is fatal or best-effort.
func RecordAttemptEvent(repoRoot string, event AttemptEvent) error {
	if event.RunID == "" {
		return fmt.Errorf("RecordAttemptEvent: runID is required")
	}
	if event.NodeID == "" {
		return fmt.Errorf("RecordAttemptEvent: runID=%s nodeID is required", event.RunID)
	}
	if event.Scope == "" {
		return fmt.Errorf("RecordAttemptEvent: runID=%s nodeID=%s scope is required", event.RunID, event.NodeID)
	}
	if err := AppendAttemptEvent(repoRoot, event); err != nil {
		return fmt.Errorf("RecordAttemptEvent: runID=%s nodeID=%s scope=%s write attempts.jsonl at %s: %w",
			event.RunID, event.NodeID, event.Scope, attemptNodePath(repoRoot, event.RunID, event.NodeID), err)
	}
	return nil
}

func ReadAttemptEvents(repoRoot, runID, nodeID string) ([]AttemptEvent, error) {
	if err := validatePathID(runID, "run_id"); err != nil {
		return nil, fmt.Errorf("read attempt events: %w", err)
	}
	if err := validatePathID(nodeID, "node_id"); err != nil {
		return nil, fmt.Errorf("read attempt events: %w", err)
	}
	lock := attemptLineageLock(repoRoot, runID)
	lock.Lock()
	defer lock.Unlock()

	path := attemptNodePath(repoRoot, runID, nodeID)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []AttemptEvent{}, nil
		}
		return nil, fmt.Errorf("read attempt events from %s: %w", path, err)
	}
	defer file.Close()

	var events []AttemptEvent
	reader := bufio.NewReader(file)
	for lineNo := 1; ; lineNo++ {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil && len(lineBytes) == 0 {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("read attempt event %s:%d: %w", path, lineNo, err)
		}
		line := strings.TrimSpace(string(lineBytes))
		if line == "" {
			if err == io.EOF {
				break
			}
			continue
		}
		var event AttemptEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("parse attempt event %s:%d: %w", path, lineNo, err)
		}
		events = append(events, event)
		if err == io.EOF {
			break
		}
	}
	return events, nil
}

func NewFileLineageStore(repoRoot string) AttemptLineageStore {
	return &fileLineageStore{repoRoot: repoRoot}
}

type fileLineageStore struct {
	repoRoot string
}

func (s *fileLineageStore) Append(event AttemptEvent) error {
	return AppendAttemptEvent(s.repoRoot, event)
}

func (s *fileLineageStore) List(filter AttemptFilter) ([]AttemptEvent, error) {
	events, err := ReadAttemptEvents(s.repoRoot, filter.RunID, filter.NodeID)
	if err != nil {
		return nil, err
	}
	filtered := events[:0]
	for _, event := range events {
		if filter.SubtaskID != "" && event.SubtaskID != filter.SubtaskID {
			continue
		}
		if filter.Scope != "" && event.Scope != filter.Scope {
			continue
		}
		if event.Attempt < filter.FromAttempt {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered, nil
}

func safeScope(scope string) string {
	replacer := strings.NewReplacer(":", "_", "/", "_", "\\", "_", " ", "_")
	return replacer.Replace(scope)
}

func validatePathID(name, label string) error {
	if name == "" {
		return fmt.Errorf("%s is required", label)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("%s %q contains disallowed path traversal", label, name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("%s %q contains disallowed character %q", label, name, r)
		}
	}
	return nil
}

func attemptLineageLock(repoRoot, runID string) *sync.Mutex {
	key := repoRoot + "\x00" + runID
	value, _ := attemptLineageLocks.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func attemptNodePath(repoRoot, runID, nodeID string) string {
	return filepath.Join(repoRoot, ".workflow", "runs", runID, nodeID, "attempts.jsonl")
}

func attemptIndexPath(repoRoot, runID string) string {
	return filepath.Join(repoRoot, ".workflow", "runs", runID, "attempts.index.jsonl")
}

func attemptScopePath(repoRoot, runID, nodeID, scope string) string {
	return filepath.Join(repoRoot, ".workflow", "runs", runID, nodeID, "attempts", safeScope(scope)+".jsonl")
}

func appendJSONL(path string, line []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open append: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

type appendState struct {
	path   string
	exists bool
	size   int64
}

func captureAppendStates(paths []string) ([]appendState, error) {
	states := make([]appendState, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				states = append(states, appendState{path: path})
				continue
			}
			return nil, fmt.Errorf("stat attempt event path %s: %w", path, err)
		}
		states = append(states, appendState{
			path:   path,
			exists: true,
			size:   info.Size(),
		})
	}
	return states, nil
}

func rollbackAppends(states []appendState) {
	for _, state := range states {
		if !state.exists {
			if err := os.Remove(state.path); err != nil {
				log.Printf("rollback: remove %s: %v", state.path, err)
			}
			continue
		}
		if err := os.Truncate(state.path, state.size); err != nil {
			log.Printf("rollback: truncate %s to %d: %v", state.path, state.size, err)
		}
	}
}

// writeAttemptEvent is the shared lineage write helper used by all node writeXxxAttempt functions.
// It constructs a minimal AttemptEvent and writes it via RecordAttemptEvent.
// runID empty → skip silently (best-effort).
func writeAttemptEvent(repoRoot, runID, nodeID string, nodeKind NodeKind, scope, role, sessionID string, attempt, parentAttempt int, trigger string, failureClass FailureClass, failureReason, rejectionReason string, outcome *AttemptOutcome, promptDelta *AttemptPromptDelta, promptPath, outputPath, exitPath string) error {
	if runID == "" {
		return nil
	}
	now := time.Now().UTC()
	event := AttemptEvent{
		RunID:      runID,
		NodeID:     nodeID,
		NodeKind:   nodeKind,
		Scope:      scope,
		SessionID:  sessionID,
		Role:       role,
		Attempt:    attempt,
		Trigger:    trigger,
		StartedAt:  now,
		FinishedAt: now,
	}
	if parentAttempt >= 0 {
		event.ParentAttempt = &parentAttempt
	}
	if failureClass != "" {
		event.FailureClass = failureClass
		event.FailureReason = string(failureClass)
	}
	if outcome != nil {
		event.Outcome = outcome
	}
	if rejectionReason != "" {
		event.RejectionReason = rejectionReason
	}
	if promptDelta != nil {
		event.PromptDelta = promptDelta
	}
	if promptPath != "" {
		event.PromptPath = promptPath
	}
	if outputPath != "" {
		event.OutputPath = outputPath
	}
	if exitPath != "" {
		event.ExitPath = exitPath
	}
	return RecordAttemptEvent(repoRoot, event)
}
