package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexAdapterInvokeRunsCodexExecInWorkDir(t *testing.T) {
	workDir := t.TempDir()
	cmd := writeTestCommand(t, `#!/bin/sh
if [ "$1" != "exec" ]; then
  echo "unexpected arg1: $1" >&2
  exit 11
fi
if [ "$2" != "repair the tests" ]; then
  echo "unexpected prompt: $2" >&2
  exit 12
fi
printf 'cwd=%s\nprompt=%s\n' "$(pwd)" "$2"
`)

	result, err := (CodexAdapter{
		Command: cmd,
		WorkDir: workDir,
		Timeout: time.Second,
	}).Invoke(AgentRequest{Prompt: "repair the tests"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	want := "cwd=" + workDir + "\nprompt=repair the tests\n"
	if result.Output != want {
		t.Fatalf("Output = %q, want %q", result.Output, want)
	}
	if got := decodeExitCode(t, result.RawJSON); got != 0 {
		t.Fatalf("exit_code = %d, want 0", got)
	}
}

func TestCodexAdapterInvokeUsesPerInvocationWorkDir(t *testing.T) {
	staticDir := t.TempDir()
	dynamicDir := t.TempDir()
	cmd := writeTestCommand(t, `#!/bin/sh
printf 'cwd=%s\n' "$(pwd)"
`)

	result, err := (CodexAdapter{
		Command: cmd,
		WorkDir: staticDir,
		Timeout: time.Second,
	}).Invoke(AgentRequest{Prompt: "repair the tests"}, ExecutionContext{WorkDir: dynamicDir})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	if want := "cwd=" + dynamicDir + "\n"; result.Output != want {
		t.Fatalf("Output = %q, want %q", result.Output, want)
	}
}

func TestCodexAdapterInvokeReturnsExitCodeAndOutputOnFailure(t *testing.T) {
	cmd := writeTestCommand(t, `#!/bin/sh
echo "partial output"
echo "failure details" >&2
exit 7
`)

	result, err := (CodexAdapter{
		Command: cmd,
		Timeout: time.Second,
	}).Invoke(AgentRequest{Prompt: "fail"})
	if err == nil {
		t.Fatal("Invoke() error = nil, want non-nil")
	}
	if result == nil {
		t.Fatal("Invoke() result = nil, want structured result")
	}
	if result.Output != "partial output\n" {
		t.Fatalf("Output = %q, want partial stdout", result.Output)
	}
	if got := decodeExitCode(t, result.RawJSON); got != 7 {
		t.Fatalf("exit_code = %d, want 7", got)
	}
	if result.Error == "" {
		t.Fatal("Error is empty, want stderr or process error")
	}
}

func TestCodexAdapterInvokeTimesOut(t *testing.T) {
	cmd := writeTestCommand(t, `#!/bin/sh
sleep 1
echo "too late"
`)

	result, err := (CodexAdapter{
		Command: cmd,
		Timeout: 10 * time.Millisecond,
	}).Invoke(AgentRequest{Prompt: "slow"})
	if err == nil {
		t.Fatal("Invoke() error = nil, want timeout error")
	}
	if result == nil {
		t.Fatal("Invoke() result = nil, want structured timeout result")
	}
	if got := decodeExitCode(t, result.RawJSON); got != -1 {
		t.Fatalf("exit_code = %d, want -1 for timeout", got)
	}
	if result.Error == "" {
		t.Fatal("Error is empty, want timeout description")
	}
}

func TestNewRegistryIncludesCLIAdapters(t *testing.T) {
	registry := NewRegistry()
	for _, key := range []string{"codex", "claude-code"} {
		if _, err := registry.Get(key); err != nil {
			t.Fatalf("Get(%q) error = %v", key, err)
		}
	}
}

func writeTestCommand(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write command: %v", err)
	}
	return path
}

func decodeExitCode(t *testing.T, raw json.RawMessage) int {
	t.Helper()
	var payload struct {
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal RawJSON %q: %v", string(raw), err)
	}
	return payload.ExitCode
}
