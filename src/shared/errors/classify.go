package apierrors

import (
	"context"
	"errors"
	"net"
)

// Normalize turns any error into an *ApiError that is safe to expose. Unknown errors become INTERNAL with the original kept as the (private) cause.
func Normalize(err error) *ApiError {
	if err == nil {
		return nil
	}
	var apiErr *ApiError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return DeadlineExceeded(err)
	case errors.Is(err, context.Canceled):
		return Wrap(err, CodeCanceled, "The request was canceled.")
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return DeadlineExceeded(err)
		}
		return UpstreamUnavailable(err)
	}
	return Internal(err)
}

// PublicError is the transport-neutral, client-visible part of an error.
type PublicError struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Public returns only what may be shown to a caller.
func (e *ApiError) Public() PublicError {
	return PublicError{Code: e.Code, Message: e.Message, Details: e.Details}
}
