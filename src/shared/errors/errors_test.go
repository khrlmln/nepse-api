package apierrors

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestStatusForEveryCode(t *testing.T) {
	want := map[Code]int{
		CodeInvalidArgument: 400, CodeMethodNotAllowed: 405, CodeForbidden: 403, CodeNotFound: 404,
		CodeRateLimited: 429, CodeUpstreamAuthFailed: 502, CodeUpstreamInvalidResp: 502, CodeUpstreamUnavailable: 503,
		CodeUnavailable: 503, CodeDeadlineExceeded: 504, CodeCanceled: StatusClientClosedRequest, CodeInternal: 500, Code("SOMETHING_NEW"): 500,
	}
	for code, status := range want {
		if got := StatusFor(code); got != status {
			t.Errorf("%s -> %d, want %d", code, got, status)
		}
	}
	if e := (&ApiError{Code: CodeNotFound, HTTPStatus: http.StatusGone}); e.Status() != 410 {
		t.Fatal("explicit HTTPStatus must win")
	}
}

func TestConstructorsSetCodeMessageAndRetryability(t *testing.T) {
	cause := errors.New("root cause")
	cases := []struct {
		err       *ApiError
		code      Code
		retryable bool
	}{
		{InvalidArgument("x"), CodeInvalidArgument, false},
		{Forbidden("x"), CodeForbidden, false},
		{NotFound("x"), CodeNotFound, false},
		{RateLimited(2 * time.Second), CodeRateLimited, true},
		{UpstreamAuthFailed(cause), CodeUpstreamAuthFailed, true},
		{UpstreamInvalidResponse(cause, ""), CodeUpstreamInvalidResp, false},
		{UpstreamUnavailable(cause), CodeUpstreamUnavailable, true},
		{DeadlineExceeded(cause), CodeDeadlineExceeded, true},
		{Unavailable("shutting down"), CodeUnavailable, true},
		{Internal(cause), CodeInternal, false},
	}
	for _, c := range cases {
		if c.err.Code != c.code || c.err.Retryable != c.retryable || c.err.Message == "" {
			t.Errorf("%+v", c.err)
		}
	}
	if got := UpstreamInvalidResponse(nil, "").Message; got == "" {
		t.Fatal("default message missing")
	}
	if got := UpstreamInvalidResponse(nil, "custom").Message; got != "custom" {
		t.Fatalf("custom message lost: %q", got)
	}
	if e := RateLimited(3 * time.Second); e.RetryAfter != 3*time.Second {
		t.Fatalf("%v", e.RetryAfter)
	}
}

func TestErrorStringUnwrapAndIs(t *testing.T) {
	cause := errors.New("db down")
	e := Wrap(cause, CodeInternal, "An internal error occurred.")
	if got := e.Error(); got != "INTERNAL: An internal error occurred.: db down" {
		t.Fatalf("%q", got)
	}
	if got := New(CodeNotFound, "nope").Error(); got != "NOT_FOUND: nope" {
		t.Fatalf("%q", got)
	}
	if !errors.Is(e, cause) || errors.Unwrap(e) != cause {
		t.Fatal("Unwrap/Is on cause broken")
	}
	wrapped := fmt.Errorf("context: %w", NotFound("x"))
	if !errors.Is(wrapped, New(CodeNotFound, "")) {
		t.Fatal("errors.Is must match on code")
	}
	if errors.Is(wrapped, New(CodeInternal, "")) {
		t.Fatal("errors.Is matched a different code")
	}
	if errors.Is(New(CodeNotFound, ""), errors.New("plain")) {
		t.Fatal("matched a non-ApiError")
	}
	if d := NotFound("x").WithDetails(map[string]int{"a": 1}); d.Details == nil {
		t.Fatal("details lost")
	}
}

type fakeNetErr struct{ timeout bool }

func (f fakeNetErr) Error() string   { return "net error" }
func (f fakeNetErr) Timeout() bool   { return f.timeout }
func (f fakeNetErr) Temporary() bool { return false }

var _ net.Error = fakeNetErr{}

func TestNormalizeEveryBranch(t *testing.T) {
	if Normalize(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	api := NotFound("x")
	if got := Normalize(fmt.Errorf("wrapped: %w", api)); got != api {
		t.Fatal("ApiError (even wrapped) must pass through unchanged")
	}
	for _, c := range []struct {
		err  error
		want Code
	}{
		{context.DeadlineExceeded, CodeDeadlineExceeded},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), CodeDeadlineExceeded},
		{context.Canceled, CodeCanceled},
		{fakeNetErr{timeout: true}, CodeDeadlineExceeded},
		{fakeNetErr{timeout: false}, CodeUpstreamUnavailable},
		{errors.New("mystery"), CodeInternal},
	} {
		got := Normalize(c.err)
		if got.Code != c.want {
			t.Errorf("%v -> %s, want %s", c.err, got.Code, c.want)
		}
		if got.Code == CodeInternal && got.Message != "An internal error occurred." {
			t.Errorf("internal errors must expose a generic message, got %q", got.Message)
		}
	}
}

func TestPublicHidesTheCause(t *testing.T) {
	e := Wrap(errors.New("secret internals: password=hunter2"), CodeInternal, "An internal error occurred.").WithDetails("d")
	p := e.Public()
	if p.Code != CodeInternal || p.Message != "An internal error occurred." || p.Details != "d" {
		t.Fatalf("%+v", p)
	}
}

func TestMethodNotAllowedStatus(t *testing.T) {
	if got := StatusFor(CodeMethodNotAllowed); got != http.StatusMethodNotAllowed {
		t.Fatalf("METHOD_NOT_ALLOWED status = %d, want %d", got, http.StatusMethodNotAllowed)
	}
}
