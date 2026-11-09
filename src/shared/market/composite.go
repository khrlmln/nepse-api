package market

import (
	"encoding/json"
	"fmt"

	apierrors "nepse-api/src/shared/errors"
)

// sectorIndex maps a company-list sector name to its NEPSE sub-index name.
var sectorIndex = map[string]string{
	"Commercial Banks":             "Banking SubIndex",
	"Development Banks":            "Development Bank Index",
	"Finance":                      "Finance Index",
	"Hotels And Tourism":           "Hotels And Tourism Index",
	"Hydro Power":                  "HydroPower Index",
	"Investment":                   "Investment Index",
	"Life Insurance":               "Life Insurance",
	"Manufacturing And Processing": "Manufacturing And Processing",
	"Microfinance":                 "Microfinance Index",
	"Mutual Fund":                  "Mutual Fund",
	"Non Life Insurance":           "Non Life Insurance",
	"Others":                       "Others Index",
	"Tradings":                     "Trading Index",
}

// overviewInput holds the eight raw upstream collections the overview combines.
type overviewInput struct {
	companies, turnover, transactions, trades, gainers, losers, priceVolume, subIndices any
}

// It is the pure aggregation behind TurnoverOverview: scripsDetails (only scrips
// found in the top gainers/losers with a non-zero previous close, and sectorsDetails (per-sector totals joined with the sub-index).
func buildTurnoverOverview(in overviewInput) (any, error) {
	companies, err := indexBySymbol(in.companies, "company list")
	if err != nil {
		return nil, err
	}
	lists := struct{ turnover, transactions, trades, gainers, losers, priceVolume map[string]map[string]any }{}
	for _, l := range []struct {
		what string
		raw  any
		dst  *map[string]map[string]any
	}{
		{"top turnover", in.turnover, &lists.turnover},
		{"top transactions", in.transactions, &lists.transactions},
		{"top trade", in.trades, &lists.trades},
		{"top gainers", in.gainers, &lists.gainers},
		{"top losers", in.losers, &lists.losers},
		{"price volume", in.priceVolume, &lists.priceVolume},
	} {
		if *l.dst, err = indexBySymbol(l.raw, l.what); err != nil {
			return nil, err
		}
	}

	scrips := map[string]map[string]any{}
	for _, symbol := range symbolOrder(in.companies) {
		detail := scripDetail(symbol, companies[symbol], lists.turnover[symbol], lists.transactions[symbol],
			lists.trades[symbol], lists.priceVolume[symbol], winner(lists.gainers, lists.losers, symbol))
		if asFloat(detail["ltp"]) == 0 || asFloat(detail["previousClose"]) == 0 {
			continue
		}
		scrips[symbol] = detail
	}
	return map[string]any{
		"scripsDetails":  scrips,
		"sectorsDetails": sectorTotals(companies, scrips, subIndexByName(in.subIndices)),
	}, nil
}

// winner returns the symbol's row from the top gainers, else from the top losers.
func winner(gainers, losers map[string]map[string]any, symbol string) map[string]any {
	if g := gainers[symbol]; g != nil {
		return g
	}
	return losers[symbol]
}

// scripDetail assembles one scrip's row. Values are passed through exactly as NEPSE sent them (json.Number etc.)
func scripDetail(symbol string, company, turnover, transaction, trade, priceVolume, winner map[string]any) map[string]any {
	detail := map[string]any{
		"symbol":              symbol,
		"sector":              company["sectorName"],
		"Turnover":            pick(turnover, "turnover", 0),
		"transaction":         pick(transaction, "totalTrades", 0),
		"volume":              pick(trade, "shareTraded", 0),
		"previousClose":       pick(priceVolume, "previousClose", 0),
		"lastUpdatedDateTime": pick(priceVolume, "lastUpdatedDateTime", 0),
		"name":                pick(company, "securityName", ""),
		"category":            company["instrumentType"],
		"pointChange":         0,
		"percentageChange":    0,
		"ltp":                 0,
	}
	if winner != nil {
		detail["pointChange"] = pick(winner, "pointChange", 0)
		detail["percentageChange"] = pick(winner, "percentageChange", 0)
		detail["ltp"] = pick(winner, "ltp", 0)
	}
	return detail
}

// sectorTotals sums the scrips of every sector present in the company list and joins each with its sub-index (falling back to the sector name.
func sectorTotals(companies map[string]map[string]any, scrips map[string]map[string]any, subIndices map[string]any) map[string]map[string]any {
	totals := map[string]map[string]any{}
	for _, company := range companies {
		sector, _ := company["sectorName"].(string)
		if _, done := totals[sector]; done {
			continue
		}
		var trades, quantity, turnover float64
		for _, d := range scrips {
			if name, _ := d["sector"].(string); name == sector {
				trades += asFloat(d["transaction"])
				quantity += asFloat(d["volume"])
				turnover += asFloat(d["Turnover"])
			}
		}
		indexName, mapped := sectorIndex[sector]
		if !mapped {
			indexName = sector
		}
		totals[sector] = map[string]any{
			"transaction":   trades,
			"volume":        quantity,
			"totalTurnover": turnover,
			"turnover":      subIndices[indexName],
			"sectorName":    sector,
		}
	}
	return totals
}

// subIndexByName indexes the sub-index list by its "index" name.
func subIndexByName(raw any) map[string]any {
	out := map[string]any{}
	items, _ := raw.([]any)
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if name, ok := m["index"].(string); ok {
				out[name] = m
			}
		}
	}
	return out
}

func indexBySymbol(v any, what string) (map[string]map[string]any, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, apierrors.UpstreamInvalidResponse(fmt.Errorf("%s: unexpected shape", what), "")
	}
	out := make(map[string]map[string]any, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if symbol, ok := m["symbol"].(string); ok && symbol != "" {
				out[symbol] = m
			}
		}
	}
	return out, nil
}

// symbolOrder lists symbols in first-seen list order.
func symbolOrder(v any) []string {
	items, _ := v.([]any)
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if symbol, ok := m["symbol"].(string); ok && symbol != "" {
			if _, dup := seen[symbol]; !dup {
				seen[symbol] = struct{}{}
				out = append(out, symbol)
			}
		}
	}
	return out
}

func pick(m map[string]any, key string, def any) any {
	if m == nil {
		return def
	}
	if v, ok := m[key]; ok {
		return v
	}
	return def
}

// asFloat converts a decoded JSON number (json.Number or Go numeric) to float64; anything else is 0.
func asFloat(v any) float64 {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0
		}
		return f
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	default:
		return 0
	}
}
