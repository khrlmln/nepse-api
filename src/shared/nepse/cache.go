package nepse

import (
	"context"
	"sync"
	"time"
)

const (
	cacheMaxEntries = 2048
	cacheMaxBytes   = 64 << 20 // total size of cached bodies
	// A single body above this share of the budget is served but never stored, so one huge response cannot evict everything else.
	cacheMaxEntryBytes = cacheMaxBytes / 4
	cacheIdleEviction  = 10 * time.Minute
	// cacheDefaultStale is how long past its TTL an entry may still be served when a refresh fails (the client overrides it from NEPSE_MAX_STALE).
	cacheDefaultStale = time.Minute
)

type responseCache struct {
	mu         sync.Mutex
	entries    map[string]*cacheEntry
	bytes      int
	maxEntries int
	maxBytes   int
	maxStale   time.Duration // 0 disables stale serving
}

type cacheEntry struct {
	value      []byte
	expires    time.Time
	staleUntil time.Time // last moment the value may still be served after a failed refresh
	lastUse    time.Time
	load       *inflightLoad // non-nil while a refresh is running
}

// inflightLoad is one running load whose outcome all waiters adopt.
type inflightLoad struct {
	done  chan struct{}
	value []byte // fresh value on success
	stale []byte // previous value, if the load failed and one existed
	err   error
}

func newResponseCache() *responseCache {
	return &responseCache{entries: make(map[string]*cacheEntry), maxEntries: cacheMaxEntries, maxBytes: cacheMaxBytes, maxStale: cacheDefaultStale}
}

// Do returns the cached value for key or loads it with fn. The returned flags say whether the value came from the cache (fresh hit) and whether it is stale (served because the refresh failed).
func (c *responseCache) Do(ctx context.Context, key string, ttl time.Duration, fn func(context.Context) ([]byte, error)) (value []byte, hit, stale bool, err error) {
	for {
		c.mu.Lock()
		now := time.Now()
		e := c.entries[key]
		if e != nil {
			e.lastUse = now
			if e.load == nil && now.Before(e.expires) {
				c.mu.Unlock()
				return e.value, true, false, nil
			}
			if e.load != nil {
				l := e.load
				c.mu.Unlock()
				v, st, retry, err := wait(ctx, l)
				if retry {
					continue
				}
				return v, false, st, err
			}
		}
		return c.lead(ctx, key, ttl, e, fn)
	}
}

// wait blocks until the load finishes and adopts its outcome. retry is true only when the load failed because ITS caller's context ended (ours is still alive).
func wait(ctx context.Context, l *inflightLoad) (value []byte, stale, retry bool, err error) {
	select {
	case <-ctx.Done():
		return nil, false, false, ctx.Err()
	case <-l.done:
	}
	switch {
	case l.err == nil:
		return l.value, false, false, nil
	case isContextErr(l.err):
		return nil, false, true, nil
	case l.stale != nil:
		return l.stale, true, false, nil
	default:
		return nil, false, false, l.err
	}
}

// lead runs the load for key. It is entered with c.mu held and releases it.
func (c *responseCache) lead(ctx context.Context, key string, ttl time.Duration, existing *cacheEntry, fn func(context.Context) ([]byte, error)) ([]byte, bool, bool, error) {
	if existing == nil && !c.makeRoomLocked(time.Now()) {
		// Every slot holds an in-flight load and nothing can be evicted: serve this request uncached rather than exceed the bound.
		c.mu.Unlock()
		v, err := fn(ctx)
		return v, false, false, err
	}
	l := &inflightLoad{done: make(chan struct{})}
	e := existing
	if e == nil {
		e = &cacheEntry{lastUse: time.Now()}
		c.entries[key] = e
	}
	if len(e.value) > 0 && time.Now().Before(e.staleUntil) {
		l.stale = e.value // still within the stale window
	}
	e.load = l
	c.mu.Unlock()

	v, err := fn(ctx)

	c.mu.Lock()
	e.load = nil
	l.value, l.err = v, err
	switch {
	case err == nil:
		c.storeLocked(key, e, v, ttl)
	case l.stale != nil:
		e.expires = time.Now() // keep the stale value for later failures; refresh on next request
	default:
		c.removeLocked(key) // nothing usable to keep (new entry, or a value past its stale window)
	}
	close(l.done)
	c.mu.Unlock()

	if err != nil && l.stale != nil {
		return l.stale, false, true, nil
	}
	return v, false, false, err
}

// storeLocked records a successful load, respecting the byte budget.
func (c *responseCache) storeLocked(key string, e *cacheEntry, v []byte, ttl time.Duration) {
	c.bytes -= len(e.value)
	e.value, e.expires = nil, time.Now()
	if len(v) > cacheMaxEntryBytes || len(v) > c.maxBytes {
		delete(c.entries, key) // too large to cache; the caller still gets the value
		return
	}
	for c.bytes+len(v) > c.maxBytes && c.evictOneLocked(key) {
	}
	e.value, e.expires = v, time.Now().Add(ttl)
	e.staleUntil = e.expires.Add(c.maxStale)
	c.bytes += len(v)
}

// makeRoomLocked frees an entry slot for a new key. It reports false when every entry is in flight, i.e. nothing can be evicted.
func (c *responseCache) makeRoomLocked(now time.Time) bool {
	if len(c.entries) < c.maxEntries {
		return true
	}
	for k, e := range c.entries { // idle entries first
		if e.load == nil && now.Sub(e.lastUse) > cacheIdleEviction {
			c.removeLocked(k)
		}
	}
	for len(c.entries) >= c.maxEntries {
		if !c.evictOneLocked("") {
			return false
		}
	}
	return true
}

// evictOneLocked removes the least recently used entry that is not loading (and is not keep). It reports whether something was evicted.
func (c *responseCache) evictOneLocked(keep string) bool {
	var oldestKey string
	var oldest time.Time
	for k, e := range c.entries {
		if e.load != nil || k == keep {
			continue
		}
		if oldestKey == "" || e.lastUse.Before(oldest) {
			oldestKey, oldest = k, e.lastUse
		}
	}
	if oldestKey == "" {
		return false
	}
	c.removeLocked(oldestKey)
	return true
}

func (c *responseCache) removeLocked(key string) {
	if e, ok := c.entries[key]; ok {
		c.bytes -= len(e.value)
		delete(c.entries, key)
	}
}
