package bitbucket

import (
	"sync"
	"time"
)

// requestWindow is the rolling window used for upstream request accounting.
const requestWindow = time.Hour

// requestCounter counts upstream HTTP requests in per-minute buckets over a
// rolling hour. Bitbucket Cloud sends no rate-limit headers, so the client
// accounts for its own traffic and the scanner throttles itself when the hourly
// budget is spent.
type requestCounter struct {
	mu      sync.Mutex
	now     func() time.Time
	minutes map[int64]int // unix minute -> requests in that minute
}

func newRequestCounter(now func() time.Time) *requestCounter {
	if now == nil {
		now = time.Now
	}
	return &requestCounter{now: now, minutes: map[int64]int{}}
}

// record counts one upstream request.
func (r *requestCounter) record() {
	r.mu.Lock()
	defer r.mu.Unlock()
	minute := r.now().Unix() / 60
	r.minutes[minute]++
	r.pruneLocked(minute)
}

// lastHour returns the requests recorded in the last 60 minutes.
func (r *requestCounter) lastHour() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	minute := r.now().Unix() / 60
	r.pruneLocked(minute)
	total := 0
	for _, count := range r.minutes {
		total += count
	}
	return total
}

// pruneLocked drops buckets that fell out of the window. Callers hold r.mu.
func (r *requestCounter) pruneLocked(current int64) {
	oldest := current - int64(requestWindow/time.Minute) + 1
	for minute := range r.minutes {
		if minute < oldest {
			delete(r.minutes, minute)
		}
	}
}
