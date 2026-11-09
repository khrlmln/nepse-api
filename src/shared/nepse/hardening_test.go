package nepse

import (
	"context"
	"testing"
	"time"
)

// HEADERS.json carries "TE: Trailers"; HTTP/2 permits only the lower-case token and strict servers answer 400 to anything else.
func TestTEHeaderIsSentLowerCaseForHTTP2(t *testing.T) {
	c := newFake(t).client(nil)
	req, err := c.newRequest(context.Background(), "GET", "/x", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Values("TE"); len(got) != 1 || got[0] != "trailers" {
		t.Fatalf("TE header = %q, want exactly \"trailers\"", got)
	}
}

// The payload identifier lookup must not hold a lock while it talks to NEPSE: a caller whose context ends has to return at once instead of queueing behind a slow load.
func TestPayloadSpecWaitIsCancellable(t *testing.T) {
	f := newFake(t)
	f.proveDelay.Store(int64(700 * time.Millisecond))
	f.proveStarted = make(chan struct{}, 1)
	c := f.client(nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.payloadSpec(context.Background()) // slow: authentication takes 700ms
	}()
	select {
	case <-f.proveStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("first caller never reached the delayed authentication request")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.payloadSpec(ctx); err == nil {
		t.Fatal("expected the cancelled caller to fail")
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("cancelled caller waited %v behind the in-flight load", elapsed)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("first caller did not finish")
	}
}

func TestPayloadSpecIsCachedForTheDay(t *testing.T) {
	f := newFake(t)
	c := f.client(func(cfg *Config) { cfg.CacheTTL = 0 })
	a, err := c.payloadSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := f.getCount.Load()
	b, err := c.payloadSpec(context.Background())
	if err != nil || a != b {
		t.Fatalf("second spec %+v err %v, want %+v", b, err, a)
	}
	if f.getCount.Load() != before {
		t.Fatal("the payload identifier was fetched again within the same Nepal day")
	}
}
