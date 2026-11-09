package httpmw

import (
	"net/http"
	"strings"

	apierrors "nepse-api/src/shared/errors"
)

// CORSPolicy is a browser cross-origin policy that allows exactly the listed origins (never a wildcard). Requests without an Origin header (CLIs, servers, desktop apps) are unaffected.
type CORSPolicy struct {
	Origins []string
	Methods []string // Access-Control-Allow-Methods of preflight answers
	Headers []string // Access-Control-Allow-Headers of preflight answers
	Expose  []string // Access-Control-Expose-Headers of allowed responses
	// RejectUnlisted answers EVERY request from an unlisted origin with 403. When
	// false only preflights are refused and other requests are served without CORS
	// headers, leaving enforcement to the browser. Use true for services whose
	// handler a foreign web page must never reach.
	RejectUnlisted bool
}

// CORS applies the policy. With no origins configured no cross-origin access is granted.
func CORS(p CORSPolicy) Middleware {
	allowed := make(map[string]struct{}, len(p.Origins))
	for _, o := range p.Origins {
		allowed[o] = struct{}{}
	}
	methods, headers, expose := strings.Join(p.Methods, ", "), strings.Join(p.Headers, ", "), strings.Join(p.Expose, ", ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Add("Vary", "Origin")
			if _, ok := allowed[origin]; !ok {
				if p.RejectUnlisted || r.Method == http.MethodOptions {
					WriteError(w, r, apierrors.Forbidden("This origin is not allowed."))
					return
				}
				next.ServeHTTP(w, r) // the browser enforces the missing CORS headers
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", expose)
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", methods)
				h.Set("Access-Control-Allow-Headers", headers)
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
