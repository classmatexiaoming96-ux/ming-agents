package adapter

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync"
)

const ptyReaderRingLimit = 64 * 1024

type PTYReader struct {
	pty *os.File

	mu         sync.Mutex
	updateCh   chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
	normalized strings.Builder
	ring       string
	closed     bool
}

func NewPTYReader(pty *os.File) *PTYReader {
	return &PTYReader{
		pty:      pty,
		updateCh: make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (r *PTYReader) ReadLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := r.pty.Read(buf)
		if n > 0 {
			normalized := StripANSI(buf[:n])
			r.mu.Lock()
			r.normalized.WriteString(normalized)
			r.ring += normalized
			if len(r.ring) > ptyReaderRingLimit {
				r.ring = r.ring[len(r.ring)-ptyReaderRingLimit:]
			}
			r.notifyLocked()
			r.mu.Unlock()
		}
		if err != nil {
			r.Close()
			return
		}
	}
}

func (r *PTYReader) WaitFor(ctx context.Context, pattern string, sinceOffset int) (string, bool) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", false
	}

	for {
		r.mu.Lock()
		text := r.normalized.String()
		if sinceOffset < 0 || sinceOffset > len(text) {
			sinceOffset = len(text)
		}
		if loc := re.FindStringIndex(text[sinceOffset:]); loc != nil {
			response := text[sinceOffset : sinceOffset+loc[0]]
			r.mu.Unlock()
			return response, true
		}
		if r.closed {
			r.mu.Unlock()
			return "", false
		}
		updateCh := r.updateCh
		done := r.done
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return "", false
		case <-done:
			return "", false
		case <-updateCh:
		}
	}
}

func (r *PTYReader) Snapshot() (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	text := r.normalized.String()
	return text, len(text)
}

func (r *PTYReader) Close() {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		_ = r.pty.Close()
		close(r.done)
		r.notifyLocked()
		r.mu.Unlock()
	})
}

func (r *PTYReader) notifyLocked() {
	close(r.updateCh)
	r.updateCh = make(chan struct{})
}

func StripANSI(data []byte) string {
	s := string(data)
	replacer := strings.NewReplacer("\r\n", "\n", "\r", "\n")
	s = replacer.Replace(s)
	ansi := regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\)|[@-Z\\-_])`)
	return ansi.ReplaceAllString(s, "")
}
