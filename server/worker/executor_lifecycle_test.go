package worker

import (
	"sync"
	"testing"
	"time"
)

type countingExecutor struct {
	mu    sync.Mutex
	count int
}

func (e *countingExecutor) ProcessOne() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count++
}

func (e *countingExecutor) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count
}

func TestWorkerDelegatesTaskProcessingToExecutor(t *testing.T) {
	executor := &countingExecutor{}
	w := NewWorkerWithExecutor(executor, time.Millisecond)
	w.Start()
	t.Cleanup(w.Stop)

	deadline := time.After(250 * time.Millisecond)
	for {
		if executor.Count() > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("worker did not delegate processing to executor")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
