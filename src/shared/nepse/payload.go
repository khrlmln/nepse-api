package nepse

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

type payloadMode int

const (
	modeScrip payloadMode = iota // getPOSTPayloadIDForScrips: ignores the salts
	modeGraph                    // getPOSTPayloadID
	modeFloor                    // getPOSTPayloadIDForFloorSheet
)

func computePayloadID(seedValue, seedIndex, day int, salts [5]int, mode payloadMode) (int, error) {
	if day < 1 || day > 31 {
		return 0, fmt.Errorf("invalid calendar day %d", day)
	}
	base := seedValue + seedIndex + 2*day
	idx := 3
	switch mode {
	case modeScrip:
		return base, nil
	case modeGraph:
		if base%10 >= 5 {
			idx = 1
		}
	case modeFloor:
		if base%10 < 4 {
			idx = 1
		}
	default:
		return 0, fmt.Errorf("unknown payload mode %d", mode)
	}
	return base + salts[idx]*day - salts[idx-1], nil
}

// payloadSpec holds the per-day inputs of a payload; the salts come from the token snapshot that is sent with the same request.
type payloadSpec struct {
	seedValue int
	seedIndex int
	day       int
}

func (s payloadSpec) body(mode payloadMode) bodyFunc {
	return func(st tokenState) ([]byte, error) {
		id, err := computePayloadID(s.seedValue, s.seedIndex, s.day, st.salts, mode)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]int{"id": id})
	}
}

func (c *Client) payloadSpec(ctx context.Context) (payloadSpec, error) {
	now := c.Today()
	key := now.Format(time.DateOnly)
	if spec, ok := c.cachedSpec(key, now.Day()); ok {
		return spec, nil
	}
	v, err := c.getJSON(ctx, c.endpoint(EpMarketStatus), c.cfg.CacheTTL, shapeObject)
	if err != nil {
		return payloadSpec{}, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return payloadSpec{}, apierrors.UpstreamInvalidResponse(nil, "NEPSE market status is not an object.")
	}
	seedIndex, ok := numberToInt(m["id"])
	if !ok || seedIndex < 0 || seedIndex >= len(c.assets.payloadSeeds) {
		return payloadSpec{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("market status id %v out of range", m["id"]), "")
	}
	c.payloadSpecMu.Lock()
	c.payloadSeedIndex, c.payloadSpecKey, c.payloadSpecReady = seedIndex, key, true
	c.payloadSpecMu.Unlock()
	return payloadSpec{seedValue: c.assets.payloadSeeds[seedIndex], seedIndex: seedIndex, day: now.Day()}, nil
}

// cachedSpec returns the cached payload inputs for the supplied Nepal calendar day.
func (c *Client) cachedSpec(key string, day int) (payloadSpec, bool) {
	c.payloadSpecMu.Lock()
	defer c.payloadSpecMu.Unlock()
	if !c.payloadSpecReady || c.payloadSpecKey != key {
		return payloadSpec{}, false
	}
	return payloadSpec{seedValue: c.assets.payloadSeeds[c.payloadSeedIndex], seedIndex: c.payloadSeedIndex, day: day}, true
}

func numberToInt(v any) (int, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Int64()
		return int(n), err == nil
	case float64:
		n := int(x)
		return n, float64(n) == x
	case int:
		return x, true
	default:
		return 0, false
	}
}
