// Package observability holds the logging conventions shared by both services: logger construction, request IDs, redaction and sanitising helpers.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	apierrors "nepse-api/src/shared/errors"
)

const (
	maxLogValueLen   = 256
	maxRequestIDLen  = 64
	redacted         = "[REDACTED]"
	requestIDPrefix  = "req_"
	requestIDRandLen = 12 // bytes -> 24 hex chars
)

var sensitiveKeys = []string{"authorization", "cookie", "token", "salt", "secret", "password", "apikey", "api_key", "signature"}

func isSensitive(key string) bool {
	key = strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(key, s) {
			return true
		}
	}
	return false
}

// NewLogger builds the structured logger: newline-delimited JSON by default, every event carrying the service name, with secret-looking attributes redacted.
func NewLogger(w io.Writer, level, format, service string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info", "":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", level)
	}
	opts := &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if isSensitive(a.Key) {
				return slog.String(a.Key, redacted)
			}
			return a
		},
	}
	var h slog.Handler
	switch strings.ToLower(format) {
	case "json", "":
		h = slog.NewJSONHandler(w, opts)
	case "text":
		h = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("unsupported log format %q", format)
	}
	return slog.New(h).With("service", service), nil
}

type ctxKey struct{}

// WithRequestID stores the request/correlation ID in ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// RequestID returns the request ID stored in ctx ("" if none).
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// NewRequestID generates a fresh, unguessable request ID.
func NewRequestID() string {
	b := make([]byte, requestIDRandLen)
	if _, err := rand.Read(b); err != nil {
		return requestIDPrefix + "unavailable"
	}
	return requestIDPrefix + hex.EncodeToString(b)
}

// ValidRequestID reports whether a client-supplied ID is safe to trust: bounded length and a restricted alphabet (no log injection, no header injection).
func ValidRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for _, r := range id {
		if !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.')) {
			return false
		}
	}
	return true
}

// Sanitize removes control characters (log injection), replaces invalid UTF-8 and bounds the length of a user-controlled string before it is logged. Truncation never splits a multi-byte character.
func Sanitize(s string) string {
	s = strings.ToValidUTF8(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s), "\uFFFD")
	if len(s) > maxLogValueLen {
		cut := maxLogValueLen
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

// RedactQuery returns the raw query string with sensitive parameter values replaced.
func RedactQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return redacted
	}
	for k := range values {
		if isSensitive(k) {
			values[k] = []string{redacted}
		}
	}
	return Sanitize(values.Encode())
}

// ErrorAttrs are the attributes logged for a failed operation. The internal cause is included (after redaction by the logger) but never sent to clients.
func ErrorAttrs(err *apierrors.ApiError) []any {
	attrs := []any{"error_code", string(err.Code), "error_kind", kind(err)}
	if err.Cause != nil {
		attrs = append(attrs, "error_cause", Sanitize(err.Cause.Error()))
	}
	return attrs
}

func kind(err *apierrors.ApiError) string {
	switch {
	case err.Code == apierrors.CodeInternal:
		return "internal"
	case err.Status() >= 500:
		return "upstream"
	default:
		return "client"
	}
}
