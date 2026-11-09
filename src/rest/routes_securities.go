package rest

import "nepse-api/src/rest/handlers"

// symbolParam is the {symbol} path parameter shared by every per-security route.
var symbolParam = param{Name: "symbol", In: "path", Required: true, Description: "NEPSE security symbol, e.g. NABIL."}

// securityRoutes are the per-security and sector endpoints.
func securityRoutes(sec handlers.Securities) []route {
	const v1 = apiV1
	return []route{
		{v1 + "/securities", operation{ID: "listSecurities", Tag: "Securities", Summary: "List securities",
			Description: "Listed (non-delisted) securities, optionally filtered by symbol or name, paginated." + passthroughNote,
			Params: []param{
				{Name: "q", In: "query", Description: "Case-insensitive filter on symbol or name (max 64 characters)."},
				{Name: "page", In: "query", Description: "1-based page number (default 1).", Type: "integer"},
				{Name: "limit", In: "query", Description: "Items per page (default 50, max 500).", Type: "integer"},
			}, Response: kindPaged}, sec.List()},
		{v1 + "/securities/{symbol}", operation{ID: "getSecurityDetails", Tag: "Securities", Summary: "Security details",
			Description: "Company details of a security." + passthroughNote, Params: []param{symbolParam}, Response: kindAny}, sec.Details()},
		{v1 + "/securities/{symbol}/validate", operation{ID: "validateSymbol", Tag: "Securities", Summary: "Validate a symbol",
			Description: "Confirms the symbol is a listed security. Unknown symbols return NOT_FOUND with suggestions in error.details.",
			Params:      []param{symbolParam}, Response: kindObject}, sec.Validate()},
		{v1 + "/securities/{symbol}/market-depth", operation{ID: "getMarketDepth", Tag: "Securities", Summary: "Market depth",
			Description: "Buy/sell order depth (meaningful while the market is open)." + passthroughNote, Params: []param{symbolParam}, Response: kindAny}, sec.MarketDepth()},
		{v1 + "/securities/{symbol}/price-volume", operation{ID: "getPriceVolume", Tag: "Securities", Summary: "Today's price and volume",
			Description: "The security's row from today's price/volume table; NOT_FOUND when it has no data today." + passthroughNote,
			Params:      []param{symbolParam}, Response: kindObject}, sec.PriceVolume()},
		{v1 + "/securities/{symbol}/price-history", operation{ID: "getPriceHistory", Tag: "Securities", Summary: "Daily price and volume history",
			Description: "Up to 500 daily rows. Default range: the last 365 days." + passthroughNote,
			Params: []param{symbolParam,
				{Name: "start_date", In: "query", Description: "Start date, YYYY-MM-DD."},
				{Name: "end_date", In: "query", Description: "End date, YYYY-MM-DD."}}, Response: kindAny}, sec.PriceHistory()},
		{v1 + "/securities/{symbol}/graph", operation{ID: "getSecurityGraph", Tag: "Securities", Summary: "Daily price graph",
			Description: "Intraday price series of a security." + passthroughNote, Params: []param{symbolParam}, Response: kindAny}, sec.Graph()},

		{v1 + "/sectors", operation{ID: "listSectors", Tag: "Securities", Summary: "List sectors",
			Description: "Sector names, including \"Promoter Share\".", Response: kindStrings}, sec.Sectors()},
		{v1 + "/sectors/{sector}/securities", operation{ID: "listSectorSecurities", Tag: "Securities", Summary: "Securities of a sector",
			Description: "Members of a sector; the sector name is matched ignoring case, spaces and punctuation.",
			Params:      []param{{Name: "sector", In: "path", Required: true, Description: "Sector name, e.g. Commercial Banks or commercial-banks."}}, Response: kindObject}, sec.SectorSecurities()},
	}
}
