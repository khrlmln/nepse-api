package httpmw

import (
	"context"
	"net/http"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

// DependencyCheckTimeout bounds the active NEPSE check behind /health/dependencies.
const DependencyCheckTimeout = 5 * time.Second

// HealthStatus is the body of the health endpoints.
type HealthStatus struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Health serves the probe endpoints of both services, so they answer identically. Its methods return handlers that report failures as errors; REST plugs them in as its handler type and MCP wraps them with HandleErr.
type Health struct {
	// Ping actively verifies the upstream authentication flow end to end.
	Ping func(context.Context) error
	// Drain reports that the process is shutting down (may be nil).
	Drain *Drain
}

// Live reports that this process is running and serving requests. It never calls NEPSE, so an upstream outage cannot cause an orchestrator to restart a healthy process.
func (h Health) Live() func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		WriteJSON(w, r, http.StatusOK, HealthStatus{Status: "ok"})
		return nil
	}
}

// Ready reports that this process can take traffic: it finished starting (assets loaded, WASM compiled) and is not draining for shutdown. It is local only, so NEPSE downtime never removes the service from rotation.
func (h Health) Ready() func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if h.Drain.Draining() {
			return apierrors.Unavailable("The service is shutting down.")
		}
		WriteJSON(w, r, http.StatusOK, HealthStatus{Status: "ok"})
		return nil
	}
}

// Dependencies actively checks that NEPSE is reachable and the authentication flow works. The check is uncached and time-bounded; it is rate limited like any other endpoint that causes upstream traffic.
func (h Health) Dependencies() func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx, cancel := context.WithTimeout(r.Context(), DependencyCheckTimeout)
		defer cancel()
		if err := h.Ping(ctx); err != nil {
			return err
		}
		WriteJSON(w, r, http.StatusOK, HealthStatus{Status: "ok", Checks: map[string]string{"nepse": "ok"}})
		return nil
	}
}

// HandleErr adapts an error-returning handler: a returned error becomes the canonical error response.
func HandleErr(h func(http.ResponseWriter, *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			WriteError(w, r, err)
		}
	})
}
