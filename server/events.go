package main

import (
	"sync"

	"github.com/ming-agents/server/task"
)

// Event types pushed to WebSocket clients.
const (
	EventCreated   = "task.created"
	EventClaimed   = "task.claimed"
	EventRunning   = "task.running"
	EventChunk     = "task.chunk"
	EventCompleted = "task.completed"
	EventFailed    = "task.failed"
	EventCanceled  = "task.canceled"
)

// Event is one message broadcast over the bus.
type Event struct {
	Type   string     `json:"type"`
	Task   *task.Task `json:"task,omitempty"`
	TaskID int64      `json:"task_id"`
	Stream string     `json:"stream,omitempty"` // for chunk events: stdout|stderr
	Chunk  string     `json:"chunk,omitempty"`
}

// EventBus is a minimal in-process pub/sub. Subscribers that fall behind drop
// messages rather than blocking the publisher (the DB remains source of truth).
type EventBus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func NewEventBus() *EventBus {
	return &EventBus{subs: map[chan Event]struct{}{}}
}

// Subscribe returns a channel of events and an unsubscribe func.
func (b *EventBus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Publish fans out to all subscribers, dropping for any that are full.
func (b *EventBus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default: // slow consumer; drop
		}
	}
}
