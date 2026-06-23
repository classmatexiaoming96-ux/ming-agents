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
)

const (
	defaultCodexStartupTimeout = 60 * time.Second
	defaultCodexReadyTimeout   = 30 * time.Second
	codexReadyDebounce         = 250 * time.Millisecond
	codexShutdownTimeout       = 3 * time.Second
	codexSendLockTimeout       = 5 * time.Second
	codexSendLockPollInterval  = 10 * time.Millisecond
)

var (
	codexReadyPattern = regexp.MustCompile(`(?i)(openai codex|codex|ready)|(^|\n)\s*›\s*$`)
	codexAuthPattern  = regexp.MustCompile(`(?i)(authentication required|not authenticated|login required|please log in|api key)`)
)

type CodexConfig struct {
	Command        string
	InvokeTimeout  time.Duration
	StartupTimeout time.Duration
	ReadyTimeout   time.Duration
}

type CodexSession struct {
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

type CodexSessionManager struct {
	mu       sync.Mutex
	sessions map[string]*CodexSession
	starting map[string]chan struct{}
	config   CodexConfig
}

func NewCodexSessionManager(config CodexConfig) *CodexSessionManager {
	if config.Command == "" {
		config.Command = "codex"
	}
	if config.InvokeTimeout <= 0 {
		config.InvokeTimeout = defaultAgentTimeout
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = defaultCodexStartupTimeout
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = defaultCodexReadyTimeout
	}
	return &CodexSessionManager{
		sessions: make(map[string]*CodexSession),
		starting: make(map[string]chan struct{}),
		config:   config,
	}
}

func (m *CodexSessionManager) GetOrStart(ctx context.Context, workDir string) (*CodexSession, error) {
	key := workDir
	for {
		m.mu.Lock()
		if session := m.sessions[key]; session != nil && !session.isClosed() {
			m.mu.Unlock()
			return session, nil
		}
		if done := m.starting[key]; done != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("wait for codex session startup: %w", ctx.Err())
			case <-done:
				continue
			}
		}
		m.starting[key] = make(chan struct{})
		oldSession := m.sessions[key]
		m.mu.Unlock()

		session, err := m.StartSession(ctx, workDir)

		m.mu.Lock()
		done := m.starting[key]
		delete(m.starting, key)
		if err != nil {
			delete(m.sessions, key)
			close(done)
			m.mu.Unlock()
			return nil, err
		}
		if oldSession != nil && oldSession != session {
			oldSession.Close()
		}
		m.sessions[key] = session
		close(done)
		m.mu.Unlock()
		return session, nil
	}
}

func (m *CodexSessionManager) StartSession(ctx context.Context, workDir string) (*CodexSession, error) {
	startCtx, cancel := context.WithTimeout(ctx, m.config.StartupTimeout)

	cmd := exec.Command(m.config.Command)
	cmd.Dir = workDir
	tty, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start codex interactive session: %w", err)
	}
	_ = disablePTYEcho(tty)

	session := &CodexSession{
		id:       uuid.NewString(),
		workDir:  workDir,
		cmd:      cmd,
		pty:      tty,
		reader:   NewPTYReader(tty),
		waitDone: make(chan struct{}),
	}
	session.reader.SetRawHandler(newCodexTerminalResponder(tty))
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

	var readyAt time.Time
	for {
		select {
		case <-readyCtx.Done():
			output, _ := session.reader.Snapshot()
			session.Close()
			cancel()
			if codexAuthPattern.MatchString(output) {
				return nil, codexAuthError()
			}
			return nil, errors.New("codex session was not ready before timeout")
		case <-session.waitDone:
			output, _ := session.reader.Snapshot()
			session.Close()
			cancel()
			if codexAuthPattern.MatchString(output) {
				return nil, codexAuthError()
			}
			session.stateMu.Lock()
			err := session.doneErr
			session.stateMu.Unlock()
			if err != nil {
				return nil, fmt.Errorf("codex exited during startup: %w", err)
			}
			return nil, errors.New("codex exited during startup")
		case <-ticker.C:
			output, _ := session.reader.Snapshot()
			if codexAuthPattern.MatchString(output) {
				session.Close()
				cancel()
				return nil, codexAuthError()
			}
			if codexReadyPattern.MatchString(output) {
				if readyAt.IsZero() {
					readyAt = time.Now()
				}
				if time.Since(readyAt) >= codexReadyDebounce {
					cancel()
					return session, nil
				}
			}
		}
	}
}

