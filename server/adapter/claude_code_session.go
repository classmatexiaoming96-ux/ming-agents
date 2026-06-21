package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	defaultClaudeStartupTimeout = 60 * time.Second
	defaultClaudeReadyTimeout   = 30 * time.Second
	claudeReadyDebounce         = 500 * time.Millisecond
	claudeShutdownTimeout       = 3 * time.Second
	claudeSendLockTimeout       = 5 * time.Second
	claudeSendLockPollInterval  = 10 * time.Millisecond
)

var (
	claudeTrustPattern = regexp.MustCompile(`(?i)(trust|trusted|security|folder|workspace|directory).*(y/n|yes|no|confirm)`)
	claudeYesNoPattern = regexp.MustCompile(`(?i)(\(y/n\)|\[y/n\]|yes/no|confirm.*:)`)
	claudeReadyPattern = regexp.MustCompile(`(?i)(how can i help|claude code|ready)`)
	claudeAuthPattern  = regexp.MustCompile(`(?i)(authentication required|not authenticated|login required|please log in|setup-token|api key)`)
)

type ClaudeCodeConfig struct {
	Command        string
	InvokeTimeout  time.Duration
	StartupTimeout time.Duration
	ReadyTimeout   time.Duration
}

type ClaudeCodeSession struct {
	id       string
	workDir  string
	cmd      *exec.Cmd
	pty      *os.File
	reader   *PTYReader
	closed   bool
	sendMu   sync.Mutex
	stateMu  sync.Mutex
	waitDone chan struct{}
	doneErr  error
}

type ClaudeCodeSessionManager struct {
	mu       sync.Mutex
	sessions map[string]*ClaudeCodeSession
	config   ClaudeCodeConfig
}

func NewClaudeCodeSessionManager(config ClaudeCodeConfig) *ClaudeCodeSessionManager {
	if config.Command == "" {
		config.Command = "claude"
	}
	if config.InvokeTimeout <= 0 {
		config.InvokeTimeout = defaultAgentTimeout
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = defaultClaudeStartupTimeout
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = defaultClaudeReadyTimeout
	}
	return &ClaudeCodeSessionManager{
		sessions: make(map[string]*ClaudeCodeSession),
		config:   config,
	}
}

func (m *ClaudeCodeSessionManager) GetOrStart(ctx context.Context, workDir string) (*ClaudeCodeSession, error) {
	key := workDir
	m.mu.Lock()
	if session := m.sessions[key]; session != nil && !session.isClosed() {
		m.mu.Unlock()
		return session, nil
	}
	oldSession := m.sessions[key]
	// Mark in-flight so other goroutines wait for the same session.
	m.sessions[key] = nil
	m.mu.Unlock()

	session, err := m.StartSession(ctx, workDir)
	if err != nil {
		// Remove in-flight marker so retry can succeed.
		m.mu.Lock()
		delete(m.sessions, key)
		m.mu.Unlock()
		return nil, err
	}

	m.mu.Lock()
	// Clean up any dead sessions before storing the new one.
	if oldSession != nil && oldSession != session {
		oldSession.Close()
	}
	m.sessions[key] = session
	m.mu.Unlock()
	return session, nil
}

func (m *ClaudeCodeSessionManager) StartSession(ctx context.Context, workDir string) (*ClaudeCodeSession, error) {
	startCtx, cancel := context.WithTimeout(ctx, m.config.StartupTimeout)

	cmd := exec.Command(m.config.Command)
	cmd.Dir = workDir
	tty, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start claude-code interactive session: %w", err)
	}
	_ = disablePTYEcho(tty)

	session := &ClaudeCodeSession{
		id:       uuid.NewString(),
		workDir:  workDir,
		cmd:      cmd,
		pty:      tty,
		reader:   NewPTYReader(tty),
		waitDone: make(chan struct{}),
	}
	go session.reader.ReadLoop()
	go func() {
		err := cmd.Wait()
		session.stateMu.Lock()
		session.closed = true
		session.doneErr = err
		session.stateMu.Unlock()
		close(session.waitDone)
		session.reader.Close()
	}()

	readyCtx, readyCancel := context.WithTimeout(startCtx, m.config.ReadyTimeout)
	defer readyCancel()

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	lastAutoReplyOffset := 0
	var readyAt time.Time
	for {
		select {
		case <-readyCtx.Done():
			output, _ := session.reader.Snapshot()
			session.Close()
			cancel()
			if claudeAuthPattern.MatchString(output) {
				return nil, claudeAuthError()
			}
			return nil, fmt.Errorf("claude-code session was not ready before timeout")
		case <-session.waitDone:
			output, _ := session.reader.Snapshot()
			session.Close()
			cancel()
			if claudeAuthPattern.MatchString(output) {
				return nil, claudeAuthError()
			}
			session.stateMu.Lock()
			err := session.doneErr
			session.stateMu.Unlock()
			if err != nil {
				return nil, fmt.Errorf("claude-code exited during startup: %w", err)
			}
			return nil, errors.New("claude-code exited during startup")
		case <-ticker.C:
			output, offset := session.reader.Snapshot()
			if claudeAuthPattern.MatchString(output) {
				session.Close()
				cancel()
				return nil, claudeAuthError()
			}
			if lastAutoReplyOffset < offset && shouldAutoReplyYes(output[lastAutoReplyOffset:]) {
				if _, err := session.pty.Write([]byte("y\r")); err != nil {
					session.Close()
					return nil, fmt.Errorf("reply to claude-code trust prompt: %w", err)
				}
				lastAutoReplyOffset = offset
				continue
			}
			if claudeReadyPattern.MatchString(output) {
				if readyAt.IsZero() {
					readyAt = time.Now()
				}
				if time.Since(readyAt) >= claudeReadyDebounce {
					cancel()
					return session, nil
				}
			}
		}
	}
}

