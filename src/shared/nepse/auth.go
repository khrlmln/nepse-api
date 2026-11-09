package nepse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

const (
	tokenValidity = 45 * time.Second
	proveEndpoint = "/api/authenticate/prove"
	// authFailureCooldown is how long a failed refresh is remembered. Calls inside the window fail fast with the same error instead of each hitting NEPSE, so an outage (or a rejected login) cannot become a refresh storm.
	authFailureCooldown = 2 * time.Second
)

// flight is one in-progress token refresh whose outcome every waiter shares.
type flight struct {
	done chan struct{}
	st   tokenState
	err  error
}

// tokenManager caches the parsed access token and salts and refreshes them with single-flight semantics: one refresh at a time, its result shared by all waiters.
type tokenManager struct {
	mu       sync.Mutex
	state    tokenState
	parser   *wasmTokenParser
	client   *Client
	inflight *flight
	failedAt time.Time
	failErr  error
}

func (t *tokenManager) validLocked() bool {
	return t.state.access != "" && time.Since(t.state.fetchedAt) < tokenValidity
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// snapshot returns a consistent view of the token and its salts, refreshing when missing or older than tokenValidity. Payloads must be derived from the salts of the snapshot whose token is sent. Waiters adopt the outcome of the refresh they waited for. Only when that refresh failed because its own caller's context ended do they try again (their contexts are still alive); any other failure is returned to all of them.
func (t *tokenManager) snapshot(ctx context.Context) (tokenState, error) {
	for {
		t.mu.Lock()
		if t.validLocked() {
			s := t.state
			t.mu.Unlock()
			return s, nil
		}
		if t.failErr != nil && time.Since(t.failedAt) < authFailureCooldown {
			err := t.failErr
			t.mu.Unlock()
			return tokenState{}, err
		}
		if fl := t.inflight; fl != nil {
			t.mu.Unlock()
			select {
			case <-ctx.Done():
				return tokenState{}, ctx.Err()
			case <-fl.done:
			}
			if fl.err == nil {
				return fl.st, nil
			}
			if isContextErr(fl.err) {
				continue
			}
			return tokenState{}, fl.err
		}
		fl := &flight{done: make(chan struct{})}
		t.inflight = fl
		t.mu.Unlock()

		start := time.Now()
		st, err := t.fetch(ctx)
		t.client.log().Log(ctx, levelFor(err), "nepse authentication refresh",
			"operation", "authenticate", "duration_ms", ms(time.Since(start)), "ok", err == nil)

		t.mu.Lock()
		fl.st, fl.err = st, err
		switch {
		case err == nil:
			t.state, t.failErr, t.failedAt = st, nil, time.Time{}
		case !isContextErr(err):
			t.failErr, t.failedAt = err, time.Now()
		}
		t.inflight = nil
		close(fl.done)
		t.mu.Unlock()
		return st, err
	}
}

// fetch requests and parses a fresh token. It never panics: a failure inside the WASM runtime must not leave the manager stuck in the updating state.
func (t *tokenManager) fetch(ctx context.Context) (st tokenState, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = apierrors.UpstreamAuthFailed(fmt.Errorf("token parser panic (%T)", r))
		}
	}()
	raw, err := t.client.requestNoAuth(ctx, http.MethodGet, proveEndpoint)
	if err != nil {
		return tokenState{}, asAuthFailure(err)
	}
	var a AuthResponse
	if err := json.Unmarshal(raw, &a); err != nil {
		return tokenState{}, apierrors.UpstreamAuthFailed(fmt.Errorf("decode authentication response: %w", err))
	}
	if a.AccessToken == "" || a.RefreshToken == "" {
		return tokenState{}, apierrors.UpstreamAuthFailed(fmt.Errorf("authentication response missing tokens"))
	}
	access, refresh, err := t.parser.parse(ctx, a)
	if err != nil {
		return tokenState{}, apierrors.UpstreamAuthFailed(err)
	}
	return tokenState{
		access: access, refresh: refresh, fetchedAt: time.Now(),
		salts: [5]int{a.Salt1, a.Salt2, a.Salt3, a.Salt4, a.Salt5},
	}, nil
}

// asAuthFailure keeps context errors and already-classified upstream errors, and labels anything else as an authentication failure.
func asAuthFailure(err error) error {
	if apiErr := apierrors.Normalize(err); apiErr.Code != apierrors.CodeInternal {
		return err
	}
	return apierrors.UpstreamAuthFailed(err)
}

// invalidate drops the cached token only if it is still the one upstream rejected.
func (t *tokenManager) invalidate(rejected string) {
	t.mu.Lock()
	if t.state.access == rejected {
		t.state.access = ""
	}
	t.mu.Unlock()
}
