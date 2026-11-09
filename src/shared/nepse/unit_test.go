package nepse

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

func TestValidateBaseURL(t *testing.T) {
	for _, u := range []string{"https://www.nepalstock.com", "https://www.nepalstock.com/", "https://host:8443", "https://127.0.0.1"} {
		if err := ValidateBaseURL(u, false); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
	for _, u := range []string{"", "www.nepalstock.com", "https://", "https://u:p@host", "https://host/path", "https://host?x=1", "https://host#f", "https://host?", "ftp://host", "://"} {
		if err := ValidateBaseURL(u, true); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
	if err := ValidateBaseURL("http://localhost:1234", false); err == nil {
		t.Fatal("plain http without the explicit opt-in accepted")
	}
	if err := ValidateBaseURL("http://localhost:1234", true); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBaseURL("https://admin:hunter2@host", false); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("credentials must never be echoed: %v", err)
	}
}

func TestStatusErrorMapping(t *testing.T) {
	mk := func(status int, retryAfter string) *http.Response {
		h := http.Header{}
		if retryAfter != "" {
			h.Set("Retry-After", retryAfter)
		}
		return &http.Response{StatusCode: status, Header: h}
	}
	cases := []struct {
		status     int
		retryAfter string
		code       apierrors.Code
		retryable  bool
		after      time.Duration
	}{
		{401, "", apierrors.CodeUpstreamAuthFailed, true, 0},
		{400, "", apierrors.CodeUpstreamInvalidResp, false, 0},
		{404, "", apierrors.CodeUpstreamInvalidResp, false, 0},
		{403, "", apierrors.CodeUpstreamInvalidResp, false, 0},
		{429, "7", apierrors.CodeUpstreamUnavailable, true, 7 * time.Second},
		{500, "", apierrors.CodeUpstreamUnavailable, true, 0},
		{502, "", apierrors.CodeUpstreamUnavailable, true, 0},
		{503, "3", apierrors.CodeUpstreamUnavailable, true, 3 * time.Second},
		{503, "999999", apierrors.CodeUpstreamUnavailable, true, maxRetryAfter}, // hostile header is capped
		{503, "-5", apierrors.CodeUpstreamUnavailable, true, 0},
		{503, "soon", apierrors.CodeUpstreamUnavailable, true, 0},
	}
	for _, c := range cases {
		err := apierrors.Normalize(statusError(mk(c.status, c.retryAfter)))
		if err.Code != c.code || err.Retryable != c.retryable || err.RetryAfter != c.after {
			t.Errorf("status %d retry-after %q: got code=%s retryable=%v after=%v", c.status, c.retryAfter, err.Code, err.Retryable, err.RetryAfter)
		}
		if strings.Contains(err.Message, fmt.Sprint(c.status)) {
			t.Errorf("status leaked into the client message: %q", err.Message)
		}
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(future); d < 80*time.Second || d > 91*time.Second {
		t.Fatalf("http-date: %v", d)
	}
	if d := parseRetryAfter(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)); d != 0 {
		t.Fatalf("a date in the past means no delay: %v", d)
	}
	for _, v := range []string{"", "  ", "garbage"} {
		if parseRetryAfter(v) != 0 {
			t.Errorf("%q", v)
		}
	}
	for _, value := range []string{"999999999999999999999999999999999999999", "9223372036854775807"} {
		if got := parseRetryAfter(value); got != maxRetryAfter {
			t.Errorf("large Retry-After %q = %s, want cap %s", value, got, maxRetryAfter)
		}
	}
}

func TestRetryPolicy(t *testing.T) {
	if !shouldRetry(apierrors.UpstreamUnavailable(errors.New("x"))) || !shouldRetry(apierrors.DeadlineExceeded(errors.New("x"))) {
		t.Fatal("transient failures must be retried")
	}
	for _, err := range []error{apierrors.UpstreamAuthFailed(errors.New("x")), apierrors.UpstreamInvalidResponse(nil, ""), apierrors.NotFound("x"),
		errors.New("plain"), context.Canceled} {
		if shouldRetry(err) {
			t.Errorf("must not retry %v", err)
		}
	}
	if d0, d1, d2 := retryDelay(0, nil), retryDelay(1, nil), retryDelay(2, nil); d0 != baseRetryDelay || d1 != 2*baseRetryDelay || d2 != 4*baseRetryDelay {
		t.Fatalf("exponential back-off: %v %v %v", d0, d1, d2)
	}
	if d := retryDelay(10, nil); d != maxRetryDelay {
		t.Fatalf("delay must be capped: %v", d)
	}
	if d := retryDelay(0, apierrors.UpstreamUnavailable(nil).AsRetryable(3*time.Second)); d != 3*time.Second {
		t.Fatalf("Retry-After must be honoured: %v", d)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(canceled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep must stop on cancel: %v", err)
	}
}

func TestCertificateErrorsAreNotRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()
	c, err := NewClient(Config{BaseURL: srv.URL, Timeout: 5 * time.Second}) // system roots do not trust httptest's CA
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	start := time.Now()
	_, err = c.Get(context.Background(), EpSummary)
	got := apierrors.Normalize(err)
	if got.Code != apierrors.CodeUpstreamUnavailable || got.Retryable {
		t.Fatalf("certificate failures are permanent until fixed: %+v", got)
	}
	if elapsed := time.Since(start); elapsed > baseRetryDelay {
		t.Fatalf("a certificate error was retried with back-off (%v)", elapsed)
	}
	if hits.Load() != 0 {
		t.Fatal("no request should reach a server whose certificate is not trusted")
	}
	var certErr *tls.CertificateVerificationError
	if !errors.As(err, &certErr) {
		t.Fatalf("the cause must stay inspectable in logs: %v", err)
	}
}

func TestNumberToIntAndShapes(t *testing.T) {
	for in, want := range map[any]int{json.Number("7"): 7, 7.0: 7, 7: 7} {
		if got, ok := numberToInt(in); !ok || got != want {
			t.Errorf("%v", in)
		}
	}
	for _, in := range []any{json.Number("7.5"), json.Number("abc"), 7.5, "7", nil, []any{}} {
		if _, ok := numberToInt(in); ok {
			t.Errorf("%v accepted", in)
		}
	}
	arr, obj := []any{map[string]any{"a": 1}}, map[string]any{"a": 1}
	checks := []struct {
		kind shapeKind
		v    any
		ok   bool
	}{
		{shapeAny, "anything", true}, {shapeArray, arr, true}, {shapeArray, obj, false}, {shapeObjects, arr, true},
		{shapeObjects, []any{1}, false}, {shapeObjects, obj, false}, {shapeObject, obj, true}, {shapeObject, arr, false},
		{shapeCollection, arr, true}, {shapeCollection, obj, true}, {shapeCollection, "s", false}, {shapeCollection, nil, false},
	}
	for i, c := range checks {
		err := checkShape(c.v, c.kind, "x")
		if (err == nil) != c.ok {
			t.Errorf("#%d kind=%d: err=%v", i, c.kind, err)
		}
		if err != nil && apierrors.Normalize(err).Code != apierrors.CodeUpstreamInvalidResp {
			t.Errorf("#%d wrong code", i)
		}
	}
	if Str(map[string]any{"a": "x", "b": 1, "c": nil}, "a") != "x" || Str(map[string]any{"b": 1}, "b") != "" || Str(nil, "z") != "" {
		t.Fatal("Str must return strings only")
	}
}

func TestEmbeddedAssetsAreComplete(t *testing.T) {
	a, err := loadAssets()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.payloadSeeds) != 100 || a.headers["Accept"] == "" {
		t.Fatalf("payload_seeds=%d headers=%v", len(a.payloadSeeds), a.headers)
	}
	for name, ep := range IndexGraphs {
		if !strings.HasPrefix(a.endpoints[string(ep)], "/api/nots/graph/index/") {
			t.Errorf("%s -> %s", name, a.endpoints[string(ep)])
		}
	}
	seen := map[string]string{} // every graph endpoint is exposed under exactly one public name
	for name, ep := range IndexGraphs {
		seen[string(ep)] = name
	}
	for name, ep := range SubIndexGraphs {
		if prev, dup := seen[string(ep)]; dup {
			t.Errorf("%s and %s share endpoint %s", name, prev, ep)
		}
		seen[string(ep)] = name
	}
	if len(IndexGraphs) != 4 || len(SubIndexGraphs) != 13 {
		t.Fatalf("graph tables: %d + %d", len(IndexGraphs), len(SubIndexGraphs))
	}
}

