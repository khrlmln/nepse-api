package httpmw

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Drain signals that the process is shutting down. Readiness handlers consult it so a load balancer stops routing new requests before connections are closed.
type Drain struct{ draining atomic.Bool }

func (d *Drain) start()         { d.draining.Store(true) }
func (d *Drain) Draining() bool { return d != nil && d.draining.Load() }

// ServeOptions control how a server stops.
type ServeOptions struct {
	// ShutdownTimeout bounds how long in-flight requests may take to finish.
	ShutdownTimeout time.Duration
	// DrainDelay is how long the server keeps accepting requests after readiness
	// turned unready, giving load balancers time to notice. Zero = no delay.
	DrainDelay time.Duration
	// Drain is flipped when shutdown begins (may be nil).
	Drain *Drain
}

// Serve runs srv until ctx is canceled, then drains and shuts it down gracefully. A listen error is returned immediately; a clean shutdown returns nil.
func Serve(ctx context.Context, log *slog.Logger, name string, srv *http.Server, o ServeOptions) error {
	errCh := make(chan error, 1)
	go func() {
		log.Info(name+" listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info(name+" shutting down", "drain_delay", o.DrainDelay.String(), "shutdown_timeout", o.ShutdownTimeout.String())
	if o.Drain != nil {
		o.Drain.start()
	}
	if o.DrainDelay > 0 {
		time.Sleep(o.DrainDelay)
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), o.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		_ = srv.Close() // in-flight requests overran the budget: drop them rather than leak connections
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return <-errCh
}
