package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCodexSessionRespondsToTerminalQueriesAndCompletesWithSentinel(t *testing.T) {
	workDir := t.TempDir()
	repliesPath := filepath.Join(workDir, "terminal-replies.txt")
	capturePath := filepath.Join(workDir, "capture.txt")
	cmd := writeTestCommand(t, `#!/bin/sh
printf '\033[6n\033[c\033]10;?\033\\'
dd bs=1 count=42 of=terminal-replies.txt 2>/dev/null
printf 'OpenAI Codex\n'
printf '› '
while IFS= read -r line; do
  case "$line" in
    *'[200~'*)
      line=${line#*'[200~'}
      ;;
  esac
  case "$line" in
    *'[201~'*)
      line=${line%%'[201~'*}
      ;;
  esac
  printf '%s\n' "$line" >> capture.txt
  case "$line" in
    *MING_AGENTS_DONE:*)
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf 'answer from codex\n'
      printf '%s\n' "$marker"
      ;;
  esac
done
`)

	session := startTestCodexSession(t, cmd, workDir)
	defer session.Close()

	output, err := session.SendPrompt(testCtx(t, time.Second), "line one\nline two")
	if err != nil {
		t.Fatalf("SendPrompt() error = %v", err)
	}
	if output != "answer from codex" {
		t.Fatalf("output = %q, want answer before sentinel", output)
	}

	replies, err := os.ReadFile(repliesPath)
	if err != nil {
		t.Fatalf("read terminal replies: %v", err)
	}
	for _, want := range []string{"\x1b[1;1R", "\x1b[?1;2c", "\x1b]10;rgb:ffff/ffff/ffff\x1b\\"} {
		if !strings.Contains(string(replies), want) {
			t.Fatalf("terminal replies = %q, want %q", string(replies), want)
		}
	}

	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read captured prompt: %v", err)
	}
	if !strings.Contains(string(captured), "line one\nline two\n") {
		t.Fatalf("captured input = %q, want multiline prompt", string(captured))
	}
}

func TestCodexSessionManagerReusesSessionForWorkDir(t *testing.T) {
	workDir := t.TempDir()
	startsPath := filepath.Join(workDir, "starts.txt")
	cmd := writeTestCommand(t, `#!/bin/sh
printf 'start\n' >> starts.txt
sleep 0.05
printf 'OpenAI Codex\n› '
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf 'ok\n%s\n' "$marker"
      ;;
  esac
done
`)
	manager := NewCodexSessionManager(CodexConfig{
		Command:        cmd,
		StartupTimeout: time.Second,
		ReadyTimeout:   time.Second,
	})

	var wg sync.WaitGroup
	sessions := make([]*CodexSession, 2)
	errs := make([]error, 2)
	for i := range sessions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessions[i], errs[i] = manager.GetOrStart(testCtx(t, time.Second), workDir)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("GetOrStart(%d) error = %v", i, err)
		}
	}
	t.Cleanup(func() { sessions[0].Close() })
	if sessions[0] != sessions[1] {
		t.Fatal("concurrent GetOrStart returned different sessions for same workDir")
	}
	starts, err := os.ReadFile(startsPath)
	if err != nil {
		t.Fatalf("read starts: %v", err)
	}
	if got := strings.Count(string(starts), "start"); got != 1 {
		t.Fatalf("session starts = %d, want 1", got)
	}
}

func TestCodexAdapterPTYSessionPreservesShortTermMemory(t *testing.T) {
	workDir := t.TempDir()
	cmd := writeTestCommand(t, `#!/bin/sh
count=0
printf 'OpenAI Codex\n› '
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      count=$((count + 1))
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf 'turn=%s\n' "$count"
      printf '%s\n' "$marker"
      ;;
  esac
done
`)
	adapter := CodexAdapter{
		Command: cmd,
		WorkDir: workDir,
		Timeout: time.Second,
	}

	first, err := adapter.Invoke(AgentRequest{Prompt: "first"})
	if err != nil {
		t.Fatalf("first Invoke() error = %v", err)
	}
	second, err := adapter.Invoke(AgentRequest{Prompt: "second"})
	if err != nil {
		t.Fatalf("second Invoke() error = %v", err)
	}

	if first.Output != "turn=1" {
		t.Fatalf("first output = %q, want turn=1", first.Output)
	}
	if second.Output != "turn=2" {
		t.Fatalf("second output = %q, want turn=2 from reused session", second.Output)
	}
}

func TestCodexAdapterFallsBackToExecWhenPTYStartupFails(t *testing.T) {
	workDir := t.TempDir()
	cmd := writeTestCommand(t, `#!/bin/sh
if [ "$1" = "exec" ]; then
  printf 'fallback prompt=%s\n' "$2"
  exit 0
fi
printf 'interactive failed\n' >&2
exit 9
`)

	result, err := (CodexAdapter{
		Command: cmd,
		WorkDir: workDir,
		Timeout: time.Second,
	}).Invoke(AgentRequest{Prompt: "repair"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Output != "fallback prompt=repair\n" {
		t.Fatalf("Output = %q, want fallback exec output", result.Output)
	}
}

func TestCodexSessionSendPromptHonorsContextWhenSendLocked(t *testing.T) {
	session := &CodexSession{}
	session.sendMu.Lock()
	defer session.sendMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := session.SendPrompt(ctx, "should not wait forever")
	if err == nil {
		t.Fatal("SendPrompt() error = nil, want context error while send lock is held")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("SendPrompt() waited %s, want it to return promptly when context is done", time.Since(start))
	}
}

func startTestCodexSession(t *testing.T, command, workDir string) *CodexSession {
	t.Helper()
	manager := NewCodexSessionManager(CodexConfig{
		Command:        command,
		InvokeTimeout:  time.Second,
		StartupTimeout: time.Second,
		ReadyTimeout:   time.Second,
	})
	session, err := manager.StartSession(testCtx(t, time.Second), workDir)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	return session
}
