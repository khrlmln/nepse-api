// Package middleware contains the REST transport plumbing: the error-returning handler type, the response envelope and REST-specific policies (CORS, headers). Transport-neutral middleware lives in shared/httpmw.
package middleware

import (
	"net/http"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/httpmw"
)

// HandlerFunc is a handler that returns an error instead of writing it, so every failure goes through one translation point (httpmw.WriteError).
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Envelope is the success body: {"data": ..., "meta": ...}.
type Envelope struct {
	Data any `json:"data"`
	Meta any `json:"meta,omitempty"`
}

// OK writes a 200 envelope.
func OK(w http.ResponseWriter, r *http.Request, data, meta any) {
	httpmw.WriteJSON(w, r, http.StatusOK, Envelope{Data: data, Meta: meta})
}

// Handle adapts a HandlerFunc for the given route template: it records the route, admits only GET and HEAD, and turns a returned error into the error response.
func Handle(pattern string, h HandlerFunc) http.Handler {
	return httpmw.Route(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			httpmw.WriteError(w, r, apierrors.New(apierrors.CodeMethodNotAllowed, "Method not allowed."))
			return
		}
		if err := h(w, r); err != nil {
			httpmw.WriteError(w, r, err)
		}
	}))
}

// NotFound is the catch-all handler for unknown paths.
func NotFound(w http.ResponseWriter, r *http.Request) {
	httpmw.WriteError(w, r, apierrors.NotFound("The requested resource does not exist."))
}