func (s *ClaudeCodeSession) SendPrompt(ctx context.Context, prompt string) (string, error) {
	if err := s.lockSend(ctx); err != nil {
		return "", err
	}
	defer s.sendMu.Unlock()

	if s.isClosed() {
		return "", errors.New("claude-code session is closed")
	}

	promptPaste := "\x1b[200~" + prompt + "\x1b[201~\r"
	if _, err := s.pty.Write([]byte(promptPaste)); err != nil {
		return "", fmt.Errorf("send prompt to claude-code: %w", err)
	}

	_, since := s.reader.Snapshot()
	sentinel := "<<<MING_AGENTS_DONE:" + uuid.NewString() + ">>>"
	sentinelPrefix, sentinelSuffix := strings.TrimSuffix(sentinel, ">>>"), ">>>"
	sentinelPrompt := "\n\nWhen finished, print exactly these two quoted parts concatenated as one line on its own line and nothing after it:\n" +
		fmt.Sprintf("%q + %q\n", sentinelPrefix, sentinelSuffix)
	paste := "\x1b[200~" + sentinelPrompt + "\x1b[201~\r"
	if _, err := s.pty.Write([]byte(paste)); err != nil {
		return "", fmt.Errorf("send completion sentinel to claude-code: %w", err)
	}

	response, ok := s.reader.WaitFor(ctx, regexp.QuoteMeta(sentinel), since)
	if !ok {
		if ctx.Err() != nil {
			return "", fmt.Errorf("claude-code invocation timed out: %w", ctx.Err())
		}
		output, _ := s.reader.Snapshot()
		s.stateMu.Lock()
		doneErr := s.doneErr
		s.stateMu.Unlock()
		if doneErr != nil {
			return "", fmt.Errorf("claude-code session closed before completion sentinel: %w: %s", doneErr, strings.TrimSpace(output))
		}
		return "", fmt.Errorf("claude-code session closed before completion sentinel: %s", strings.TrimSpace(output))
	}

	output := strings.TrimSpace(response)
	output = stripPromptEcho(output, sentinelPrompt)
	return output, nil
}

func (s *ClaudeCodeSession) lockSend(ctx context.Context) error {
	if s.sendMu.TryLock() {
		return nil
	}
	timer := time.NewTimer(claudeSendLockTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(claudeSendLockPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("acquire claude-code send lock: %w", ctx.Err())
		case <-timer.C:
			return errors.New("acquire claude-code send lock: timed out")
		case <-ticker.C:
			if s.sendMu.TryLock() {
				return nil
			}
		}
	}
}

func (s *ClaudeCodeSession) Close() {
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return
	}
	s.closed = true
	s.stateMu.Unlock()

	if s.reader != nil {
		s.reader.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		if s.cmd.ProcessState != nil {
			return
		}
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-s.waitDone:
		case <-time.After(claudeShutdownTimeout):
			_ = s.cmd.Process.Kill()
			select {
			case <-s.waitDone:
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

func (s *ClaudeCodeSession) isClosed() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.closed
}

func claudeAuthError() error {
	return errors.New("Claude Code authentication required; run `claude --setup-token` and retry")
}

func shouldAutoReplyYes(output string) bool {
	return claudeTrustPattern.MatchString(output) || claudeYesNoPattern.MatchString(output)
}

func disablePTYEcho(tty *os.File) error {
	termios, err := unix.IoctlGetTermios(int(tty.Fd()), unix.TCGETS)
	if err != nil {
		return err
	}
	termios.Lflag &^= unix.ECHO
	return unix.IoctlSetTermios(int(tty.Fd()), unix.TCSETS, termios)
}

func stripPromptEcho(output, fullPrompt string) string {
	output = strings.TrimSpace(output)
	fullPrompt = strings.TrimSpace(fullPrompt)
	if strings.HasPrefix(output, fullPrompt) {
		return strings.TrimSpace(strings.TrimPrefix(output, fullPrompt))
	}
	return output
}