func TestPayloadIDRejectsBadInput(t *testing.T) {
	for _, day := range []int{0, 32} {
		if _, err := computePayloadID(1, 1, day, [5]int{}, modeScrip); err == nil {
			t.Fatalf("day %d accepted", day)
		}
	}
	if _, err := computePayloadID(1, 1, 5, [5]int{}, payloadMode(99)); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if id, err := computePayloadID(10, 3, 5, [5]int{9, 9, 9, 9, 9}, modeScrip); err != nil || id != 23 {
		t.Fatalf("scrip payload ignores salts: %d %v", id, err)
	}
}

func TestDecodeJSONStrictness(t *testing.T) {
	if _, err := decodeJSON([]byte(`{"a":1} {"b":2}`)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := decodeJSON([]byte(``)); err == nil {
		t.Fatal("empty body accepted")
	}
	v, err := decodeJSON([]byte(`{"big":20260000000000001,"f":1.50}`))
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := json.Marshal(v); !strings.Contains(string(out), "20260000000000001") || !strings.Contains(string(out), "1.50") {
		t.Fatalf("numbers must pass through exactly: %s", out)
	}
}

func TestFloorsheetHelpers(t *testing.T) {
	for in, want := range map[string]bool{"[]": true, " {} ": true, "null": true, "\n[]\n": true, `{"floorsheets":{}}`: false, "[1]": false, "": false} {
		if got := isEmptySheet([]byte(in)); got != want {
			t.Errorf("%q -> %v", in, got)
		}
	}
	if p := emptyFloorPage(3, true); p.TotalPages != 0 || p.TotalElements != 0 || p.Rows == nil || len(p.Rows) != 0 || p.Page != 3 {
		t.Fatalf("%+v", p)
	}
	if p := emptyFloorPage(3, false); p.TotalPages != -1 || p.TotalElements != -1 {
		t.Fatalf("%+v", p)
	}
	good, err := parseFloorEnvelope([]byte(`{"floorsheets":{"content":[{"a":1}],"totalPages":4,"totalElements":2001}}`), 2)
	if err != nil || len(good.Rows) != 1 || good.TotalPages != 4 || good.TotalElements != 2001 || good.Page != 2 {
		t.Fatalf("%+v %v", good, err)
	}
	noTotals, err := parseFloorEnvelope([]byte(`{"floorsheets":{"content":[]}}`), 0)
	if err != nil || noTotals.TotalPages != -1 || noTotals.TotalElements != -1 || noTotals.Rows == nil {
		t.Fatalf("%+v %v", noTotals, err)
	}
	for _, bad := range []string{`not json`, `{"floorsheets":null}`, `{"floorsheets":{}}`, `{"floorsheets":{"content":[],"totalPages":-1}}`,
		`{"floorsheets":{"content":[],"totalPages":"x"}}`, `{"floorsheets":{"content":[],"totalPages":1.5}}`} {
		if _, err := parseFloorEnvelope([]byte(bad), 0); apierrors.Normalize(err).Code != apierrors.CodeUpstreamInvalidResp {
			t.Errorf("%s: %v", bad, err)
		}
	}
	// totalElements is informational: unusable values are omitted, never fatal
	for _, te := range []string{`"x"`, `-4`, `1.5`, `null`, `[]`} {
		p, err := parseFloorEnvelope([]byte(`{"floorsheets":{"content":[],"totalPages":1,"totalElements":`+te+`}}`), 0)
		if err != nil || p.TotalElements != -1 || p.TotalPages != 1 {
			t.Errorf("totalElements=%s: %+v %v", te, p, err)
		}
	}
	// numbers NEPSE sends as quoted strings are accepted
	if p, err := parseFloorEnvelope([]byte(`{"floorsheets":{"content":[],"totalPages":"3","totalElements":"1200"}}`), 0); err != nil || p.TotalPages != 3 || p.TotalElements != 1200 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestAuthFailuresAreClassified(t *testing.T) {
	long := strings.Repeat("A", 200)
	for name, body := range map[string]string{
		"not json":         `not json`,
		"missing tokens":   `{"serverTime":1}`,
		"token too short":  `{"accessToken":"abc","refreshToken":"abc","salt1":1,"salt2":2,"salt3":3,"salt4":4,"salt5":5}`,
		"salt overflow":    `{"accessToken":"` + long + `","refreshToken":"` + long + `","salt1":99999999999,"salt2":2,"salt3":3,"salt4":4,"salt5":5}`,
		"wrong field type": `{"accessToken":5,"refreshToken":6}`,
	} {
		f := newFake(t)
		f.proveBody.Store(body)
		c := f.client(nil)
		if _, err := c.Get(context.Background(), EpSummary); apierrors.Normalize(err).Code != apierrors.CodeUpstreamAuthFailed {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestInvalidateOnlyDropsTheRejectedToken(t *testing.T) {
	f := newFake(t)
	c := f.client(nil)
	st, err := c.token.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.token.invalidate("some-other-token") // another goroutine already refreshed past it
	if again, _ := c.token.snapshot(context.Background()); again.access != st.access || f.prove.Load() != 1 {
		t.Fatal("a stale invalidation must not discard a fresher token")
	}
	c.token.invalidate(st.access)
	if _, err := c.token.snapshot(context.Background()); err != nil || f.prove.Load() != 2 {
		t.Fatalf("rejected token must be refreshed: prove=%d err=%v", f.prove.Load(), err)
	}
}

func TestSecurityLookupEdgeCases(t *testing.T) {
	f := newFake(t)
	c := f.client(nil)
	ctx := context.Background()
	if _, err := c.SecurityID(ctx, "NOPE"); apierrors.Normalize(err).Code != apierrors.CodeNotFound {
		t.Fatal(err)
	}
	before := f.getCount.Load()
	for i := 0; i < 5; i++ { // remembered misses must not trigger new upstream calls
		if _, err := c.SecurityID(ctx, "NOPE"); apierrors.Normalize(err).Code != apierrors.CodeNotFound {
			t.Fatal(err)
		}
	}
	if f.getCount.Load() != before {
		t.Fatalf("negative cache ignored (%d new upstream calls)", f.getCount.Load()-before)
	}
	c.securityMu.Lock() // an expired miss is looked up again (the list is still cached) and re-remembered
	c.securityMiss["NOPE"] = time.Now().Add(-securityMissTTL - time.Second)
	c.securityMu.Unlock()
	if _, err := c.SecurityID(ctx, "NOPE"); apierrors.Normalize(err).Code != apierrors.CodeNotFound {
		t.Fatal(err)
	}
	if _, _, missed := c.lookup("NOPE"); !missed {
		t.Fatal("the miss must be remembered again after re-checking")
	}
	if id, known, missed := c.lookup("NABIL"); !known || id != 101 || missed {
		t.Fatalf("%d %v %v", id, known, missed)
	}
	if _, known, missed := c.lookup("UNSEEN"); known || missed {
		t.Fatal("never-seen symbols are neither known nor recently missed")
	}
}

func TestListAccessorsAndPing(t *testing.T) {
	f := newFake(t)
	c := f.client(nil)
	if _, err := c.CompanyList(context.Background()); err != nil {
		t.Fatalf("empty array is a valid company list: %v", err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}
