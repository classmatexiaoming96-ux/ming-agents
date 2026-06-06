package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shrimp-mvp/server/agent"
	"github.com/shrimp-mvp/server/task"
)

// Daemon is the scheduler: it polls the queue, claims work per agent up to each
// agent's concurrency, and runs each task as a child Claude Code process.
type Daemon struct {
	cfg     *Config
	pool    *pgxpool.Pool
	queue   *task.Queue
	runner  *task.Runner
	manager *task.Manager
	reg     *agent.Registry
	bus     *EventBus

	// inflight counts running tasks per agent id (process-local guard atop the
	// DB claim, so the scheduler claims exactly the free slots).
	mu       sync.Mutex
	inflight map[int64]int
}

func NewDaemon(cfg *Config, pool *pgxpool.Pool, reg *agent.Registry, bus *EventBus) *Daemon {
	return &Daemon{
		cfg:      cfg,
		pool:     pool,
		queue:    task.NewQueue(pool),
		runner:   task.NewRunner(0),
		manager:  task.NewManager(),
		reg:      reg,
		bus:      bus,
		inflight: map[int64]int{},
	}
}

// Run blocks until ctx is canceled, scheduling tasks on each tick.
func (d *Daemon) Run(ctx context.Context) error {
	n, err := d.queue.RecoverOrphanedTasks(ctx, d.cfg.OrphanTimeout)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("recovered %d orphaned task(s)", n)
	}

	ticker := time.NewTicker(d.cfg.PollInterval)
	defer ticker.Stop()
	log.Printf("scheduler started (worker=%s, poll=%s)", d.cfg.WorkerID, d.cfg.PollInterval)

	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			log.Printf("scheduler stopping; waiting for %d in-flight task(s)", d.manager.Inflight())
			wg.Wait()
			return nil
		case <-ticker.C:
			d.tick(ctx, &wg)
		}
	}
}

// tick claims and launches as many tasks as there is free capacity for.
func (d *Daemon) tick(ctx context.Context, wg *sync.WaitGroup) {
	for _, a := range d.reg.All() {
		free := a.MaxConcurrentTasks - d.inflightCount(a.ID)
		for i := 0; i < free; i++ {
			t, err := d.queue.Claim(ctx, d.cfg.WorkerID, []int64{a.ID})
			if err == task.ErrNoTask {
				break
			}
			if err != nil {
				log.Printf("claim error (agent=%s): %v", a.Name, err)
				break
			}
			d.addInflight(a.ID)
			d.publish(Event{Type: EventClaimed, Task: t, TaskID: t.ID})

			wg.Add(1)
			go func(t *task.Task, ag *agent.Agent) {
				defer wg.Done()
				defer d.removeInflight(ag.ID)
				d.execute(ctx, t, ag)
			}(t, a)
		}
	}
}

