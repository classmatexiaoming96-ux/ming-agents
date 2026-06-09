package main

import (
	"sync"
	"testing"
)

// The daemon keeps a process-local per-agent in-flight counter on top of the DB
// claim, so each tick claims exactly the free slots (free = MaxConcurrentTasks -
// inflight). These tests cover that counter in isolation — no DB or registry
// needed — including its concurrent access from task goroutines.

func newTestDaemon() *Daemon {
	return &Daemon{inflight: map[int64]int{}}
}

func TestDaemonInflightCounter(t *testing.T) {
	d := newTestDaemon()

	if got := d.inflightCount(1); got != 0 {
		t.Fatalf("initial inflightCount = %d, want 0", got)
	}

	d.addInflight(1)
	d.addInflight(1)
	d.addInflight(2)
	if got := d.inflightCount(1); got != 2 {
		t.Errorf("agent 1 inflight = %d, want 2", got)
	}
	if got := d.inflightCount(2); got != 1 {
		t.Errorf("agent 2 inflight = %d, want 1", got)
	}

	d.removeInflight(1)
	if got := d.inflightCount(1); got != 1 {
		t.Errorf("agent 1 inflight after remove = %d, want 1", got)
	}
}

// removeInflight must never drive a counter negative, even if called more times
// than addInflight (defensive against double-decrement on a crash path).
func TestDaemonInflightNeverNegative(t *testing.T) {
	d := newTestDaemon()
	d.addInflight(5)
	d.removeInflight(5)
	d.removeInflight(5) // extra decrement
	if got := d.inflightCount(5); got != 0 {
		t.Errorf("inflightCount = %d, want clamped at 0", got)
	}
}

// Each agent's slot accounting is independent.
func TestDaemonInflightPerAgentIsolation(t *testing.T) {
	d := newTestDaemon()
	d.addInflight(10)
	d.addInflight(20)
	d.removeInflight(10)
	if got := d.inflightCount(10); got != 0 {
		t.Errorf("agent 10 = %d, want 0", got)
	}
	if got := d.inflightCount(20); got != 1 {
		t.Errorf("agent 20 = %d, want 1 (unaffected by agent 10)", got)
	}
}

// add/remove are called from concurrent task goroutines; the mutex must keep the
// count exact. Run with -race.
func TestDaemonInflightConcurrent(t *testing.T) {
	d := newTestDaemon()
	const agents = 4
	const perAgent = 100
	var wg sync.WaitGroup
	for a := int64(0); a < agents; a++ {
		for i := 0; i < perAgent; i++ {
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				d.addInflight(id)
				d.removeInflight(id)
			}(a)
		}
	}
	wg.Wait()
	for a := int64(0); a < agents; a++ {
		if got := d.inflightCount(a); got != 0 {
			t.Errorf("agent %d inflight = %d, want 0 after balanced churn", a, got)
		}
	}
}
