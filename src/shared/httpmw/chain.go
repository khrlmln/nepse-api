// Package httpmw holds the transport-neutral HTTP plumbing shared by the REST and MCP services: request IDs, access logging, panic recovery, rate and concurrency limits, timeouts, canonical error responses and the server lifecycle. Services plug in their own error writers where a response must be produced.
package httpmw

import (
	"net/http"
	"time"

	"nepse-api/src/shared/ratelimit"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares so that the first one listed is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// ErrorWriter writes the transport's error response for err.
type ErrorWriter func(w http.ResponseWriter, r *http.Request, err error)

// pathSet is a set of exact request paths.
type pathSet map[string]struct{}

func newPathSet(paths []string) pathSet {
	s := make(pathSet, len(paths))
	for _, p := range paths {
		s[p] = struct{}{}
	}
	return s
}

func (s pathSet) has(path string) bool { _, ok := s[path]; return ok }

// LocalProbes are the endpoints that never call NEPSE: they bypass the rate and concurrency limits and are logged quietly, so probes keep working under load.
var LocalProbes = []string{"/health/live", "/health/ready"}

const (
	rateWindow  = time.Minute
	maxRateKeys = 50000
)

// NewRateLimiter is the per-client-IP limiter both services use: perMinute requests per fixed one-minute window (0 disables it) and a hard bound on the number of tracked clients.
func NewRateLimiter(perMinute int) *ratelimit.Limiter {
	return ratelimit.New(perMinute, rateWindow, maxRateKeys)
}
