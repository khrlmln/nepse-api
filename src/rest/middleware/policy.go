package middleware

import (
	"net/http"
	"strings"

	"nepse-api/src/shared/httpmw"
)

// Re-exported so the REST server composes one middleware vocabulary.
type Middleware = httpmw.Middleware

// SecurityHeaders sets conservative headers for API responses. Paths in relaxed (the Swagger UI) keep a permissive policy because the UI needs scripts.
func SecurityHeaders(relaxedPrefixes ...string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Frame-Options", "DENY")
			relaxed := false
			for _, p := range relaxedPrefixes {
				p = strings.TrimSuffix(p, "/")
				if p != "" && (r.URL.Path == p || strings.HasPrefix(r.URL.Path, p+"/")) {
					relaxed = true
					break
				}
			}
			if !relaxed {
				h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
				h.Set("Cache-Control", "no-store")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS allows exactly the listed origins (no wildcard); with an empty list no cross-origin access is granted. A request from an unlisted origin is served without CORS headers (the browser enforces them); only its preflight is refused.
func CORS(allowed []string) Middleware {
	return httpmw.CORS(httpmw.CORSPolicy{
		Origins: allowed,
		Methods: []string{"GET", "HEAD", "OPTIONS"},
		Headers: []string{"Content-Type", "X-Request-ID"},
		Expose:  []string{"X-Request-ID", "Retry-After"},
	})
}
