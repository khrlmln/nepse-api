package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersUseStrictDefaultsAndRelaxOnlyDocs(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := SecurityHeaders("/docs")(next)

	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api/v1/market/summary", nil))
	for name, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"Cache-Control":           "no-store",
	} {
		if got := apiResponse.Header().Get(name); got != want {
			t.Errorf("API header %s = %q, want %q", name, got, want)
		}
	}

	docsResponse := httptest.NewRecorder()
	handler.ServeHTTP(docsResponse, httptest.NewRequest(http.MethodGet, "/docs/", nil))
	for _, name := range []string{"X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options"} {
		if docsResponse.Header().Get(name) == "" {
			t.Errorf("Swagger UI is missing security header %s", name)
		}
	}
	if docsResponse.Header().Get("Content-Security-Policy") != "" || docsResponse.Header().Get("Cache-Control") != "" {
		t.Error("Swagger UI should use the relaxed CSP/cache policy")
	}

	lookalike := httptest.NewRecorder()
	handler.ServeHTTP(lookalike, httptest.NewRequest(http.MethodGet, "/docs-not-the-ui", nil))
	if lookalike.Header().Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" || lookalike.Header().Get("Cache-Control") != "no-store" {
		t.Error("lookalike paths must not inherit the Swagger UI's relaxed security policy")
	}
}

func TestCORSAllowlistAndPreflight(t *testing.T) {
	calls := 0
	handler := CORS([]string{"https://internal.example"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))

	allowed := httptest.NewRecorder()
	allowedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/market/summary", nil)
	allowedRequest.Header.Set("Origin", "https://internal.example")
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusNoContent || allowed.Header().Get("Access-Control-Allow-Origin") != "https://internal.example" {
		t.Fatalf("allowed origin response: status=%d headers=%v", allowed.Code, allowed.Header())
	}

	preflight := httptest.NewRecorder()
	preflightRequest := httptest.NewRequest(http.MethodOptions, "/api/v1/market/summary", nil)
	preflightRequest.Header.Set("Origin", "https://internal.example")
	handler.ServeHTTP(preflight, preflightRequest)
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatalf("allowed preflight response: status=%d headers=%v", preflight.Code, preflight.Header())
	}

	denied := httptest.NewRecorder()
	deniedRequest := httptest.NewRequest(http.MethodOptions, "/api/v1/market/summary", nil)
	deniedRequest.Header.Set("Origin", "https://untrusted.example")
	handler.ServeHTTP(denied, deniedRequest)
	if denied.Code != http.StatusForbidden {
		t.Errorf("disallowed preflight status = %d, want %d", denied.Code, http.StatusForbidden)
	}

	withoutOrigin := httptest.NewRecorder()
	handler.ServeHTTP(withoutOrigin, httptest.NewRequest(http.MethodGet, "/api/v1/market/summary", nil))
	if withoutOrigin.Code != http.StatusNoContent {
		t.Errorf("request without Origin status = %d", withoutOrigin.Code)
	}
	if calls != 2 {
		t.Errorf("downstream handler called %d times, want 2", calls)
	}
}
