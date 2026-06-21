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

func TestClaudeCodeSessionAutoRepliesToTrustPrompt(t *testing.T) {
	workDir := t.TempDir()
	replyPath := filepath.Join(workDir, "trust-reply.txt")
	cmd := writeTestCommand(t, `#!/bin/sh
printf 'Claude Code\n'
printf 'Do you trust this workspace? (y/n) '
IFS= read -r reply
printf '%s' "$reply" > trust-reply.txt
printf '\nHow can I help?\n'
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf 'accepted\n%s\n' "$marker"
      ;;
  esac
done
`)

	session := startTestClaudeSession(t, cmd, workDir)
	defer session.Close()

	reply, err := os.ReadFile(replyPath)
	if err != nil {
		t.Fatalf("read trust reply: %v", err)
	}
	if string(reply) != "y" {
		t.Fatalf("trust reply = %q, want y", string(reply))
	}
}

func TestClaudeCodeSessionBracketedPasteSendsMultilinePrompt(t *testing.T) {
	workDir := t.TempDir()
	capturePath := filepath.Join(workDir, "capture.txt")
	cmd := writeTestCommand(t, `#!/bin/sh
printf 'Claude Code ready\n'
buf=''
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
      printf 'done\n%s\n' "$marker"
      ;;
  esac
done
`)

	session := startTestClaudeSession(t, cmd, workDir)
	defer session.Close()

	if _, err := session.SendPrompt(testCtx(t, time.Second), "line one\nline two"); err != nil {
		t.Fatalf("SendPrompt() error = %v", err)
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if !strings.Contains(string(captured), "line one\nline two\n") {
		t.Fatalf("captured input = %q, want multiline prompt", string(captured))
	}
}

func TestClaudeCodeSessionSentinelCompletionExtractsOnlyResponse(t *testing.T) {
	workDir := t.TempDir()
	cmd := writeTestCommand(t, `#!/bin/sh
printf 'How can I help?\n'
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf 'first line\n'
      printf 'second line\n'
      printf '%s\n' "$marker"
      printf 'noise after sentinel\n'
      ;;
  esac
done
`)

	session := startTestClaudeSession(t, cmd, workDir)
	defer session.Close()

	output, err := session.SendPrompt(testCtx(t, time.Second), "answer me")
	if err != nil {
		t.Fatalf("SendPrompt() error = %v", err)
	}
	if output != "first line\nsecond line" {
		t.Fatalf("output = %q, want response before sentinel only", output)
	}
}

func TestClaudeCodeSessionSendPromptHonorsContextWhenSendLocked(t *testing.T) {
	session := &ClaudeCodeSession{}
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

func TestClaudeCodeSessionWaitForUsesNormalizedResponseSnapshot(t *testing.T) {
	workDir := t.TempDir()
	cmd := writeTestCommand(t, `#!/bin/sh
printf 'Claude Code ready\n'
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf '\033[31mred response\033[0m\n'
      printf '%s\n' "$marker"
      ;;
  esac
done
`)

	session := startTestClaudeSession(t, cmd, workDir)
	defer session.Close()

	output, err := session.SendPrompt(testCtx(t, time.Second), "answer me")
	if err != nil {
		t.Fatalf("SendPrompt() error = %v", err)
	}
	if output != "red response" {
		t.Fatalf("output = %q, want normalized response before sentinel", output)
	}
}

func TestClaudeCodeAdapterManagersAreKeyedByCommand(t *testing.T) {
	first := ClaudeCodeAdapter{Command: "/tmp/first-claude", Timeout: time.Second}.manager("/tmp/first-claude")
	second := ClaudeCodeAdapter{Command: "/tmp/second-claude", Timeout: time.Second}.manager("/tmp/second-claude")

	if first == second {
		t.Fatal("manager() returned same manager for different commands")
	}
	if first.config.Command != "/tmp/first-claude" {
		t.Fatalf("first manager command = %q", first.config.Command)
	}
	if second.config.Command != "/tmp/second-claude" {
		t.Fatalf("second manager command = %q", second.config.Command)
	}
}

func TestClaudeCodeSessionManagerSharesConcurrentStartupForWorkDir(t *testing.T) {
	workDir := t.TempDir()
	startsPath := filepath.Join(workDir, "starts.txt")
	cmd := writeTestCommand(t, `#!/bin/sh
printf 'start\n' >> starts.txt
sleep 0.05
printf 'Claude Code ready\n'
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf 'ok\n%s\n' "$marker"
      ;;
  esac
done
`)
	manager := NewClaudeCodeSessionManager(ClaudeCodeConfig{
		Command:        cmd,
		StartupTimeout: time.Second,
		ReadyTimeout:   time.Second,
	})

	var wg sync.WaitGroup
	sessions := make([]*ClaudeCodeSession, 2)
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

func TestClaudeCodeAdapterInvokeTimeoutReturnsStructuredError(t *testing.T) {
	cmd := writeTestCommand(t, `#!/bin/sh
printf 'Claude Code ready\n'
while IFS= read -r line; do
  :
done
`)

	result, err := (ClaudeCodeAdapter{
		Command: cmd,
		Timeout: 20 * time.Millisecond,
	}).Invoke(AgentRequest{Prompt: "never completes"})
	if err == nil {
		t.Fatal("Invoke() error = nil, want timeout error")
	}
	if result == nil {
		t.Fatal("Invoke() result = nil, want structured timeout result")
	}
	if got := decodeExitCode(t, result.RawJSON); got != -1 {
		t.Fatalf("exit_code = %d, want -1", got)
	}
	if !strings.Contains(strings.ToLower(result.Error), "timed out") {
		t.Fatalf("Error = %q, want timeout message", result.Error)
	}
}

func TestClaudeCodeAdapterAuthRequiredReturnsActionableError(t *testing.T) {
	cmd := writeTestCommand(t, `#!/bin/sh
printf 'Authentication required. Please log in.\n'
sleep 1
`)

	result, err := (ClaudeCodeAdapter{
		Command: cmd,
		Timeout: time.Second,
	}).Invoke(AgentRequest{Prompt: "hello"})
	if err == nil {
		t.Fatal("Invoke() error = nil, want auth error")
	}
	if result == nil {
		t.Fatal("Invoke() result = nil, want structured auth result")
	}
	if !strings.Contains(result.Error, "claude --setup-token") {
		t.Fatalf("Error = %q, want setup-token guidance", result.Error)
	}
}

func TestClaudeCodeAdapterNeverCallsForbiddenModes(t *testing.T) {
	workDir := t.TempDir()
	argsPath := filepath.Join(workDir, "args.txt")
	cmd := writeTestCommand(t, `#!/bin/sh
printf '%s\n' "$@" > args.txt
printf 'Claude Code ready\n'
while IFS= read -r line; do
  case "$line" in
    *MING_AGENTS_DONE:*)
      marker=$(printf '%s' "$line" | tr -d '"' | sed 's/ + //g')
      printf 'ok\n%s\n' "$marker"
      ;;
  esac
done
`)

	result, err := (ClaudeCodeAdapter{
		Command: cmd,
		WorkDir: workDir,
		Timeout: time.Second,
	}).Invoke(AgentRequest{Prompt: "inspect args"})
	if err != nil {
		t.Fatalf("Invoke() error = %v; result=%+v", err, result)
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	for _, forbidden := range []string{"-" + "p", "--" + "print", "--" + "acp", "--" + "stdio"} {
		if strings.Contains(string(args), forbidden) {
			t.Fatalf("args contain forbidden mode %q: %q", forbidden, string(args))
		}
	}
}

func startTestClaudeSession(t *testing.T, command, workDir string) *ClaudeCodeSession {
	t.Helper()
	manager := NewClaudeCodeSessionManager(ClaudeCodeConfig{
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

func testCtx(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}
