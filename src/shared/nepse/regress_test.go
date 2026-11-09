package nepse

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

func TestMalformedAndWrongShapeResponsesAreNeverCached(t *testing.T) {
	f := newFake(t)
	c := f.client(func(cfg *Config) { cfg.CacheTTL = time.Minute })
	ctx := context.Background()

	f.summary.Store(`{"not":"valid json`)
	if _, err := c.Get(ctx, EpSummary); apierrors.Normalize(err).Code != apierrors.CodeUpstreamInvalidResp {
		t.Fatalf("malformed JSON: %v", err)
	}
	f.summary.Store(`{"an":"object, not an array"}`)
	if _, err := c.Get(ctx, EpSummary); apierrors.Normalize(err).Code != apierrors.CodeUpstreamInvalidResp {
		t.Fatalf("wrong shape: %v", err)
	}
	f.summary.Store(`[1,2,3]`)
	if _, err := c.Get(ctx, EpSummary); apierrors.Normalize(err).Code != apierrors.CodeUpstreamInvalidResp {
		t.Fatalf("array of non-objects: %v", err)
	}
	// the bad bodies must not have poisoned the entry: the next good body is served at once
	f.summary.Store(`[{"detail":"x","value":1}]`)
	if v, err := c.Get(ctx, EpSummary); err != nil || v == nil {
		t.Fatalf("good response after bad ones: %v %v", v, err)
	}
	before := f.getCount.Load()
	for i := 0; i < 3; i++ {
		if _, err := c.Get(ctx, EpSummary); err != nil {
			t.Fatal(err)
		}
	}
	if f.getCount.Load() != before {
		t.Fatalf("valid response was not cached")
	}
}

func TestPingBypassesCacheAndStaleFallback(t *testing.T) {
	f := newFake(t)
	c := f.client(func(cfg *Config) { cfg.CacheTTL = 40 * time.Millisecond; cfg.MaxStale = time.Minute })
	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, EpMarketStatus); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond) // cache entry is now stale
	f.srv.Close()
	if _, err := c.Get(ctx, EpMarketStatus); err != nil {
		t.Fatalf("ordinary reads keep serving stale data during an outage: %v", err)
	}
	if err := c.Ping(ctx); apierrors.Normalize(err).Code != apierrors.CodeUpstreamUnavailable {
		t.Fatalf("Ping must NOT be satisfied from the cache: %v", err)
	}
}

func TestNegativeSymbolCacheIsBounded(t *testing.T) {
	f := newFake(t)
	c := f.client(nil)
	for i := 0; i < 3*maxSecurityMisses; i++ {
		if _, err := c.SecurityID(context.Background(), fmt.Sprintf("NOPE%d", i)); apierrors.Normalize(err).Code != apierrors.CodeNotFound {
			t.Fatal(err)
		}
		c.securityMu.Lock()
		n := len(c.securityMiss)
		c.securityMu.Unlock()
		if n > maxSecurityMisses {
			t.Fatalf("negative cache grew to %d (> %d)", n, maxSecurityMisses)
		}
	}
}

func TestCacheStaysBoundedWhenEverySlotIsLoading(t *testing.T) {
	c := newResponseCache()
	c.maxEntries = 2
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(2)
	var done sync.WaitGroup
	for _, k := range []string{"a", "b"} {
		done.Add(1)
		go func() {
			defer done.Done()
			_, _, _, _ = c.Do(context.Background(), k, time.Minute, func(context.Context) ([]byte, error) {
				started.Done()
				<-release
				return []byte("x"), nil
			})
		}()
	}
	started.Wait()
	v, _, _, err := c.Do(context.Background(), "third", time.Minute, func(context.Context) ([]byte, error) { return []byte("direct"), nil })
	if err != nil || string(v) != "direct" {
		t.Fatalf("overflow request must still be served (uncached): %q %v", v, err)
	}
	c.mu.Lock()
	n := len(c.entries)
	c.mu.Unlock()
	if n > 2 {
		t.Fatalf("cache grew past its bound: %d", n)
	}
	close(release)
	done.Wait()
}

