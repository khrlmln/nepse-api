// Package parallel runs a fixed set of independent calls concurrently.
package parallel

import (
	"context"
	"fmt"
	"sync"

	apierrors "nepse-api/src/shared/errors"
)

// Do runs every fn concurrently and waits for all of them. The first failure cancels the context the others receive and is the error returned (cancellations it caused in siblings are not reported instead of it). A panic in any fn is converted to an INTERNAL error rather than crashing the process, because these goroutines are not covered by the HTTP panic-recovery middleware.
func Do(ctx context.Context, fns ...func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	fail := func(err error) { once.Do(func() { firstErr = err; cancel() }) }

	for _, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					fail(apierrors.Internal(fmt.Errorf("panic in parallel call (%T)", r)))
				}
			}()
			if err := fn(ctx); err != nil {
				fail(err)
			}
		}()
	}
	wg.Wait()
	return firstErr
}
