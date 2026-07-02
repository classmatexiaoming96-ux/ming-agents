package api

import (
	"os"
	"sync"
	"time"
)

// tokenBucket is a minimal, dependency-free rate limiter: it refills tokens at a
// fixed rate up to a burst ceiling and reports whether a request may proceed.
// It guards the new memory contradiction write endpoints so a runaway script
// can't evict the whole flagged set in one minute.
type tokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	burst      float64
	refillRate float64 // tokens per second
	last       time.Time
}

// newTokenBucket builds a bucket that refills ratePerMin tokens each minute and
// tolerates a burst of burst requests.
func newTokenBucket(ratePerMin, burst float64) *tokenBucket {
	return &tokenBucket{
		tokens:     burst,
		burst:      burst,
		refillRate: ratePerMin / 60.0,
		last:       nowFunc(),
	}
}

// allow reports whether a request may proceed and, if not, how many seconds the
// caller should wait before retrying.
func (b *tokenBucket) allow() (bool, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := nowFunc()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.refillRate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	retry := 1
	if b.refillRate > 0 {
		retry = int((1-b.tokens)/b.refillRate) + 1
	}
	return false, retry
}

// nowFunc is overridable so tests can drive the bucket with a fake clock.
var nowFunc = time.Now

// ratelimitDisabledEnv, when set to "1", turns the memory API limiter off.
const ratelimitDisabledEnv = "MEMORY_API_RATELIMIT_DISABLE"

// ratelimitDisabled reports whether limiting is switched off via env.
func ratelimitDisabled() bool {
	return os.Getenv(ratelimitDisabledEnv) == "1"
}
