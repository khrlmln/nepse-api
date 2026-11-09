package rest

import "nepse-api/src/rest/handlers"

// tradingRoutes are the floorsheet endpoints.
func tradingRoutes(trd handlers.Trading) []route {
	const v1 = apiV1
	return []route{
		{v1 + "/trading/floorsheet", operation{ID: "getFloorsheet", Tag: "Trading", Summary: "Floorsheet page (all securities)",
			Description: "One page (500 rows, zero-based) of the latest available all-market session. Page through using meta.total_pages." + passthroughNote,
			Params:      []param{{Name: "page", In: "query", Description: "Zero-based page index (default 0).", Type: "integer"}}, Response: kindFloorsheet}, trd.Floorsheet()},
		{v1 + "/trading/floorsheet/{symbol}", operation{ID: "getSecurityFloorsheet", Tag: "Trading", Summary: "Floorsheet page (one security)",
			Description: "One page (500 rows, zero-based) of the latest available session filtered to one security. This scans the bounded all-market session pages and may be slower than the all-market route. Historical per-symbol floorsheets are not available from NEPSE." + passthroughNote,
			Params: []param{symbolParam,
				{Name: "business_date", In: "query", Description: "Optional business date (YYYY-MM-DD); must match the latest session returned by NEPSE."},
				{Name: "page", In: "query", Description: "Zero-based page index (default 0).", Type: "integer"}}, Response: kindFloorsheet}, trd.Floorsheet()},
	}
}
