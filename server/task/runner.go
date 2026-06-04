package task

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RunSpec describes how to launch a child process for a task. It is built from
// the agent's config — nothing here is hardcoded.
type RunSpec struct {
	Command       string   // executable, e.g. "claude"
	Args          []string // args; {{model}} is substituted
	Prompt        string   // streamed to the child's stdin
	Model         string
	ThinkingLevel string
	Env           []string // extra env vars appended to os.Environ()
}

// Runner executes Claude Code (or any configured CLI) via exec.Command, wiring
// stdin/stdout/stderr through goroutine pipes — the Multica-style model. It
// never uses a terminal multiplexer.
type Runner struct {
	// WaitDelay is how long the process gets after SIGTERM before the runtime
	// escalates to SIGKILL.
	WaitDelay time.Duration
	// MaxLineBytes bounds a single scanned line (defends against huge JSON
	// frames from --output-format stream-json).
	MaxLineBytes int
}

func NewRunner(waitDelay time.Duration) *Runner {
	if waitDelay <= 0 {
		waitDelay = 10 * time.Second
	}
	return &Runner{WaitDelay: waitDelay, MaxLineBytes: 1 << 20}
}

// Run launches the process and blocks until it exits. Every output line is
// forwarded to onChunk (which fans out to the WebSocket) and accumulated into
// the returned result. Cancelling ctx sends SIGTERM, then SIGKILL after
// WaitDelay.
func (r *Runner) Run(ctx context.Context, spec RunSpec, onChunk func(stream, line string)) (string, error) {
	if spec.Command == "" {
		return "", fmt.Errorf("empty command")
	}
	args := substitute(spec.Args, spec)

	cmd := exec.CommandContext(ctx, spec.Command, args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	// Graceful cancellation: SIGTERM first, kill only after WaitDelay.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = r.WaitDelay

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start %s: %w", spec.Command, err)
	}

	// Feed the prompt then close stdin (EOF) so the CLI knows input is done.
	go func() {
		defer stdin.Close()
		_, _ = io.WriteString(stdin, spec.Prompt)
	}()

	var (
		mu  sync.Mutex
		out strings.Builder
		wg  sync.WaitGroup
	)
	drain := func(stream string, rc io.Reader, capture bool) {
		defer wg.Done()
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 64*1024), r.MaxLineBytes)
		for sc.Scan() {
			line := sc.Text()
			if capture {
				mu.Lock()
				out.WriteString(line)
				out.WriteByte('\n')
				mu.Unlock()
			}
			if onChunk != nil {
				onChunk(stream, line)
			}
		}
	}
	wg.Add(2)
	go drain("stdout", stdout, true)
	go drain("stderr", stderr, false)
	wg.Wait() // both pipes drained -> child has closed them

	waitErr := cmd.Wait()

	mu.Lock()
	result := out.String()
	mu.Unlock()

	if waitErr != nil {
		// Distinguish cancellation from a genuine failure.
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fmt.Errorf("process exited: %w", waitErr)
	}
	return result, nil
}

// substitute expands {{model}} / {{thinking}} placeholders in args.
func substitute(args []string, spec RunSpec) []string {
	rep := strings.NewReplacer(
		"{{model}}", spec.Model,
		"{{thinking}}", spec.ThinkingLevel,
	)
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = rep.Replace(a)
	}
	return out
}
