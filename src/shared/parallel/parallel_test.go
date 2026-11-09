package parallel

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

func TestDoRunsEverythingConcurrently(t *testing.T) {
	var running, peak atomic.Int32
	fn := func(context.Context) error {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		running.Add(-1)
		return nil
	}
	if err := Do(context.Background(), fn, fn, fn, fn); err != nil {
		t.Fatal(err)
	}
	if peak.Load() < 3 {
		t.Fatalf("calls did not overlap: peak=%d", peak.Load())
	}
	if err := Do(context.Background()); err != nil {
		t.Fatalf("no functions: %v", err)
	}
}

func TestFirstErrorWinsAndCancelsSiblings(t *testing.T) {
	boom := errors.New("boom")
	var siblingSawCancel atomic.Bool
	err := Do(context.Background(),
		func(context.Context) error { return boom },
		func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				siblingSawCancel.Store(true)
				return ctx.Err() // a collateral cancellation must NOT replace the real error
			case <-time.After(2 * time.Second):
				return nil
			}
		},
	)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the original failure", err)
	}
	if !siblingSawCancel.Load() {
		t.Fatal("siblings must be cancelled after the first failure")
	}
}

func TestPanicBecomesInternalErrorWithoutLoggingPanicPayload(t *testing.T) {
	err := Do(context.Background(), func(context.Context) error { panic("sensitive-token-value") })
	got := apierrors.Normalize(err)
	if got.Code != apierrors.CodeInternal {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), "sensitive-token-value") {
		t.Fatalf("panic payload leaked through the returned error: %v", err)
	}
	if !strings.Contains(err.Error(), "string") {
		t.Fatalf("panic type should remain diagnosable: %v", err)
	}
}

func TestParentCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Do(ctx, func(ctx context.Context) error { return ctx.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
