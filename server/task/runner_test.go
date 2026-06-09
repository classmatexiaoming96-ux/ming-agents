package task

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// The runner shells out to real, harmless commands (cat / sh) rather than
// monkeypatching exec.Command: this exercises the genuine pipe-wiring, stdin
// streaming, scanning and signal-on-cancel paths end-to-end, while never going
// near Claude Code. Every command used (cat, sh, true) is POSIX-standard.

func TestSubstitute(t *testing.T) {
	args := []string{"-p", "--model", "{{model}}", "--think", "{{thinking}}", "literal"}
	got := substitute(args, RunSpec{Model: "claude-opus-4-8", ThinkingLevel: "high"})
	want := []string{"-p", "--model", "claude-opus-4-8", "--think", "high", "literal"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Original slice must not be mutated.
	if args[2] != "{{model}}" {
		t.Errorf("substitute mutated input slice: %v", args)
	}
}

func TestRunEmptyCommandErrors(t *testing.T) {
	r := NewRunner(time.Second)
	if _, err := r.Run(context.Background(), RunSpec{Command: ""}, nil); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestRunStreamsPromptToStdin(t *testing.T) {
	// `cat` echoes whatever it reads on stdin to stdout, so the prompt round-trips
	// back through the capture path — proving stdin injection works.
	r := NewRunner(time.Second)
	prompt := "first line\nsecond line\n"
	var mu sync.Mutex
	var chunks []string
	result, err := r.Run(context.Background(), RunSpec{Command: "cat", Prompt: prompt},
		func(stream, line string) {
			if stream == "stdout" {
				mu.Lock()
				chunks = append(chunks, line)
				mu.Unlock()
			}
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result, "first line") || !strings.Contains(result, "second line") {
		t.Errorf("result missing prompt content: %q", result)
	}
	if len(chunks) != 2 || chunks[0] != "first line" || chunks[1] != "second line" {
		t.Errorf("onChunk stdout = %v, want [first line, second line]", chunks)
	}
}

func TestRunCapturesStdoutNotStderr(t *testing.T) {
	// stdout is accumulated into the result; stderr is forwarded to onChunk but
	// NOT part of the returned result. onChunk is called from both drain
	// goroutines concurrently, so the callback guards its own state with a mutex.
	r := NewRunner(time.Second)
	var mu sync.Mutex
	byStream := map[string][]string{}
	result, err := r.Run(context.Background(),
		RunSpec{Command: "sh", Args: []string{"-c", "echo OUTLINE; echo ERRLINE 1>&2"}},
		func(stream, line string) {
			mu.Lock()
			byStream[stream] = append(byStream[stream], line)
			mu.Unlock()
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result, "OUTLINE") {
		t.Errorf("result missing stdout: %q", result)
	}
	if strings.Contains(result, "ERRLINE") {
		t.Errorf("result must not capture stderr: %q", result)
	}
	if len(byStream["stdout"]) != 1 || byStream["stdout"][0] != "OUTLINE" {
		t.Errorf("stdout chunks = %v", byStream["stdout"])
	}
	if len(byStream["stderr"]) != 1 || byStream["stderr"][0] != "ERRLINE" {
		t.Errorf("stderr chunks = %v (stderr must still stream to onChunk)", byStream["stderr"])
	}
}

func TestRunCancellationSignalsChild(t *testing.T) {
	// A long sleep is interrupted by context cancellation: cmd.Cancel sends
	// SIGTERM, the child dies, and Run returns ctx.Err() promptly — well before
	// the 30s sleep would elapse.
	r := NewRunner(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()

	// Run `sleep` directly (no intervening shell) so the SIGTERM from cmd.Cancel
	// lands on the actual sleeping process rather than a parent shell that forked
	// it — otherwise the grandchild would hold the stdout pipe open and stall the
	// drain.
	start := time.Now()
	_, err := r.Run(ctx, RunSpec{Command: "sleep", Args: []string{"30"}}, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if ctx.Err() == nil {
		t.Errorf("ctx not canceled; err = %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("cancellation took %v; signal was not delivered promptly", elapsed)
	}
}

func TestRunNonZeroExitIsError(t *testing.T) {
	r := NewRunner(time.Second)
	ctx := context.Background()
	_, err := r.Run(ctx, RunSpec{Command: "sh", Args: []string{"-c", "exit 7"}}, nil)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if ctx.Err() != nil {
		t.Errorf("ctx should not be canceled on a plain failure: %v", ctx.Err())
	}
	if !strings.Contains(err.Error(), "process exited") {
		t.Errorf("err = %v, want it to wrap 'process exited'", err)
	}
}

func TestNewRunnerDefaults(t *testing.T) {
	r := NewRunner(0)
	if r.WaitDelay != 10*time.Second {
		t.Errorf("default WaitDelay = %v, want 10s", r.WaitDelay)
	}
	if r.MaxLineBytes != 1<<20 {
		t.Errorf("default MaxLineBytes = %d, want %d", r.MaxLineBytes, 1<<20)
	}
}
