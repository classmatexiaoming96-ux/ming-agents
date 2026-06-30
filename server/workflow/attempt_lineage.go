package workflow

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func AppendAttemptEvent(repoRoot string, event AttemptEvent) error {
	if strings.TrimSpace(event.RunID) == "" {
		return fmt.Errorf("append attempt event: run_id is required")
	}
	if strings.TrimSpace(event.NodeID) == "" {
		return fmt.Errorf("append attempt event: node_id is required")
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal attempt event: %w", err)
	}
	line := append(data, '\n')

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

func ReadAttemptEvents(repoRoot, runID, nodeID string) ([]AttemptEvent, error) {
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
	scanner := bufio.NewScanner(file)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event AttemptEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("parse attempt event %s:%d: %w", path, lineNo, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan attempt events from %s: %w", path, err)
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
			_ = os.Remove(state.path)
			continue
		}
		_ = os.Truncate(state.path, state.size)
	}
}
