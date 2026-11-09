package httpmw

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/observability"
)

const maxStackBytes = 4096

// Recover turns a panic into a logged 500 with the standard error body. If the response has already started, nothing more is written (an error body appended to a half-sent response would corrupt it); the panic is only logged.
func Recover(log *slog.Logger, respond ErrorWriter) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec) // net/http's own signal to abort the connection
				}
				stack := string(debug.Stack())
				if len(stack) > maxStackBytes {
					stack = stack[:maxStackBytes]
				}
				started := false
				if s, ok := w.(interface{ Started() bool }); ok {
					started = s.Started()
				}
				log.Error("panic recovered", "request_id", observability.RequestID(r.Context()), "panic_type", fmt.Sprintf("%T", rec),
					"response_started", started, "stack", stack)
				apiErr := apierrors.Internal(fmt.Errorf("panic in HTTP handler (%T)", rec))
				SetError(r.Context(), apiErr.Code, "panic recovered")
				if !started {
					respond(w, r, apiErr)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
