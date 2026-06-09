package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ming-agents/server/db"
)

// --- unit tests (no database) ---

// Claim short-circuits before touching the pool when no agents are given, so it
// is safe to call on a zero-value Queue. This guards the scheduler's empty-slice
// path.
func TestClaimEmptyAgentIDsReturnsNoTask(t *testing.T) {
	q := &Queue{}
	_, err := q.Claim(context.Background(), "worker-1", nil)
	if !errors.Is(err, ErrNoTask) {
		t.Errorf("Claim(nil agents) err = %v, want ErrNoTask", err)
	}
	_, err = q.Claim(context.Background(), "worker-1", []int64{})
	if !errors.Is(err, ErrNoTask) {
		t.Errorf("Claim(empty agents) err = %v, want ErrNoTask", err)
	}
}

// --- integration tests (require a Postgres reachable via DATABASE_URL) ---
//
// These exercise the real queue state machine — claim ordering, SKIP LOCKED
// concurrency, heartbeat/cancel signalling, and orphan recovery. They run in CI
// (or anywhere DATABASE_URL points at a throwaway Postgres) and skip cleanly
// otherwise, so the unit suite stays green offline.

func testQueue(t *testing.T) (*Queue, int64) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping queue integration tests")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("cannot reach test Postgres: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewQueue(pool), seedAgent(t, pool)
}

// seedAgent inserts a unique throwaway agent and cascades its tasks away on
// cleanup, so tests don't collide with each other or with real data.
func seedAgent(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("test-agent-%d", time.Now().UnixNano())
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO agents (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	t.Cleanup(func() {
		// ON DELETE CASCADE removes the agent's queued tasks too.
		_, _ = pool.Exec(context.Background(), `DELETE FROM agents WHERE id = $1`, id)
	})
	return id
}

func TestQueueClaimOrdersByPriority(t *testing.T) {
	q, agentID := testQueue(t)
	ctx := context.Background()

	// Enqueue out of priority order.
	if _, err := q.Enqueue(ctx, agentID, "low", PriorityLow); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, agentID, "high", PriorityHigh); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, agentID, "medium", PriorityMedium); err != nil {
		t.Fatal(err)
	}

	want := []string{"high", "medium", "low"}
	for _, w := range want {
		got, err := q.Claim(ctx, "w1", []int64{agentID})
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if got.Prompt != w {
			t.Errorf("claim order: got %q, want %q", got.Prompt, w)
		}
		if got.Status != StatusClaimed || got.WorkerID == nil || *got.WorkerID != "w1" {
			t.Errorf("claimed task not marked correctly: %+v", got)
		}
		if got.Attempts != 1 {
			t.Errorf("attempts = %d, want 1 after first claim", got.Attempts)
		}
	}
	// Queue now empty.
	if _, err := q.Claim(ctx, "w1", []int64{agentID}); !errors.Is(err, ErrNoTask) {
		t.Errorf("expected ErrNoTask on empty queue, got %v", err)
	}
}

func TestQueueConcurrentClaimNoDoubleAssign(t *testing.T) {
	q, agentID := testQueue(t)
	ctx := context.Background()

	if _, err := q.Enqueue(ctx, agentID, "solo", PriorityMedium); err != nil {
		t.Fatal(err)
	}

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(n int) {
			defer wg.Done()
			task, err := q.Claim(ctx, fmt.Sprintf("w%d", n), []int64{agentID})
			if err == nil && task != nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Errorf("FOR UPDATE SKIP LOCKED failed: %d workers claimed the same task, want 1", wins)
	}
}

func TestQueueHeartbeatSignalsCancel(t *testing.T) {
	q, agentID := testQueue(t)
	ctx := context.Background()

	enq, err := q.Enqueue(ctx, agentID, "cancelme", PriorityMedium)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := q.Claim(ctx, "w1", []int64{agentID})
	if err != nil {
		t.Fatal(err)
	}

	// Normal heartbeat → no cancel.
	cancel, err := q.Heartbeat(ctx, claimed.ID, "w1")
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if cancel {
		t.Error("heartbeat reported cancel before any was requested")
	}

	// Remote cancel request → next heartbeat reports it.
	if _, err := q.RequestCancel(ctx, enq.ID); err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}
	cancel, err = q.Heartbeat(ctx, claimed.ID, "w1")
	if err != nil {
		t.Fatalf("Heartbeat after cancel: %v", err)
	}
	if !cancel {
		t.Error("heartbeat did not report the requested cancel")
	}

	// Heartbeat from a different worker (lost ownership) is treated as cancel.
	cancel, err = q.Heartbeat(ctx, claimed.ID, "someone-else")
	if err != nil {
		t.Fatalf("Heartbeat wrong worker: %v", err)
	}
	if !cancel {
		t.Error("heartbeat from non-owner should signal cancel")
	}
}

func TestQueueRecoverOrphanedTasks(t *testing.T) {
	q, agentID := testQueue(t)
	ctx := context.Background()

	// Claim a task, then backdate its heartbeat to simulate a dead worker.
	if _, err := q.Enqueue(ctx, agentID, "orphan", PriorityMedium); err != nil {
		t.Fatal(err)
	}
	claimed, err := q.Claim(ctx, "dead-worker", []int64{agentID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.pool.Exec(ctx,
		`UPDATE agent_task_queue SET heartbeat_at = now() - interval '1 hour' WHERE id = $1`,
		claimed.ID); err != nil {
		t.Fatal(err)
	}

	n, err := q.RecoverOrphanedTasks(ctx, time.Minute)
	if err != nil {
		t.Fatalf("RecoverOrphanedTasks: %v", err)
	}
	if n != 1 {
		t.Errorf("recovered = %d, want 1", n)
	}
	got, err := q.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending || got.WorkerID != nil {
		t.Errorf("recovered task = %+v, want pending with no worker", got)
	}

	// A stale task that has exhausted its attempts is failed, not recovered.
	if _, err := q.Enqueue(ctx, agentID, "exhausted", PriorityMedium); err != nil {
		t.Fatal(err)
	}
	exhausted, err := q.Claim(ctx, "dead-worker", []int64{agentID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.pool.Exec(ctx,
		`UPDATE agent_task_queue
		 SET heartbeat_at = now() - interval '1 hour', attempts = max_attempts
		 WHERE id = $1`, exhausted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.RecoverOrphanedTasks(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err = q.Get(ctx, exhausted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed {
		t.Errorf("exhausted task status = %q, want failed", got.Status)
	}
}

func TestQueueLifecycleTransitions(t *testing.T) {
	q, agentID := testQueue(t)
	ctx := context.Background()

	if _, err := q.Enqueue(ctx, agentID, "work", PriorityMedium); err != nil {
		t.Fatal(err)
	}
	claimed, err := q.Claim(ctx, "w1", []int64{agentID})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.MarkRunning(ctx, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(ctx, claimed.ID, "the result"); err != nil {
		t.Fatal(err)
	}
	got, err := q.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted || got.Result == nil || *got.Result != "the result" {
		t.Errorf("completed task = %+v, want completed with result", got)
	}

	// CountInflight reflects only non-terminal tasks for the worker.
	n, err := q.CountInflight(ctx, agentID, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("CountInflight after completion = %d, want 0", n)
	}
}