// execute runs a single task through its lifecycle with cancellation + heartbeat.
func (d *Daemon) execute(parent context.Context, t *task.Task, a *agent.Agent) {
	ctx, done := d.manager.Start(parent, t.ID)
	defer done()

	if err := d.queue.MarkRunning(ctx, t.ID); err != nil {
		log.Printf("task %d mark running: %v", t.ID, err)
	}
	t.Status = task.StatusRunning
	d.publish(Event{Type: EventRunning, Task: t, TaskID: t.ID})

	if t.RepoPath != "" {
		stats, err := fetchCodeGraphStats(t.RepoPath)
		if err != nil {
			log.Printf("task %d codegraph stats: %v", t.ID, err)
		} else {
			log.Printf("task %d codegraph stats: %v", t.ID, stats)
		}
	}

	// Heartbeat loop: refresh liveness and honor remote cancel requests.
	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go d.heartbeat(hbCtx, t.ID, d.manager)

	spec := task.RunSpec{
		Command:       d.commandFor(a),
		Args:          d.argsFor(a),
		Prompt:        t.Prompt,
		Model:         a.Model,
		ThinkingLevel: a.ThinkingLevel,
		Env: []string{
			"SHRIMP_TASK_ID=" + itoa(t.ID),
			"SHRIMP_AGENT=" + a.Name,
		},
	}

	result, err := d.runner.Run(ctx, spec, func(stream, line string) {
		d.publish(Event{Type: EventChunk, TaskID: t.ID, Stream: stream, Chunk: line})
	})
	stopHB()

	// Use a short detached context for the terminal write so a canceled parent
	// context doesn't prevent recording the final state.
	finCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch {
	case err == nil:
		_ = d.queue.Complete(finCtx, t.ID, result)
		t.Status = task.StatusCompleted
		d.publish(Event{Type: EventCompleted, Task: refresh(finCtx, d.queue, t), TaskID: t.ID})
	case ctx.Err() != nil:
		_ = d.queue.Canceled(finCtx, t.ID, result)
		t.Status = task.StatusCanceled
		d.publish(Event{Type: EventCanceled, Task: refresh(finCtx, d.queue, t), TaskID: t.ID})
		log.Printf("task %d canceled", t.ID)
	default:
		_ = d.queue.Fail(finCtx, t.ID, err.Error(), result)
		t.Status = task.StatusFailed
		d.publish(Event{Type: EventFailed, Task: refresh(finCtx, d.queue, t), TaskID: t.ID})
		log.Printf("task %d failed: %v", t.ID, err)
	}
}

// heartbeat refreshes liveness; if a remote cancel was requested, it cancels the
// local task context (which SIGTERMs the child).
func (d *Daemon) heartbeat(ctx context.Context, id int64, mgr *task.Manager) {
	ticker := time.NewTicker(d.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			canceled, err := d.queue.Heartbeat(ctx, id, d.cfg.WorkerID)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("task %d heartbeat: %v", id, err)
				}
				continue
			}
			if canceled {
				mgr.Cancel(id)
				return
			}
		}
	}
}

// CancelTask requests cancellation of a task. If it is running on this daemon it
// is canceled immediately; otherwise the DB flag is set for the owning worker.
func (d *Daemon) CancelTask(ctx context.Context, id int64) (*task.Task, error) {
	if d.manager.Cancel(id) {
		// Mark intent in DB too, for observers.
		_, _ = d.queue.RequestCancel(ctx, id)
		return d.queue.Get(ctx, id)
	}
	return d.queue.RequestCancel(ctx, id)
}

func (d *Daemon) commandFor(a *agent.Agent) string {
	if a.Command != "" {
		return a.Command
	}
	return d.cfg.ClaudeCommand
}

func (d *Daemon) argsFor(a *agent.Agent) []string {
	if len(a.Args) > 0 {
		return a.Args
	}
	return d.cfg.ClaudeArgs
}

func (d *Daemon) publish(e Event) { d.bus.Publish(e) }

func (d *Daemon) inflightCount(agentID int64) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.inflight[agentID]
}
func (d *Daemon) addInflight(agentID int64) {
	d.mu.Lock()
	d.inflight[agentID]++
	d.mu.Unlock()
}
func (d *Daemon) removeInflight(agentID int64) {
	d.mu.Lock()
	if d.inflight[agentID] > 0 {
		d.inflight[agentID]--
	}
	d.mu.Unlock()
}

func refresh(ctx context.Context, q *task.Queue, fallback *task.Task) *task.Task {
	if t, err := q.Get(ctx, fallback.ID); err == nil {
		return t
	}
	return fallback
}

func fetchCodeGraphStats(repoPath string) (map[string]int, error) {
	if repoPath == "" {
		return nil, nil
	}

	cmd := exec.Command("/usr/local/bin/codegraph", "stats", "--repo", repoPath)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codegraph stats failed: %v %s", err, stderr.String())
	}

	var stats map[string]int
	if err := json.Unmarshal(out.Bytes(), &stats); err != nil {
		return nil, fmt.Errorf("parse codegraph stats: %w", err)
	}
	return stats, nil
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
