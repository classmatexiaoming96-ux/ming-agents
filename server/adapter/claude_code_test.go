package adapter

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeCodeAdapterInvokeRunsACPStdioWithPromptOnStdin(t *testing.T) {
	workDir := t.TempDir()
	inputPath := filepath.Join(workDir, "stdin.txt")
	cmd := writeTestCommand(t, `#!/bin/sh
if [ "$1" != "--acp" ]; then
  echo "unexpected arg1: $1" >&2
  exit 21
fi
if [ "$2" != "--stdio" ]; then
  echo "unexpected arg2: $2" >&2
  exit 22
fi
cat > stdin.txt
printf 'cwd=%s\ninput=%s\n' "$(pwd)" "$(cat stdin.txt)"
`)

	result, err := (ClaudeCodeAdapter{
		Command: cmd,
		WorkDir: workDir,
		Timeout: time.Second,
	}).Invoke(AgentRequest{Prompt: "repair via stdin"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	stdinBytes, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read stdin capture: %v", err)
	}
	if string(stdinBytes) != "repair via stdin" {
		t.Fatalf("stdin = %q, want prompt", string(stdinBytes))
	}

	want := "cwd=" + workDir + "\ninput=repair via stdin\n"
	if result.Output != want {
		t.Fatalf("Output = %q, want %q", result.Output, want)
	}
	if got := decodeExitCode(t, result.RawJSON); got != 0 {
		t.Fatalf("exit_code = %d, want 0", got)
	}
}

func TestClaudeCodeAdapterInvokeReturnsExitCodeAndOutputOnFailure(t *testing.T) {
	cmd := writeTestCommand(t, `#!/bin/sh
cat >/dev/null
echo "claude output"
echo "claude failure" >&2
exit 9
`)

	result, err := (ClaudeCodeAdapter{
		Command: cmd,
		Timeout: time.Second,
	}).Invoke(AgentRequest{Prompt: "fail"})
	if err == nil {
		t.Fatal("Invoke() error = nil, want non-nil")
	}
	if result == nil {
		t.Fatal("Invoke() result = nil, want structured result")
	}
	if result.Output != "claude output\n" {
		t.Fatalf("Output = %q, want stdout", result.Output)
	}
	if got := decodeExitCode(t, result.RawJSON); got != 9 {
		t.Fatalf("exit_code = %d, want 9", got)
	}
	if result.Error == "" {
		t.Fatal("Error is empty, want stderr or process error")
	}
}

func TestClaudeCodeAdapterInvokeTimesOut(t *testing.T) {
	cmd := writeTestCommand(t, `#!/bin/sh
cat >/dev/null
sleep 1
`)

	result, err := (ClaudeCodeAdapter{
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
