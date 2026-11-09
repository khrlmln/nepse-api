package httpmw

import (
	"context"
	"net/http"
	"sync"

	apierrors "nepse-api/src/shared/errors"
)

// info carries per-request facts from handlers and error writers to the access log.
type info struct {
	mu        sync.Mutex
	route     string
	errorCode apierrors.Code
	cause     string
}

type infoKey struct{}

func infoFrom(ctx context.Context) *info {
	i, _ := ctx.Value(infoKey{}).(*info)
	return i
}

func (i *info) setRoute(route string) {
	i.mu.Lock()
	i.route = route
	i.mu.Unlock()
}

func (i *info) setError(code apierrors.Code, cause string) {
	i.mu.Lock()
	i.errorCode, i.cause = code, cause
	i.mu.Unlock()
}

// snapshot returns the recorded route and error.
func (i *info) snapshot() (route string, code apierrors.Code, cause string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.route, i.errorCode, i.cause
}

// SetError records the error outcome of the current request for the access log.
func SetError(ctx context.Context, code apierrors.Code, cause string) {
	if i := infoFrom(ctx); i != nil {
		i.setError(code, cause)
	}
}

// Route records the matched route template (low-cardinality, e.g. "/api/v1/securities/{symbol}") for the access log. Request copies made by other middleware hide the mux's own r.Pattern from outer layers, so it is passed through the shared per-request info instead.
func Route(pattern string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if i := infoFrom(r.Context()); i != nil {
			i.setRoute(pattern)
		}
		next.ServeHTTP(w, r)
	})
}
