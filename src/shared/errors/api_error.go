// Package apierrors is the single error model shared by the REST and MCP services. Use-case and client code return *ApiError (or errors wrapping one); each transport adapts it to its own wire format.
package apierrors

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Code is a stable, machine-readable error category.
type Code string

const (
	CodeInvalidArgument     Code = "INVALID_ARGUMENT"
	CodeMethodNotAllowed    Code = "METHOD_NOT_ALLOWED"
	CodeForbidden           Code = "FORBIDDEN"
	CodeNotFound            Code = "NOT_FOUND"
	CodeRateLimited         Code = "RATE_LIMITED"
	CodeUpstreamAuthFailed  Code = "UPSTREAM_AUTH_FAILED"
	CodeUpstreamInvalidResp Code = "UPSTREAM_INVALID_RESPONSE"
	CodeUpstreamUnavailable Code = "UPSTREAM_UNAVAILABLE"
	CodeDeadlineExceeded    Code = "DEADLINE_EXCEEDED"
	CodeCanceled            Code = "CANCELED"
	CodeUnavailable         Code = "UNAVAILABLE"
	CodeInternal            Code = "INTERNAL"
)

// StatusClientClosedRequest is the de-facto status for a client that went away.
const StatusClientClosedRequest = 499

// ApiError carries a safe client-facing message plus an internal cause for logs.
type ApiError struct {
	Code       Code          // stable category
	Message    string        // safe, client-facing
	HTTPStatus int           // REST status; derived from Code when zero
	Cause      error         // internal only: logged, never sent to clients
	Details    any           // optional safe, client-actionable details (validation info)
	Retryable  bool          // whether the caller may retry
	RetryAfter time.Duration // optional hint for RATE_LIMITED / UPSTREAM_UNAVAILABLE
}

func (e *ApiError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *ApiError) Unwrap() error { return e.Cause }

// Is makes errors.Is(err, apierrors.New(apierrors.CodeNotFound, "")) match on the code.
func (e *ApiError) Is(target error) bool {
	var t *ApiError
	return errors.As(target, &t) && t.Code == e.Code
}

// Status is the HTTP status for REST responses.
func (e *ApiError) Status() int {
	if e.HTTPStatus != 0 {
		return e.HTTPStatus
	}
	return StatusFor(e.Code)
}

// StatusFor maps a code to its REST status (one consistent choice per code).
func StatusFor(code Code) int {
	switch code {
	case CodeInvalidArgument:
		return http.StatusBadRequest
	case CodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeUpstreamAuthFailed, CodeUpstreamInvalidResp:
		return http.StatusBadGateway
	case CodeUpstreamUnavailable, CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case CodeCanceled:
		return StatusClientClosedRequest
	default:
		return http.StatusInternalServerError
	}
}

// New builds an error with a safe message.
func New(code Code, message string) *ApiError { return &ApiError{Code: code, Message: message} }

// Wrap builds an error that keeps cause for diagnostics.
func Wrap(cause error, code Code, message string) *ApiError {
	return &ApiError{Code: code, Message: message, Cause: cause}
}

// WithDetails attaches safe, client-actionable details.
func (e *ApiError) WithDetails(details any) *ApiError { e.Details = details; return e }

// AsRetryable marks the error retryable, optionally with a retry hint.
func (e *ApiError) AsRetryable(after time.Duration) *ApiError {
	e.Retryable, e.RetryAfter = true, after
	return e
}

func InvalidArgument(message string) *ApiError { return New(CodeInvalidArgument, message) }
func Forbidden(message string) *ApiError       { return New(CodeForbidden, message) }
func NotFound(message string) *ApiError        { return New(CodeNotFound, message) }

func RateLimited(after time.Duration) *ApiError {
	return New(CodeRateLimited, "Too many requests. Please retry later.").AsRetryable(after)
}

func UpstreamAuthFailed(cause error) *ApiError {
	return Wrap(cause, CodeUpstreamAuthFailed, "Could not authenticate with NEPSE.").AsRetryable(0)
}

func UpstreamInvalidResponse(cause error, message string) *ApiError {
	if message == "" {
		message = "NEPSE returned data in an unexpected format."
	}
	return Wrap(cause, CodeUpstreamInvalidResp, message)
}

func UpstreamUnavailable(cause error) *ApiError {
	return Wrap(cause, CodeUpstreamUnavailable, "Market data is temporarily unavailable.").AsRetryable(0)
}

func DeadlineExceeded(cause error) *ApiError {
	return Wrap(cause, CodeDeadlineExceeded, "NEPSE did not respond before the request deadline.").AsRetryable(0)
}

// Unavailable reports that this service itself cannot serve right now (for example while shutting down), as opposed to UpstreamUnavailable for NEPSE problems.
func Unavailable(message string) *ApiError {
	return New(CodeUnavailable, message).AsRetryable(time.Second)
}

func Internal(cause error) *ApiError {
	return Wrap(cause, CodeInternal, "An internal error occurred.")
}
