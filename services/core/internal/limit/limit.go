// Package limit is a fixed-window rate limiter keyed by caller-supplied
// values (an IP, an email). Those keys are attacker input, so the map is
// bounded: when it is full and nothing has expired, a new key is refused
// rather than letting memory grow.
package limit

import (
	"sync"
	"time"
)

type window struct {
	start time.Time
	n     int
}

type Limiter struct {
	mu   sync.Mutex
	max  int
	per  time.Duration
	keys map[string]*window
	cap  int
	now  func() time.Time
}

// New allows max events per key in each window of length per, tracking at
// most capacity keys.
func New(max int, per time.Duration, capacity int) *Limiter {
	return &Limiter{max: max, per: per, keys: make(map[string]*window), cap: capacity, now: time.Now}
}

// Allow records one event for key and reports whether it is within the limit.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w, ok := l.keys[key]
	if !ok || now.Sub(w.start) >= l.per {
		if !ok && len(l.keys) >= l.cap {
			l.sweep(now)
			if len(l.keys) >= l.cap {
				return false
			}
		}
		l.keys[key] = &window{start: now, n: 1}
		return true
	}
	if w.n >= l.max {
		return false
	}
	w.n++
	return true
}

func (l *Limiter) sweep(now time.Time) {
	for k, w := range l.keys {
		if now.Sub(w.start) >= l.per {
			delete(l.keys, k)
		}
	}
}
