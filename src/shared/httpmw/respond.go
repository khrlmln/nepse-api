package httpmw

import (
	"encoding/json"
	"net/http"
	"strconv"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/observability"
)

// ErrorBody is the failure body: {"error": {...}}.
type ErrorBody struct {
	Error ErrorPayload `json:"error"`
}

// ErrorPayload is the client-visible error.
type ErrorPayload struct {
	Code      apierrors.Code `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id,omitempty"`
	Details   any            `json:"details,omitempty"`
}

// WriteJSON writes a JSON body. Encoding happens before the header is written so an unencodable value becomes a clean 500 instead of a truncated 200.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	b, err := json.Marshal(body)
	if err != nil {
		WriteError(w, r, apierrors.Internal(err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)+1))
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

// WriteError translates any error into the canonical error response.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := apierrors.Normalize(err)
	cause := ""
	if apiErr.Cause != nil {
		cause = observability.Sanitize(apiErr.Cause.Error())
	}
	SetError(r.Context(), apiErr.Code, cause)
	// Drop headers a failed handler may have set for a body it never wrote.
	w.Header().Del("Content-Length")
	w.Header().Del("Content-Encoding")
	if apiErr.RetryAfter > 0 || apiErr.Code == apierrors.CodeRateLimited {
		secs := int(apiErr.RetryAfter.Seconds() + 0.999)
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	body := ErrorBody{Error: ErrorPayload{
		Code: apiErr.Code, Message: apiErr.Message,
		RequestID: observability.RequestID(r.Context()), Details: apiErr.Details,
	}}
	b, mErr := json.Marshal(body)
	if mErr != nil { // cannot happen for these types; never leave the client without a body
		b = []byte(`{"error":{"code":"INTERNAL","message":"An internal error occurred."}}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(apiErr.Status())
	_, _ = w.Write(append(b, '\n'))
}
