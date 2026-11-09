package httpmw

import (
	"context"
	"net/http"
	"net/netip"
	"time"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/netutil"
	"nepse-api/src/shared/ratelimit"
)

// RateLimit limits requests per client IP. Requests to the exact paths in exempt (local-only probes) are not limited; anything else, including endpoints that call upstream, is.
func RateLimit(l *ratelimit.Limiter, trusted []netip.Prefix, exempt []string, respond ErrorWriter) Middleware {
	skip := newPathSet(exempt)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip.has(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if res := l.Allow(netutil.ClientIP(r, trusted)); !res.Allowed {
				respond(w, r, apierrors.RateLimited(res.RetryAfter))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ConcurrencyLimit rejects (rather than queues) requests beyond max in flight with a clear, retryable overload error (RATE_LIMITED + Retry-After), not an upstream error. Exact paths in exempt (local probes) bypass the limit, so a saturated server still answers its health checks.
func ConcurrencyLimit(max int, exempt []string, respond ErrorWriter) Middleware {
	sem := make(chan struct{}, max)
	skip := newPathSet(exempt)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip.has(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				next.ServeHTTP(w, r)
			default:
				busy := apierrors.RateLimited(time.Second)
				busy.Message = "The server is busy handling other requests. Please retry shortly."
				respond(w, r, busy)
			}
		})
	}
}

// Timeout bounds the time a handler may spend, via the request context, so all upstream calls stop at the deadline and a clean error can still be written.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
