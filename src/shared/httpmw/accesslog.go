package httpmw

import (
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/netutil"
	"nepse-api/src/shared/observability"
)

// AccessLog writes exactly one structured event per request, after it completes. Successful requests to the exact paths in quiet (health probes, polled every few seconds) are logged at DEBUG so they do not drown real traffic.
func AccessLog(log *slog.Logger, trusted []netip.Prefix, slow time.Duration, quiet []string) Middleware {
	quietPaths := newPathSet(quiet)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			elapsed := time.Since(start)

			route, errCode, errCause := "unmatched", apierrors.Code(""), ""
			if i := infoFrom(r.Context()); i != nil {
				var rt string
				rt, errCode, errCause = i.snapshot()
				if rt != "" {
					route = rt
				}
			}
			attrs := []any{
				"request_id", observability.RequestID(r.Context()),
				"method", r.Method,
				"route", observability.Sanitize(route),
				"path", observability.Sanitize(r.URL.Path),
				"query", observability.RedactQuery(r.URL.RawQuery),
				"status", rec.status,
				"duration_ms", float64(elapsed.Microseconds()) / 1000,
				"response_bytes", rec.bytes,
				"client_ip", netutil.ClientIP(r, trusted),
				"user_agent", observability.Sanitize(r.UserAgent()),
			}
			if errCode != "" {
				attrs = append(attrs, "error_code", string(errCode))
				if errCause != "" {
					attrs = append(attrs, "error_cause", errCause)
				}
			}
			if elapsed >= slow {
				attrs = append(attrs, "slow", true)
			}
			log.Log(r.Context(), accessLevel(rec.status, elapsed, slow, quietPaths.has(r.URL.Path)), "http request completed", attrs...)
		})
	}
}

// accessLevel maps an outcome to a log level: 5xx is an error, 429 and slow requests are warnings, expected 4xx stay informational, quiet probes are debug.
func accessLevel(status int, elapsed, slow time.Duration, quiet bool) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status == http.StatusTooManyRequests, elapsed >= slow:
		return slog.LevelWarn
	case quiet && status < 400:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}