func TestCacheByteBudgetEvictsLeastRecentlyUsed(t *testing.T) {
	c := newResponseCache()
	c.maxBytes = 100
	load := func(key string, n int) {
		if _, _, _, err := c.Do(context.Background(), key, time.Hour, func(context.Context) ([]byte, error) { return make([]byte, n), nil }); err != nil {
			t.Fatal(err)
		}
	}
	load("a", 20)
	load("b", 20)
	load("c", 20)
	load("d", 20)
	if _, hit, _, _ := c.Do(context.Background(), "a", time.Hour, func(context.Context) ([]byte, error) { return make([]byte, 20), nil }); !hit {
		t.Fatal("a should still be cached")
	}
	load("e", 20) // 5 x 20 = 100: still within budget
	load("f", 20) // now over budget: the least recently used (b) must go
	c.mu.Lock()
	total, entries := c.bytes, len(c.entries)
	_, hasB := c.entries["b"]
	c.mu.Unlock()
	if total > 100 || hasB || entries != 5 {
		t.Fatalf("budget not enforced: bytes=%d entries=%d hasB=%v", total, entries, hasB)
	}
	big := make([]byte, cacheMaxEntryBytes+1) // a single oversized body is served but never stored
	c2 := newResponseCache()
	v, hit, _, err := c2.Do(context.Background(), "big", time.Hour, func(context.Context) ([]byte, error) { return big, nil })
	if err != nil || hit || len(v) != len(big) || c2.bytes != 0 || len(c2.entries) != 0 {
		t.Fatalf("oversized: hit=%v len=%d bytes=%d entries=%d err=%v", hit, len(v), c2.bytes, len(c2.entries), err)
	}
}

func TestCacheStaleOnErrorAndSharedFailure(t *testing.T) {
	c := newResponseCache()
	ctx := context.Background()
	if _, _, _, err := c.Do(ctx, "k", 10*time.Millisecond, func(context.Context) ([]byte, error) { return []byte("v1"), nil }); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	v, hit, stale, err := c.Do(ctx, "k", time.Hour, func(context.Context) ([]byte, error) { return nil, fmt.Errorf("boom") })
	if err != nil || !stale || hit || string(v) != "v1" {
		t.Fatalf("stale-on-error: %q hit=%v stale=%v err=%v", v, hit, stale, err)
	}
	var calls atomic.Int32 // a failing load with no stale value: waiters share the ONE failure
	gate := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := c.Do(ctx, "fresh", time.Hour, func(context.Context) ([]byte, error) {
				calls.Add(1)
				<-gate
				return nil, fmt.Errorf("upstream down")
			})
			errs <- err
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err == nil {
			t.Fatal("a failing load must fail all of its waiters")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("failing load executed %d times, want 1", calls.Load())
	}
}

func TestAuthFailureIsSharedAndCooledDown(t *testing.T) {
	f := newFake(t)
	f.proveFail.Store(true)
	c := f.client(nil)
	var wg sync.WaitGroup
	errs := make(chan error, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Get(context.Background(), EpSummary)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if code := apierrors.Normalize(err).Code; code != apierrors.CodeUpstreamUnavailable && code != apierrors.CodeUpstreamAuthFailed {
			t.Fatalf("unexpected: %v", err)
		}
	}
	afterBurst := f.prove.Load()
	if afterBurst > int32(maxAttempts) {
		t.Fatalf("30 concurrent callers caused %d login attempts; waiters must share one refresh (<= %d)", afterBurst, maxAttempts)
	}
	for i := 0; i < 5; i++ { // inside the cooldown: fail fast, no new login
		if _, err := c.Get(context.Background(), EpSummary); err == nil {
			t.Fatal("expected failure")
		}
	}
	if f.prove.Load() != afterBurst {
		t.Fatalf("cooldown did not suppress new login attempts: %d -> %d", afterBurst, f.prove.Load())
	}
	f.proveFail.Store(false)
	time.Sleep(authFailureCooldown + 100*time.Millisecond)
	if _, err := c.Get(context.Background(), EpSummary); err != nil {
		t.Fatalf("recovery after the cooldown: %v", err)
	}
}

