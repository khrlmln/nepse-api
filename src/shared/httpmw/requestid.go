package httpmw

import (
	"context"
	"net/http"

	"nepse-api/src/shared/observability"
)

// RequestID assigns each request an ID (a valid client-supplied X-Request-ID is reused for correlation), exposes it to handlers and returns it in the response.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if !observability.ValidRequestID(id) {
				id = observability.NewRequestID()
			}
			w.Header().Set("X-Request-ID", id)
			ctx := observability.WithRequestID(r.Context(), id)
			ctx = context.WithValue(ctx, infoKey{}, &info{})
			r = r.WithContext(ctx)
			// Stamp the canonical ID on the request too: transports that detach handlers from the HTTP context (the MCP SDK) still see it through the headers.
			r.Header = r.Header.Clone()
			r.Header.Set("X-Request-ID", id)
			next.ServeHTTP(w, r)
		})
	}
}
