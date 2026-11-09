package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

func TestKeysStayBounded(t *testing.T) {
	l := New(5, time.Hour, 100)
	for i := 0; i < 5000; i++ {
		l.Allow(fmt.Sprintf("ip-%d", i))
		if len(l.entries) > 100 {
			t.Fatalf("tracked keys %d exceed the bound", len(l.entries))
		}
	}
}

func TestWindowResetsAndDisabled(t *testing.T) {
	l := New(1, time.Minute, 10)
	now := time.Now()
	l.now = func() time.Time { return now }
	if !l.Allow("a").Allowed || l.Allow("a").Allowed {
		t.Fatal("limit not enforced")
	}
	now = now.Add(61 * time.Second)
	if !l.Allow("a").Allowed {
		t.Fatal("window did not reset")
	}
	off := New(0, time.Minute, 10)
	for i := 0; i < 100; i++ {
		if !off.Allow("x").Allowed {
			t.Fatal("disabled limiter rejected")
		}
	}
}
