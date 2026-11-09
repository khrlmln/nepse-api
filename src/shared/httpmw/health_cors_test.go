package httpmw

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "nepse-api/src/shared/errors"
)

func TestSharedHealthHandlers(t *testing.T) {
	drain := &Drain{}
	h := Health{Drain: drain, Ping: func(context.Context) error { return nil }}
	serve := func(fn func(http.ResponseWriter, *http.Request) error) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		HandleErr(fn).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec
	}
	if rec := serve(h.Live()); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("live: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(h.Ready()); rec.Code != 200 {
		t.Fatalf("ready: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(h.Dependencies()); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"nepse":"ok"`) {
		t.Fatalf("dependencies: %d %s", rec.Code, rec.Body)
	}
	drain.start()
	if rec := serve(h.Ready()); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "UNAVAILABLE") {
		t.Fatalf("draining ready: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(h.Live()); rec.Code != 200 {
		t.Fatalf("liveness must stay up while draining: %d", rec.Code)
	}
	h.Ping = func(context.Context) error { return apierrors.UpstreamUnavailable(errors.New("down")) }
	if rec := serve(h.Dependencies()); rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "down") {
		t.Fatalf("failing dependency: %d %s (cause must not leak)", rec.Code, rec.Body)
	}
}

func TestCORSPolicyModes(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	policy := CORSPolicy{
		Origins: []string{"https://ok.example"}, Methods: []string{"GET", "OPTIONS"},
		Headers: []string{"X-A", "X-B"}, Expose: []string{"X-Request-ID"},
	}
	do := func(p CORSPolicy, method, origin string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		CORS(p)(next).ServeHTTP(rec, r)
		return rec
	}

	pre := do(policy, http.MethodOptions, "https://ok.example")
	if pre.Code != 204 || pre.Header().Get("Access-Control-Allow-Origin") != "https://ok.example" ||
		pre.Header().Get("Access-Control-Allow-Methods") != "GET, OPTIONS" || pre.Header().Get("Access-Control-Allow-Headers") != "X-A, X-B" ||
		pre.Header().Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("allowed preflight: %d %v", pre.Code, pre.Header())
	}
	if got := do(policy, http.MethodGet, "https://ok.example").Header().Get("Access-Control-Expose-Headers"); got != "X-Request-ID" {
		t.Errorf("expose headers = %q", got)
	}
	if rec := do(policy, http.MethodGet, ""); rec.Code != 204 || rec.Header().Get("Vary") != "" {
		t.Errorf("no Origin: %d %v", rec.Code, rec.Header())
	}

	// Lenient mode: unlisted origins reach the handler (without CORS headers) except for preflights.
	if rec := do(policy, http.MethodGet, "https://evil.example"); rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("lenient GET: %d %v", rec.Code, rec.Header())
	}
	if rec := do(policy, http.MethodOptions, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("lenient preflight: %d", rec.Code)
	}
	// Strict mode: unlisted origins never reach the handler.
	policy.RejectUnlisted = true
	if rec := do(policy, http.MethodPost, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("strict POST: %d", rec.Code)
	}
	// No origins configured: nothing is allowed cross-origin.
	if rec := do(CORSPolicy{}, http.MethodOptions, "https://ok.example"); rec.Code != http.StatusForbidden {
		t.Errorf("empty allow-list: %d", rec.Code)
	}
}

func TestProbesAreSharedAndLimiterIsBounded(t *testing.T) {
	if len(LocalProbes) != 2 || LocalProbes[0] != "/health/live" || LocalProbes[1] != "/health/ready" {
		t.Fatalf("LocalProbes = %v", LocalProbes)
	}
	l := NewRateLimiter(1)
	if !l.Allow("a").Allowed || l.Allow("a").Allowed {
		t.Fatal("limiter must allow exactly one request per window")
	}
	if !NewRateLimiter(0).Allow("a").Allowed {
		t.Fatal("a zero limit disables the limiter")
	}
}
