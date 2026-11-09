package nepse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

const (
	listCacheTTL    = 10 * time.Minute // company / security lists
	depthCacheTTL   = 2 * time.Second
	historyCacheTTL = time.Minute
	historyPageSize = 500 // rows requested for price history (one upstream page)
)

// decodeJSON decodes with UseNumber so ids and prices pass through exactly.
func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, apierrors.UpstreamInvalidResponse(fmt.Errorf("decode NEPSE JSON: %w", err), "")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple top-level JSON values")
		}
		return nil, apierrors.UpstreamInvalidResponse(fmt.Errorf("trailing data after NEPSE JSON: %w", err), "")
	}
	return v, nil
}

// fetchChecked performs an authenticated GET and returns the body only if it is valid JSON of the expected shape. Nothing that fails these checks is ever cached.
func (c *Client) fetchChecked(ctx context.Context, path string, shape shapeKind) ([]byte, error) {
	b, err := c.execute(ctx, http.MethodGet, path, true, nil)
	if err != nil {
		return nil, err
	}
	v, err := decodeJSON(b)
	if err != nil {
		return nil, err
	}
	if err := checkShape(v, shape, route(path)); err != nil {
		return nil, err
	}
	return b, nil
}

// getJSON fetches (and caches) a GET response; see fetchChecked for validation.
func (c *Client) getJSON(ctx context.Context, path string, ttl time.Duration, shape shapeKind) (any, error) {
	b, _, stale, err := c.cache.Do(ctx, "GET "+path, ttl, func(ctx context.Context) ([]byte, error) {
		return c.fetchChecked(ctx, path, shape)
	})
	if err != nil {
		return nil, err
	}
	if stale {
		c.log().Log(ctx, slog.LevelWarn, "serving a stale NEPSE response because the refresh failed",
			"operation", "stale_cache", "path", route(path), "max_stale", c.cfg.MaxStale.String())
	}
	return decodeJSON(b)
}

// postRaw issues an authenticated POST whose body is the payload id for mode.
func (c *Client) postRaw(ctx context.Context, path string, mode payloadMode) ([]byte, error) {
	spec, err := c.payloadSpec(ctx)
	if err != nil {
		return nil, err
	}
	return c.execute(ctx, http.MethodPost, path, true, spec.body(mode))
}

func (c *Client) postJSON(ctx context.Context, path string, mode payloadMode) (any, error) {
	b, err := c.postRaw(ctx, path, mode)
	if err != nil {
		return nil, err
	}
	return decodeJSON(b)
}

// Get fetches a read-only upstream collection (cached briefly).
func (c *Client) Get(ctx context.Context, ep Endpoint) (any, error) {
	ttl := c.cfg.CacheTTL
	if ep == EpCompanyList || ep == EpSecurityList {
		ttl = listCacheTTL
	}
	return c.getJSON(ctx, c.endpoint(ep), ttl, endpointShapes[ep])
}

// Graph fetches one of the daily index graphs (main index or sub-index).
func (c *Client) Graph(ctx context.Context, ep Endpoint) (any, error) {
	return c.postJSON(ctx, c.endpoint(ep), modeGraph)
}

// Ping actively verifies NEPSE reachability and the authentication flow. It bypasses the response cache (and its stale-on-error fallback), so a success means NEPSE answered this very call.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.fetchChecked(ctx, c.endpoint(EpMarketStatus), endpointShapes[EpMarketStatus])
	return err
}

func (c *Client) postForSymbol(ctx context.Context, ep Endpoint, symbol string) (any, error) {
	id, err := c.SecurityID(ctx, symbol)
	if err != nil {
		return nil, err
	}
	return c.postJSON(ctx, c.endpoint(ep)+strconv.Itoa(id), modeScrip)
}

// CompanyDetails returns the company details of a symbol.
func (c *Client) CompanyDetails(ctx context.Context, symbol string) (any, error) {
	return c.postForSymbol(ctx, EpCompanyDetails, symbol)
}

// DailyScripGraph returns the intraday price series of a symbol.
func (c *Client) DailyScripGraph(ctx context.Context, symbol string) (any, error) {
	return c.postForSymbol(ctx, EpCompanyDailyGraph, symbol)
}

// MarketDepth returns the order depth of a symbol (cached for a moment).
func (c *Client) MarketDepth(ctx context.Context, symbol string) (any, error) {
	id, err := c.SecurityID(ctx, symbol)
	if err != nil {
		return nil, err
	}
	return c.getJSON(ctx, fmt.Sprintf("%s%d/", c.endpoint(EpMarketDepth), id), depthCacheTTL, shapeCollection)
}

// PriceVolumeHistory returns up to 500 daily rows between start and end (both required; the use case applies the defaults and validates the range).
func (c *Client) PriceVolumeHistory(ctx context.Context, symbol string, start, end time.Time) (any, error) {
	if start.IsZero() || end.IsZero() {
		return nil, apierrors.InvalidArgument("Both start and end dates are required.")
	}
	id, err := c.SecurityID(ctx, symbol)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("%s%d?&size=%d&startDate=%s&endDate=%s", c.endpoint(EpPriceHistory), id, historyPageSize,
		start.Format(time.DateOnly), end.Format(time.DateOnly))
	v, err := c.getJSON(ctx, path, historyCacheTTL, shapeCollection)
	if err != nil {
		return nil, err
	}
	if m, ok := v.(map[string]any); ok {
		if content, ok := m["content"]; ok {
			return content, nil
		}
	}
	return v, nil
}
