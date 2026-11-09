package httpmw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/observability"
)

func logger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	l, err := observability.NewLogger(&buf, "debug", "json", "t")
	if err != nil {
		t.Fatal(err)
	}
	return l, &buf
}

func records(t *testing.T, buf *bytes.Buffer) []map[string]any {
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON: %q", line)
		}
		out = append(out, m)
	}
	return out
}

func TestAccessLevelPolicy(t *testing.T) {
	slow := time.Second
	cases := []struct {
		status  int
		elapsed time.Duration
		quiet   bool
		want    slog.Level
	}{
		{200, time.Millisecond, false, slog.LevelInfo},
		{404, time.Millisecond, false, slog.LevelInfo}, // expected client errors are not alarms
		{401, time.Millisecond, false, slog.LevelInfo},
		{429, time.Millisecond, false, slog.LevelWarn},
		{500, time.Millisecond, false, slog.LevelError},
		{503, 5 * time.Second, false, slog.LevelError},
		{200, 2 * time.Second, false, slog.LevelWarn},  // slow
		{200, time.Millisecond, true, slog.LevelDebug}, // quiet probe, healthy
		{503, time.Millisecond, true, slog.LevelError}, // quiet probe, failing: never hidden
		{200, 2 * time.Second, true, slog.LevelWarn},   // a slow probe is still worth seeing
	}
	for _, c := range cases {
		if got := accessLevel(c.status, c.elapsed, slow, c.quiet); got != c.want {
			t.Errorf("status=%d elapsed=%v quiet=%v -> %v, want %v", c.status, c.elapsed, c.quiet, got, c.want)
		}
	}
}

func TestAccessLogFieldsRouteAndQuietProbes(t *testing.T) {
	log, buf := logger(t)
	mux := http.NewServeMux()
	mux.Handle("/health/ready", Route("/health/ready", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })))
	mux.Handle("/items/", Route("/items/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetError(r.Context(), apierrors.CodeNotFound, "cause here")
		w.WriteHeader(404)
		_, _ = w.Write([]byte("nope"))
	})))
	h := Chain(mux, RequestID(), AccessLog(log, nil, time.Hour, []string{"/health/ready"}))

	req := httptest.NewRequest("GET", "/items/42?token=s3cret&page=1", nil)
	req.Header.Set("User-Agent", "agent\nwith newline")
	req.Header.Set("X-Request-ID", "rid-1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/health/ready", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/unknown", nil))

	recs := records(t, buf)
	if len(recs) != 3 {
		t.Fatalf("one event per request, got %d:\n%s", len(recs), buf.String())
	}
	item := recs[0]
	if item["msg"] != "http request completed" || item["request_id"] != "rid-1" || item["route"] != "/items/{id}" || item["path"] != "/items/42" ||
		item["status"] != float64(404) || item["response_bytes"] != float64(4) || item["error_code"] != "NOT_FOUND" ||
		item["error_cause"] != "cause here" || item["level"] != "INFO" || item["method"] != "GET" || item["client_ip"] == "" {
		t.Fatalf("%v", item)
	}
	if strings.Contains(buf.String(), "s3cret") || strings.Contains(item["user_agent"].(string), "\n") {
		t.Fatalf("secret/newline leaked into the log: %v", item)
	}
	if recs[1]["level"] != "DEBUG" || recs[1]["route"] != "/health/ready" {
		t.Fatalf("healthy probe must be quiet: %v", recs[1])
	}
	if recs[2]["route"] != "unmatched" {
		t.Fatalf("%v", recs[2])
	}
}

func TestSlowRequestsAreFlagged(t *testing.T) {
	log, buf := logger(t)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { time.Sleep(30 * time.Millisecond) }), RequestID(), AccessLog(log, nil, 10*time.Millisecond, nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	rec := records(t, buf)[0]
	if rec["slow"] != true || rec["level"] != "WARN" {
		t.Fatalf("%v", rec)
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	onFlush func()
}

func (f *flushRecorder) Flush() { f.onFlush(); f.ResponseRecorder.Flush() }

func TestStatusRecorderIsTransparent(t *testing.T) {
	flushed := false
	inner := &flushRecorder{ResponseRecorder: httptest.NewRecorder(), onFlush: func() { flushed = true }}
	rec := &statusRecorder{ResponseWriter: inner, status: 200}
	if rec.Started() {
		t.Fatal("not started yet")
	}
	rec.Flush() // streaming handlers (SSE) must be able to flush through the chain
	if !flushed || !rec.Started() || rec.status != 200 {
		t.Fatalf("flush not forwarded: flushed=%v started=%v", flushed, rec.Started())
	}
	if rec.Unwrap() != inner {
		t.Fatal("Unwrap must expose the underlying writer (http.ResponseController)")
	}
	if _, _, err := rec.Hijack(); err == nil {
		t.Fatal("hijacking an unsupporting writer must error, not panic")
	}
	w2 := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	w2.WriteHeader(201)
	w2.WriteHeader(500) // a second WriteHeader must not change the recorded status
	_, _ = w2.Write([]byte("abc"))
	if w2.status != 201 || w2.bytes != 3 {
		t.Fatalf("status=%d bytes=%d", w2.status, w2.bytes)
	}
}

func TestWriteErrorEnvelopeAndHeaderHygiene(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Length", "9999") // a handler set this for a body it never wrote
	rec.Header().Set("Content-Encoding", "gzip")
	req := httptest.NewRequest("GET", "/", nil).WithContext(observability.WithRequestID(context.Background(), "rid-9"))
	WriteError(rec, req, apierrors.RateLimited(2500*time.Millisecond).WithDetails(map[string]string{"hint": "slow down"}))
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "3" || rec.Header().Get("Content-Length") != "" || rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != apierrors.CodeRateLimited || body.Error.RequestID != "rid-9" || body.Error.Details == nil {
		t.Fatalf("%s %v", rec.Body.String(), err)
	}
	rec = httptest.NewRecorder() // unknown errors never leak their text
	WriteError(rec, req, errors.New("secret connection string"))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder() // Retry-After is always at least 1 second when present
	WriteError(rec, req, apierrors.RateLimited(0))
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("%q", rec.Header().Get("Retry-After"))
	}
}

