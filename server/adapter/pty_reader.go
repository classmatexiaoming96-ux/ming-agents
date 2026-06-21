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
	cond       *sync.Cond
	raw        []byte
	normalized strings.Builder
	ring       string
	closed     bool
}

func NewPTYReader(pty *os.File) *PTYReader {
	r := &PTYReader{pty: pty}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *PTYReader) ReadLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := r.pty.Read(buf)
		if n > 0 {
			normalized := StripANSI(buf[:n])
			r.mu.Lock()
			r.raw = append(r.raw, buf[:n]...)
			r.normalized.WriteString(normalized)
			r.ring += normalized
			if len(r.ring) > ptyReaderRingLimit {
				r.ring = r.ring[len(r.ring)-ptyReaderRingLimit:]
			}
			r.cond.Broadcast()
			r.mu.Unlock()
		}
		if err != nil {
			r.mu.Lock()
			r.closed = true
			r.cond.Broadcast()
			r.mu.Unlock()
			return
		}
	}
}

func (r *PTYReader) WaitFor(ctx context.Context, pattern string, sinceOffset int) int {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return -1
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.cond.Broadcast()
			r.mu.Unlock()
		case <-done:
		}
	}()
	defer close(done)

	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		text := r.normalized.String()
		if sinceOffset < 0 || sinceOffset > len(text) {
			sinceOffset = len(text)
		}
		if loc := re.FindStringIndex(text[sinceOffset:]); loc != nil {
			return sinceOffset + loc[0]
		}
		if ctx.Err() != nil || r.closed {
			return -1
		}
		r.cond.Wait()
	}
}

func (r *PTYReader) Snapshot() (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	text := r.normalized.String()
	return text, len(text)
}

func (r *PTYReader) Close() {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		_ = r.pty.Close()
	}
	r.cond.Broadcast()
	r.mu.Unlock()
}

func StripANSI(data []byte) string {
	s := string(data)
	replacer := strings.NewReplacer("\r\n", "\n", "\r", "\n")
	s = replacer.Replace(s)
	ansi := regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\)|[@-Z\\-_])`)
	return ansi.ReplaceAllString(s, "")
}
