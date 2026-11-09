package nepse

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

const (
	securityMissTTL = 5 * time.Minute // how long an unknown symbol is remembered
	// maxSecurityMisses bounds the negative symbol cache so a caller probing endless distinct unknown symbols cannot grow memory without limit.
	maxSecurityMisses = 1024
)

// SecurityList returns the non-delisted securities and refreshes the symbol index.
func (c *Client) SecurityList(ctx context.Context) ([]map[string]any, error) {
	v, err := c.Get(ctx, EpSecurityList)
	if err != nil {
		return nil, err
	}
	items, err := ExpectObjects(v, "security list")
	if err != nil {
		return nil, err
	}
	ids := make(map[string]int, len(items))
	for _, m := range items {
		if id, ok := numberToInt(m["id"]); ok && id > 0 {
			if sym := strings.ToUpper(strings.TrimSpace(Str(m, "symbol"))); sym != "" {
				ids[sym] = id
			}
		}
	}
	c.securityMu.Lock()
	c.securityIDs = ids // rebuilt wholesale so delisted symbols disappear
	for sym := range ids {
		delete(c.securityMiss, sym)
	}
	c.securityMu.Unlock()
	return items, nil
}

// CompanyList returns the company list (symbol, sector, ...).
func (c *Client) CompanyList(ctx context.Context) ([]map[string]any, error) {
	v, err := c.Get(ctx, EpCompanyList)
	if err != nil {
		return nil, err
	}
	return ExpectObjects(v, "company list")
}

// SecurityID resolves a normalised symbol to NEPSE's security id, refreshing the security list on a miss. Unknown symbols return NOT_FOUND and are remembered briefly (bounded) so repeated probes do not hit NEPSE.
func (c *Client) SecurityID(ctx context.Context, symbol string) (int, error) {
	if id, known, missed := c.lookup(symbol); known {
		return id, nil
	} else if missed {
		return 0, notFound(symbol)
	}
	if _, err := c.SecurityList(ctx); err != nil {
		return 0, err
	}
	c.securityMu.Lock()
	defer c.securityMu.Unlock()
	if id, ok := c.securityIDs[symbol]; ok {
		return id, nil
	}
	c.rememberMissLocked(symbol)
	return 0, notFound(symbol)
}

func notFound(symbol string) error {
	return apierrors.NotFound(fmt.Sprintf("Symbol %q was not found.", symbol))
}

// lookup consults the symbol index and the negative cache without any network call.
func (c *Client) lookup(symbol string) (id int, known, recentlyMissed bool) {
	c.securityMu.Lock()
	defer c.securityMu.Unlock()
	if id, ok := c.securityIDs[symbol]; ok {
		return id, true, false
	}
	t, missed := c.securityMiss[symbol]
	return 0, false, missed && time.Since(t) < securityMissTTL
}

func (c *Client) rememberMissLocked(symbol string) {
	if len(c.securityMiss) >= maxSecurityMisses {
		for k, t := range c.securityMiss {
			if time.Since(t) >= securityMissTTL {
				delete(c.securityMiss, k)
			}
		}
		for k := range c.securityMiss {
			if len(c.securityMiss) < maxSecurityMisses {
				break
			}
			delete(c.securityMiss, k)
		}
	}
	c.securityMiss[symbol] = time.Now()
}
