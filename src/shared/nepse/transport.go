package nepse

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

const (
	maxAttempts    = 3
	baseRetryDelay = 500 * time.Millisecond
	maxRetryDelay  = 5 * time.Second
	// maxRetryAfter caps a Retry-After hint copied from NEPSE to our own callers.
	maxRetryAfter = 5 * time.Minute
)

// bodyFunc builds a request body from the token snapshot about to be used, so a payload always matches the salts of the token sent with it (and is rebuilt after a token refresh).
type bodyFunc func(tokenState) ([]byte, error)

// attempt is the outcome of one HTTP exchange.
type attempt struct {
	body   []byte
	status int
	token  string // access token used ("" when none was obtained or needed)
}

func (c *Client) newRequest(ctx context.Context, method, path string, body []byte, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for name, value := range c.assets.headers {
		switch strings.ToLower(name) {
		case "host", "referer", "accept-encoding", "connection", "content-length":
			// managed by net/http or set below
		case "te":
			// HTTP/2 allows only the lower-case token "trailers" and strict servers answer 400 to anything else (HEADERS.json carries "Trailers").
			if strings.EqualFold(strings.TrimSpace(value), "trailers") {
				req.Header.Set(name, "trailers")
			}
		default:
			req.Header.Set(name, value)
		}
	}
	if c.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}
	req.Header.Set("Referer", c.referer)
	if token != "" {
		// NEPSE's "Salter" scheme: the transformed token, not a Bearer token.
		req.Header.Set("Authorization", "Salter "+token)
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// doOnce performs one HTTP exchange and classifies every failure as an ApiError (context errors are returned as-is).
func (c *Client) doOnce(ctx context.Context, method, path string, auth bool, build bodyFunc) (attempt, error) {
	var a attempt
	var reqBody []byte
	if auth {
		st, err := c.token.snapshot(ctx)
		if err != nil {
			return a, err
		}
		a.token = st.access
		if build != nil {
			if reqBody, err = build(st); err != nil {
				return a, apierrors.Internal(fmt.Errorf("build request payload: %w", err))
			}
		}
	}
	req, err := c.newRequest(ctx, method, path, reqBody, a.token)
	if err != nil {
		return a, apierrors.Internal(fmt.Errorf("build request: %w", err))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return a, transportError(ctx, err)
	}
	defer resp.Body.Close()
	a.status = resp.StatusCode

	b, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxResponseBytes+1))
	if err != nil {
		return a, transportError(ctx, err)
	}
	if int64(len(b)) > c.cfg.MaxResponseBytes {
		return a, apierrors.UpstreamInvalidResponse(fmt.Errorf("response exceeds %d bytes", c.cfg.MaxResponseBytes),
			"NEPSE returned more data than the configured limit allows.")
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		a.body = b
		return a, nil
	}
	return a, statusError(resp)
}

// transportError maps a failure of the HTTP exchange itself.
func transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		// A certificate the system does not trust will not become trusted by retrying: fail once. Operators fix this with NEPSE_CA_BUNDLE.
		unavailable := apierrors.UpstreamUnavailable(err)
		unavailable.Retryable = false
		return unavailable
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return apierrors.DeadlineExceeded(err)
	}
	return apierrors.UpstreamUnavailable(err)
}

// statusError maps a non-2xx NEPSE response to an ApiError. Upstream bodies are never forwarded.
func statusError(resp *http.Response) error {
	cause := fmt.Errorf("upstream status %d", resp.StatusCode)
	after := parseRetryAfter(resp.Header.Get("Retry-After"))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return apierrors.UpstreamAuthFailed(cause)
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		return apierrors.UpstreamUnavailable(cause).AsRetryable(after)
	default: // 400 and other 4xx: upstream rejected what we sent
		return apierrors.UpstreamInvalidResponse(cause, "NEPSE rejected the request.")
	}
}

// parseRetryAfter reads a Retry-After header (seconds or HTTP date), capped at maxRetryAfter; 0 means absent or invalid.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var d time.Duration
	isDeltaSeconds := true
	for _, r := range value {
		if r < '0' || r > '9' {
			isDeltaSeconds = false
			break
		}
	}
	if isDeltaSeconds {
		secs, err := strconv.ParseInt(value, 10, 64)
		maxSeconds := int64(maxRetryAfter / time.Second)
		if err != nil || secs >= maxSeconds {
			return maxRetryAfter
		}
		d = time.Duration(secs) * time.Second
	} else if when, err := http.ParseTime(value); err == nil && time.Until(when) > 0 {
		d = time.Until(when)
	}
	return min(d, maxRetryAfter)
}

func shouldRetry(err error) bool {
	var apiErr *apierrors.ApiError
	if !errors.As(err, &apiErr) || !apiErr.Retryable {
		return false
	}
	return apiErr.Code == apierrors.CodeUpstreamUnavailable || apiErr.Code == apierrors.CodeDeadlineExceeded
}

func retryDelay(attemptNo int, err error) time.Duration {
	d := baseRetryDelay << attemptNo
	var apiErr *apierrors.ApiError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > d {
		d = apiErr.RetryAfter
	}
	return min(d, maxRetryDelay)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// execute runs a request with bounded retries. Authenticated requests also get exactly one transparent token refresh on 401, after which the body is rebuilt from the new token's salts.
func (c *Client) execute(ctx context.Context, method, path string, auth bool, build bodyFunc) ([]byte, error) {
	endpointPath := route(path)
	refreshed := false
	var last error
	for n := 0; n < maxAttempts; n++ {
		start := time.Now()
		a, err := c.doOnce(ctx, method, path, auth, build)
		attrs := []any{"operation", "upstream_request", "method", method, "path", endpointPath, "attempt", n + 1,
			"status", a.status, "duration_ms", ms(time.Since(start))}
		if err == nil {
			c.log().Log(ctx, slog.LevelDebug, "nepse upstream request", attrs...)
			return a.body, nil
		}
		last = err
		apiErr := apierrors.Normalize(err)
		c.log().Log(ctx, slog.LevelWarn, "nepse upstream request failed",
			append(attrs, "error_code", string(apiErr.Code), "error_cause", errText(apiErr.Cause))...)

		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case auth && a.token == "":
			// The failure happened while obtaining a token, before any request was sent. The refresh already retried internally and failures are held in a cooldown, so retrying here would only multiply attempts against NEPSE.
			return nil, err
		case auth && apiErr.Code == apierrors.CodeUpstreamAuthFailed && a.status == http.StatusUnauthorized && !refreshed:
			c.log().Log(ctx, slog.LevelInfo, "nepse token rejected; refreshing", "operation", "authenticate", "path", endpointPath)
			c.token.invalidate(a.token)
			refreshed = true
			continue
		case !shouldRetry(err):
			return nil, err
		}
		if n < maxAttempts-1 {
			if serr := sleep(ctx, retryDelay(n, err)); serr != nil {
				return nil, serr
			}
		}
	}
	return nil, last
}

// requestNoAuth is execute for unauthenticated calls (the login endpoint).
func (c *Client) requestNoAuth(ctx context.Context, method, path string) ([]byte, error) {
	return c.execute(ctx, method, path, false, nil)
}

// route strips the query string (for logs and error text).
func route(path string) string {
	r, _, _ := strings.Cut(path, "?")
	return r
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func levelFor(err error) slog.Level {
	if err != nil {
		return slog.LevelWarn
	}
	return slog.LevelDebug
}
