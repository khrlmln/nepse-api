package httpmw

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nepse-api/src/shared/observability"
	"nepse-api/src/shared/ratelimit"
)

func TestRecoverReturnsCanonicalErrorAndLogsOnce(t *testing.T) {
	var logs bytes.Buffer
	log, _ := observability.NewLogger(&logs, "info", "json", "t")
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom with secret-ish text") }),
		RequestID(), AccessLog(log, nil, time.Second, nil), Recover(log, WriteError))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	var body map[string]map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 500 || body["error"]["code"] != "INTERNAL" || strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if n := strings.Count(logs.String(), `"msg":"http request completed"`); n != 1 {
		t.Fatalf("access logs: %d\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), `"error_code":"INTERNAL"`) || !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Fatalf("panic not logged as ERROR with code:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "secret-ish text") || !strings.Contains(logs.String(), `"panic_type":"string"`) {
		t.Fatalf("panic logs must include type but not the panic payload:\n%s", logs.String())
	}
}

func TestPanicAfterPartialWriteDoesNotCorruptResponse(t *testing.T) {
	var logs bytes.Buffer
	log, _ := observability.NewLogger(&logs, "info", "json", "t")
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("partial"))
		panic("late failure")
	}), RequestID(), AccessLog(log, nil, time.Second, nil), Recover(log, WriteError))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Body.String() != "partial" || rec.Code != 200 {
		t.Fatalf("a second body was appended to a started response: %d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), `"response_started":true`) || strings.Count(logs.String(), `"msg":"http request completed"`) != 1 {
		t.Fatalf("logs:\n%s", logs.String())
	}
}

func TestRateLimitExemptionsAreExactPaths(t *testing.T) {
	l := ratelimit.New(1, time.Hour, 10)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }),
		RateLimit(l, nil, []string{"/health/ready"}, WriteError))
	do := func(path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Code
	}
	for i := 0; i < 5; i++ {
		if do("/health/ready") != 200 {
			t.Fatal("exempt path limited")
		}
	}
	if do("/health/dependencies") != 200 || do("/health/dependencies") != 429 {
		t.Fatal("non-exempt path under /health must be limited")
	}
	if do("/health/ready/extra") != 429 {
		t.Fatal("prefix match must not exempt other paths")
	}
}

func TestRequestIDStampedOnRequestHeader(t *testing.T) {
	var seen string
	h := Chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Get("X-Request-ID") }), RequestID())
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "bad id")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.HasPrefix(seen, "req_") {
		t.Fatalf("invalid inbound id must be replaced on the request header too: %q", seen)
	}
	if got := req.Header.Get("X-Request-ID"); got != "bad id" {
		t.Fatalf("the caller's own request must not be mutated: %q", got)
	}
}
