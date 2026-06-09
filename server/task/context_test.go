package task

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestManagerStartCancelInflight(t *testing.T) {
	m := NewManager()
	if m.Inflight() != 0 {
		t.Fatalf("fresh manager Inflight = %d, want 0", m.Inflight())
	}

	ctx1, done1 := m.Start(context.Background(), 1)
	_, done2 := m.Start(context.Background(), 2)
	if m.Inflight() != 2 {
		t.Fatalf("Inflight = %d, want 2", m.Inflight())
	}

	// Cancel a known task → its context is canceled and Cancel reports true.
	if !m.Cancel(1) {
		t.Error("Cancel(1) = false, want true (task is in-flight)")
	}
	select {
	case <-ctx1.Done():
	case <-time.After(time.Second):
		t.Error("ctx1 not canceled after Cancel(1)")
	}

	// Unknown id → false, no panic.
	if m.Cancel(999) {
		t.Error("Cancel(unknown) = true, want false")
	}

	// done() deregisters even if Cancel already fired.
	done1()
	if m.Inflight() != 1 {
		t.Errorf("after done1, Inflight = %d, want 1", m.Inflight())
	}
	done2()
	if m.Inflight() != 0 {
		t.Errorf("after done2, Inflight = %d, want 0", m.Inflight())
	}
	// A second Cancel on a finished task is a harmless no-op.
	if m.Cancel(2) {
		t.Error("Cancel on finished task = true, want false")
	}
}

// TestManagerCancelPropagatesToChildContext proves the cancel signal travels
// from Cancel(id) down to the derived task context — this is the path the
// daemon's heartbeat loop uses to honor a remote cancel (which SIGTERMs the
// child process).
func TestManagerCancelPropagatesToChildContext(t *testing.T) {
	m := NewManager()
	parent := context.Background()
	ctx, done := m.Start(parent, 42)
	defer done()

	select {
	case <-ctx.Done():
		t.Fatal("context canceled before Cancel was called")
	default:
	}

	m.Cancel(42)

	select {
	case <-ctx.Done():
		if ctx.Err() != context.Canceled {
			t.Errorf("ctx.Err() = %v, want context.Canceled", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("Cancel did not propagate to child context")
	}
}

// TestManagerParentCancelPropagates ensures a canceled parent (daemon shutdown)
// also tears down every in-flight task context.
func TestManagerParentCancelPropagates(t *testing.T) {
	m := NewManager()
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, done := m.Start(parent, 7)
	defer done()

	cancelParent()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancel did not propagate to task context")
	}
}

// TestManagerConcurrentStartDone hammers the registry from many goroutines; run
// with -race to catch any locking regression. It must end with zero in-flight.
func TestManagerConcurrentStartDone(t *testing.T) {
	m := NewManager()
	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(id int64) {
			defer wg.Done()
			_, done := m.Start(context.Background(), id)
			// Interleave cancels against the same id space.
			m.Cancel(id)
			done()
		}(int64(i))
	}
	wg.Wait()
	if got := m.Inflight(); got != 0 {
		t.Errorf("after concurrent churn, Inflight = %d, want 0", got)
	}
}