func TestCanceledLeaderDoesNotFailWaiters(t *testing.T) {
	f := newFake(t)
	f.proveDelay.Store(int64(300 * time.Millisecond))
	c := f.client(nil)
	leaderCtx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	leaderDone := make(chan error, 1)
	go func() { _, err := c.Get(leaderCtx, EpSummary); leaderDone <- err }()
	time.Sleep(20 * time.Millisecond) // leader is now the in-flight refresh
	if _, err := c.Get(context.Background(), EpSummary); err != nil {
		t.Fatalf("a waiter must retry when the leader's own context ended: %v", err)
	}
	if err := <-leaderDone; err == nil {
		t.Fatal("leader should have timed out")
	}
}

func TestBaseURLRejectsQueryAndFragment(t *testing.T) {
	for _, u := range []string{"https://www.nepalstock.com/?a=1", "https://www.nepalstock.com/#frag", "https://www.nepalstock.com?", "https://www.nepalstock.com/x"} {
		if c, err := NewClient(Config{BaseURL: u}); err == nil {
			_ = c.Close(context.Background())
			t.Errorf("%q accepted", u)
		}
	}
	if c, err := NewClient(Config{BaseURL: "https://www.nepalstock.com"}); err != nil {
		t.Fatal(err)
	} else {
		_ = c.Close(context.Background())
	}
}

func TestStaleDataIsServedOnlyWithinTheStaleWindow(t *testing.T) {
	c := newResponseCache()
	c.maxStale = 150 * time.Millisecond
	ctx := context.Background()
	boom := func(context.Context) ([]byte, error) { return nil, fmt.Errorf("upstream down") }
	if _, _, _, err := c.Do(ctx, "k", 20*time.Millisecond, func(context.Context) ([]byte, error) { return []byte("old"), nil }); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond) // expired, but inside the stale window
	if v, _, stale, err := c.Do(ctx, "k", time.Hour, boom); err != nil || !stale || string(v) != "old" {
		t.Fatalf("within the window the stale value is served: %q stale=%v err=%v", v, stale, err)
	}
	time.Sleep(200 * time.Millisecond) // now older than ttl + maxStale: old data must never look current
	if v, _, stale, err := c.Do(ctx, "k", time.Hour, boom); err == nil || stale || v != nil {
		t.Fatalf("beyond the window the failure must be returned, got %q stale=%v err=%v", v, stale, err)
	}
	c.mu.Lock()
	n, bytes := len(c.entries), c.bytes
	c.mu.Unlock()
	if n != 0 || bytes != 0 {
		t.Fatalf("the unusable entry must be dropped and its bytes released: entries=%d bytes=%d", n, bytes)
	}
	// a successful refresh afterwards works normally
	if v, _, _, err := c.Do(ctx, "k", time.Hour, func(context.Context) ([]byte, error) { return []byte("new"), nil }); err != nil || string(v) != "new" {
		t.Fatalf("%q %v", v, err)
	}
}

func TestZeroMaxStaleNeverServesStaleData(t *testing.T) {
	c := newResponseCache()
	c.maxStale = 0
	ctx := context.Background()
	if _, _, _, err := c.Do(ctx, "k", 10*time.Millisecond, func(context.Context) ([]byte, error) { return []byte("old"), nil }); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, _, stale, err := c.Do(ctx, "k", time.Hour, func(context.Context) ([]byte, error) { return nil, fmt.Errorf("down") }); err == nil || stale {
		t.Fatalf("stale serving is disabled: stale=%v err=%v", stale, err)
	}
}

func TestClientHonoursMaxStaleEndToEnd(t *testing.T) {
	f := newFake(t)
	c := f.client(func(cfg *Config) { cfg.CacheTTL = 30 * time.Millisecond; cfg.MaxStale = 250 * time.Millisecond })
	ctx := context.Background()
	if _, err := c.Get(ctx, EpSummary); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	f.srv.Close() // NEPSE goes away
	if _, err := c.Get(ctx, EpSummary); err != nil {
		t.Fatalf("a recent response is still served during a short outage: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := c.Get(ctx, EpSummary); apierrors.Normalize(err).Code != apierrors.CodeUpstreamUnavailable {
		t.Fatalf("after the stale window the outage must surface as 503-class, got %v", err)
	}
}
