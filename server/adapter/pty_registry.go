package adapter

import (
	"sync"
	"time"
)

const (
	PTYSessionStatusStarting     = "starting"
	PTYSessionStatusRunning      = "running"
	PTYSessionStatusWaitingInput = "waiting_input"
	PTYSessionStatusCompleted    = "completed"
	PTYSessionStatusFailed       = "failed"
	PTYSessionStatusClosed       = "closed"
)

type PTYSessionRecord struct {
	SessionID  string
	RunID      string
	StepID     string
	TaskID     string
	NodeName   string
	SubtaskID  string
	AgentType  string
	AdapterKey string
	WorkDir    string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time

	owner      any
	readerRef  *PTYReader
	writeInput func([]byte) error
	resize     func(cols, rows int) error
}

type PTYSessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*PTYSessionRecord
}

var DefaultPTYSessionRegistry = NewPTYSessionRegistry()

func (r *PTYSessionRecord) Reader() *PTYReader {
	if r == nil {
		return nil
	}
	return r.readerRef
}

func (r *PTYSessionRecord) AttachIO(reader *PTYReader, writeInput func([]byte) error, resize func(cols, rows int) error) {
	if r == nil {
		return
	}
	r.readerRef = reader
	r.writeInput = writeInput
	r.resize = resize
}

func (r *PTYSessionRecord) WriteInput(data []byte) error {
	if r == nil || r.writeInput == nil {
		return nil
	}
	return r.writeInput(data)
}

func (r *PTYSessionRecord) Resize(cols, rows int) error {
	if r == nil || r.resize == nil {
		return nil
	}
	return r.resize(cols, rows)
}

func NewPTYSessionRegistry() *PTYSessionRegistry {
	return &PTYSessionRegistry{sessions: make(map[string]*PTYSessionRecord)}
}

func (r *PTYSessionRegistry) Register(rec *PTYSessionRecord) {
	if r == nil || rec == nil || rec.SessionID == "" {
		return
	}
	now := time.Now()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	if rec.Status == "" {
		rec.Status = PTYSessionStatusStarting
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	copyRec := *rec
	r.sessions[rec.SessionID] = &copyRec
}

func (r *PTYSessionRegistry) Get(sessionID string) (*PTYSessionRecord, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.sessions[sessionID]
	if !ok {
		return nil, false
	}
	copyRec := *rec
	return &copyRec, true
}

func (r *PTYSessionRegistry) ListByRun(runID string) []*PTYSessionRecord {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*PTYSessionRecord, 0)
	for _, rec := range r.sessions {
		if rec.RunID != runID {
			continue
		}
		copyRec := *rec
		out = append(out, &copyRec)
	}
	return out
}

func (r *PTYSessionRegistry) UpdateStatus(sessionID, status string) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.sessions[sessionID]
	if rec == nil {
		return
	}
	rec.Status = status
	rec.UpdatedAt = time.Now()
}

func (r *PTYSessionRegistry) Unregister(sessionID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
}