func (s *CodexSession) SendPrompt(ctx context.Context, prompt string) (string, error) {
	if err := s.lockSend(ctx); err != nil {
		return "", err
	}
	defer s.sendMu.Unlock()

	if s.isClosed() {
		return "", errors.New("codex session is closed")
	}

	promptPaste := "\x1b[200~" + prompt + "\x1b[201~\r"
	if _, err := s.pty.Write([]byte(promptPaste)); err != nil {
		return "", fmt.Errorf("send prompt to codex: %w", err)
	}

	_, since := s.reader.Snapshot()
	sentinel := "<<<MING_AGENTS_DONE:" + uuid.NewString() + ">>>"
	sentinelPrefix, sentinelSuffix := strings.TrimSuffix(sentinel, ">>>"), ">>>"
	sentinelPrompt := "\n\nWhen finished, print exactly these two quoted parts concatenated as one line on its own line and nothing after it:\n" +
		fmt.Sprintf("%q + %q\n", sentinelPrefix, sentinelSuffix)
	paste := "\x1b[200~" + sentinelPrompt + "\x1b[201~\r"
	if _, err := s.pty.Write([]byte(paste)); err != nil {
		return "", fmt.Errorf("send completion sentinel to codex: %w", err)
	}

	response, ok := s.reader.WaitFor(ctx, regexp.QuoteMeta(sentinel), since)
	if !ok {
		if ctx.Err() != nil {
			return "", fmt.Errorf("codex invocation timed out: %w", ctx.Err())
		}
		output, _ := s.reader.Snapshot()
		s.stateMu.Lock()
		doneErr := s.doneErr
		s.stateMu.Unlock()
		if doneErr != nil {
			return "", fmt.Errorf("codex session closed before completion sentinel: %w: %s", doneErr, strings.TrimSpace(output))
		}
		return "", fmt.Errorf("codex session closed before completion sentinel: %s", strings.TrimSpace(output))
	}

	output := strings.TrimSpace(response)
	output = stripPromptEcho(output, sentinelPrompt)
	return output, nil
}

func (s *CodexSession) lockSend(ctx context.Context) error {
	if s.sendMu.TryLock() {
		return nil
	}
	timer := time.NewTimer(codexSendLockTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(codexSendLockPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("acquire codex send lock: %w", ctx.Err())
		case <-timer.C:
			return errors.New("acquire codex send lock: timed out")
		case <-ticker.C:
			if s.sendMu.TryLock() {
				return nil
			}
		}
	}
}

func (s *CodexSession) Close() {
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
		case <-time.After(codexShutdownTimeout):
			_ = s.cmd.Process.Kill()
			select {
			case <-s.waitDone:
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

func (s *CodexSession) isClosed() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.closed
}

func codexAuthError() error {
	return errors.New("Codex authentication required; run `codex login` and retry")
}

func newCodexTerminalResponder(tty *os.File) func([]byte) {
	var mu sync.Mutex
	var pending string
	return func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		pending += string(data)
		for {
			switch {
			case strings.Contains(pending, "\x1b[6n"):
				pending = strings.Replace(pending, "\x1b[6n", "", 1)
				_, _ = tty.Write([]byte("\x1b[1;1R"))
			case strings.Contains(pending, "\x1b[c"):
				pending = strings.Replace(pending, "\x1b[c", "", 1)
				_, _ = tty.Write([]byte("\x1b[?1;2c"))
			case strings.Contains(pending, "\x1b]10;?\x1b\\"):
				pending = strings.Replace(pending, "\x1b]10;?\x1b\\", "", 1)
				_, _ = tty.Write([]byte("\x1b]10;rgb:ffff/ffff/ffff\x1b\\\x1b[0n\r"))
			default:
				if len(pending) > 64 {
					pending = pending[len(pending)-64:]
				}
				return
			}
		}
	}
}
