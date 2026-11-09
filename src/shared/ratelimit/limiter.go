// Package ratelimit is a small fixed-window, per-key rate limiter with a hard bound on the number of tracked keys.
package ratelimit

import (
	"sync"
	"time"
)

type entry struct {
	start time.Time
	count int
}

// Limiter allows limit events per window per key. A non-positive limit disables it.
type Limiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	maxKeys int
	entries map[string]*entry
	now     func() time.Time
}

func New(limit int, window time.Duration, maxKeys int) *Limiter {
	return &Limiter{limit: limit, window: window, maxKeys: maxKeys, entries: make(map[string]*entry), now: time.Now}
}

// Result describes the outcome of one Allow call.
type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

func (l *Limiter) Allow(key string) Result {
	if l.limit <= 0 {
		return Result{Allowed: true}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[key]
	if !ok || now.Sub(e.start) >= l.window {
		if !ok && len(l.entries) >= l.maxKeys {
			l.evictLocked(now)
		}
		e = &entry{start: now}
		l.entries[key] = e
	}
	if e.count >= l.limit {
		return Result{Limit: l.limit, RetryAfter: e.start.Add(l.window).Sub(now)}
	}
	e.count++
	return Result{Allowed: true, Limit: l.limit, Remaining: l.limit - e.count}
}

// evictLocked frees space: expired windows first, then arbitrary entries, so memory stays bounded however many distinct keys arrive.
func (l *Limiter) evictLocked(now time.Time) {
	for k, e := range l.entries {
		if now.Sub(e.start) >= l.window {
			delete(l.entries, k)
		}
	}
	for k := range l.entries {
		if len(l.entries) < l.maxKeys {
			return
		}
		delete(l.entries, k)
	}
}
