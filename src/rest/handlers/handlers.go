// Package handlers adapts the shared use cases to HTTP. Handlers parse and validate transport-level input, call exactly one use case, and write an envelope; business logic lives in the shared packages.
package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"nepse-api/src/rest/middleware"
	apierrors "nepse-api/src/shared/errors"
)

// noInput adapts a use case that takes no parameters.
func noInput[T any](fn func(context.Context) (T, error)) middleware.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		v, err := fn(r.Context())
		if err != nil {
			return err
		}
		middleware.OK(w, r, v, nil)
		return nil
	}
}

// byParam adapts a use case that takes one path parameter (e.g. "symbol").
func byParam[T any](param string, fn func(context.Context, string) (T, error)) middleware.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		v, err := fn(r.Context(), r.PathValue(param))
		if err != nil {
			return err
		}
		middleware.OK(w, r, v, nil)
		return nil
	}
}

// bySymbol is byParam for the {symbol} path parameter.
func bySymbol[T any](fn func(context.Context, string) (T, error)) middleware.HandlerFunc {
	return byParam("symbol", fn)
}

// intParam parses an optional integer query parameter (absent => 0).
func intParam(r *http.Request, name string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apierrors.InvalidArgument(name + " must be an integer.")
	}
	return n, nil
}