func TestWriteJSONFailsCleanlyForUnencodableValues(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, httptest.NewRequest("GET", "/", nil), 200, map[string]any{"bad": make(chan int)})
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"INTERNAL"`) {
		t.Fatalf("an unencodable value must become a clean 500, not a truncated 200: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	WriteJSON(rec, httptest.NewRequest("GET", "/", nil), 201, map[string]int{"a": 1})
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
		rec.Header().Get("Content-Length") != strconv.Itoa(rec.Body.Len()) || !strings.HasSuffix(rec.Body.String(), "\n") {
		t.Fatalf("%v %q", rec.Header(), rec.Body.String())
	}
}

func TestConcurrencyLimitAndExemptions(t *testing.T) {
	release := make(chan struct{})
	var inFlight sync.WaitGroup
	inFlight.Add(1)
	started := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-release
		}
		w.WriteHeader(200)
	})
	h := ConcurrencyLimit(1, []string{"/health/ready"}, WriteError)(slow)
	go func() {
		defer inFlight.Done()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/slow", nil))
	}()
	<-started
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/other", nil))
	var body ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 429 || body.Error.Code != apierrors.CodeRateLimited || !strings.Contains(body.Error.Message, "busy") || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("overload must be a clear, retryable local error: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/health/ready", nil))
	if rec.Code != 200 {
		t.Fatalf("exempt probes must pass while saturated: %d", rec.Code)
	}
	close(release)
	inFlight.Wait()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/other", nil))
	if rec.Code != 200 {
		t.Fatalf("the slot must be released afterwards: %d", rec.Code)
	}
}

func TestTimeoutSetsADeadline(t *testing.T) {
	var deadline time.Time
	var has bool
	h := Timeout(2 * time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { deadline, has = r.Context().Deadline() }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !has || time.Until(deadline) > 2*time.Second || time.Until(deadline) < time.Second {
		t.Fatalf("%v %v", has, deadline)
	}
}

func TestChainOrderFirstIsOutermost(t *testing.T) {
	var order []string
	mk := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+">")
				next.ServeHTTP(w, r)
				order = append(order, "<"+name)
			})
		}
	}
	Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }), mk("a"), mk("b")).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if strings.Join(order, " ") != "a> b> handler <b <a" {
		t.Fatalf("%v", order)
	}
}

func TestRecoverRepanicsAbortHandler(t *testing.T) {
	log, _ := logger(t)
	h := Recover(log, WriteError)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Fatalf("http.ErrAbortHandler must propagate so net/http aborts the connection, got %v", r)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestServeDrainsThenShutsDownGracefully(t *testing.T) {
	log, buf := logger(t)
	addr := freeAddr(t)
	drain := &Drain{}
	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		if drain.Draining() {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	})
	finish := make(chan struct{})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) { <-finish; _, _ = w.Write([]byte("done")) })
	srv := &http.Server{Addr: addr, Handler: mux}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, log, "test server", srv, ServeOptions{ShutdownTimeout: 5 * time.Second, DrainDelay: 400 * time.Millisecond, Drain: drain})
	}()
	status := func(path string) int {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			return -1
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for i := 0; i < 50 && status("/ready") != 200; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	slowBody := make(chan string, 1) // an in-flight request must survive the shutdown
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err != nil {
			slowBody <- "ERR " + err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		slowBody <- string(b)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel() // SIGTERM equivalent
	time.Sleep(100 * time.Millisecond)
	if got := status("/ready"); got != 503 {
		t.Fatalf("readiness must turn 503 as soon as shutdown begins, got %d", got)
	}
	select {
	case err := <-done:
		t.Fatalf("server stopped before the drain delay elapsed: %v", err)
	default:
	}
	time.Sleep(450 * time.Millisecond) // the drain delay passes; Shutdown now waits for /slow
	close(finish)
	if body := <-slowBody; body != "done" {
		t.Fatalf("in-flight request was cut off: %q", body)
	}
	if err := <-done; err != nil {
		t.Fatalf("clean shutdown must return nil: %v", err)
	}
	if status("/ready") != -1 {
		t.Fatal("the listener must be closed after shutdown")
	}
	if out := buf.String(); !strings.Contains(out, "test server listening") || !strings.Contains(out, "test server shutting down") {
		t.Fatalf("lifecycle not logged:\n%s", out)
	}
}

func TestServeReturnsListenErrors(t *testing.T) {
	log, _ := logger(t)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	srv := &http.Server{Addr: l.Addr().String(), Handler: http.NewServeMux()} // port already taken
	if err := Serve(context.Background(), log, "x", srv, ServeOptions{ShutdownTimeout: time.Second}); err == nil {
		t.Fatal("a failed bind must be returned immediately")
	}
}

func TestDrainNilSafe(t *testing.T) {
	var d *Drain
	if d.Draining() {
		t.Fatal("a nil Drain is never draining")
	}
}
