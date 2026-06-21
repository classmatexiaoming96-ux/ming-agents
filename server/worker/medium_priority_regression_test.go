package worker

import (
	"sync"
	"testing"
	"time"
)

func TestWorkerStopCanBeCalledConcurrently(t *testing.T) {
	w := NewWorker(nil, nil, nil, time.Hour)
	w.Start()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Stop()
		}()
	}
	wg.Wait()
}
