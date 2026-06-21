package worker

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ming-agents/server/adapter"
	"github.com/ming-agents/server/domain"
	"github.com/ming-agents/server/store"
)

// TaskCallback is called by the worker when a task completes or fails.
type TaskCallback interface {
	OnTaskCompleted(taskID uuid.UUID) error
}

// Worker consumes tasks from agent_task_queue, invokes adapters, and writes results.
// Epic 4.1: 队列消费 worker — 消费 agent_task_queue → 调 Adapter → 回写.
type Worker struct {
	store        *store.Store
	registry     *adapter.Registry
	callback     TaskCallback
	pollInterval time.Duration
	stopCh       chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
}

// NewWorker creates a new queue consumer worker.
func NewWorker(s *store.Store, r *adapter.Registry, callback TaskCallback, pollInterval time.Duration) *Worker {
	if pollInterval == 0 {
		pollInterval = 100 * time.Millisecond
	}
	return &Worker{
		store:        s,
		registry:     r,
		callback:     callback,
		pollInterval: pollInterval,
		stopCh:       make(chan struct{}),
	}
}

// Start begins the worker's consumption loop.
func (w *Worker) Start() {
	w.wg.Add(1)
	go w.run()
}

// Stop gracefully stops the worker.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	w.wg.Wait()
}

func (w *Worker) run() {
	defer w.wg.Done()
	for {
		select {
		case <-w.stopCh:
			return
		case <-time.After(w.pollInterval):
			w.processOne()
		}
	}
}

func (w *Worker) processOne() {
	task, err := w.store.ClaimTask()
	if err != nil {
		// sql.ErrNoRows means no pending tasks — not an error.
		return
	}

	log.Printf("[worker] processing task %s (step %s)", task.ID, task.StepID)

	// Get adapter.
	a, err := w.registry.Get(task.AdapterKey)
	if err != nil {
		w.failTask(task, fmt.Sprintf("adapter not found: %s", task.AdapterKey))
		return
	}

	// Build request.
	var req adapter.AgentRequest
	if err := json.Unmarshal(task.AgentRequest, &req); err != nil {
		w.failTask(task, fmt.Sprintf("unmarshal request: %v", err))
		return
	}

	// Invoke.
	result, err := a.Invoke(req)
	if err != nil {
		w.failTask(task, fmt.Sprintf("adapter invoke: %v", err))
		return
	}

	// Record result.
	w.completeTask(task, result)
}

func (w *Worker) completeTask(task *domain.Task, result *adapter.AgentResult) {
	raw, _ := json.Marshal(result)
	summary := result.Summary
	if summary == "" {
		summary = result.Output
		if len(summary) > 200 {
			summary = summary[:200] + "..."
		}
	}
	// Atomically write status + result + summary + timestamp.
	if err := w.store.SetTaskResult(task.ID, raw, summary); err != nil {
		log.Printf("[worker] set result: %v", err)
		return
	}
	// Notify callback after result is durably written.
	if w.callback != nil {
		if err := w.callback.OnTaskCompleted(task.ID); err != nil {
			log.Printf("[worker] OnTaskCompleted callback: %v", err)
		}
	}
}

func (w *Worker) failTask(task *domain.Task, reason string) {
	log.Printf("[worker] task %s failed: %s", task.ID, reason)
	result := &adapter.AgentResult{
		Error:   reason,
		Summary: reason,
	}
	raw, _ := json.Marshal(result)
	if err := w.store.SetTaskFailure(task.ID, raw, reason); err != nil {
		log.Printf("[worker] set failure result: %v", err)
		return
	}
	if w.callback != nil {
		if err := w.callback.OnTaskCompleted(task.ID); err != nil {
			log.Printf("[worker] OnTaskCompleted callback (fail): %v", err)
		}
	}
}

// Pool manages a pool of workers for parallel task consumption.
type Pool struct {
	workers []*Worker
}

// NewPool creates a pool of N workers.
func NewPool(n int, s *store.Store, r *adapter.Registry, pollInterval time.Duration) *Pool {
	pool := &Pool{workers: make([]*Worker, n)}
	for i := 0; i < n; i++ {
		pool.workers[i] = NewWorker(s, r, nil, pollInterval)
	}
	return pool
}

// Start starts all workers in the pool.
func (p *Pool) Start() {
	for _, w := range p.workers {
		w.Start()
	}
}

// Stop stops all workers in the pool.
func (p *Pool) Stop() {
	for _, w := range p.workers {
		w.Stop()
	}
}
