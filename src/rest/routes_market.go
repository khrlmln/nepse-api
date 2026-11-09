package rest

import (
	"nepse-api/src/rest/handlers"
	"nepse-api/src/shared/market"
)

// marketRoutes are the market-wide endpoints.
func marketRoutes(mk handlers.Market) []route {
	const v1 = apiV1
	rs := []route{
		{v1 + "/market/summary", operation{ID: "getMarketSummary", Tag: "Market", Summary: "Market summary",
			Description: "Headline figures of the day (turnover, traded shares, transactions, scrips traded) as a label-to-value object.", Response: kindObject}, mk.Summary()},
		{v1 + "/market/status", operation{ID: "getMarketStatus", Tag: "Market", Summary: "Market open/closed status",
			Description: "Whether the market is open, as reported by NEPSE.", Response: kindObject}, mk.Status()},
		{v1 + "/market/index", operation{ID: "getNepseIndex", Tag: "Market", Summary: "NEPSE indices",
			Description: "The NEPSE indices keyed by index name." + passthroughNote, Response: kindObject}, mk.Index()},
		{v1 + "/market/sub-indices", operation{ID: "getSubIndices", Tag: "Market", Summary: "Sector sub-indices",
			Description: "The sector sub-indices keyed by name." + passthroughNote, Response: kindObject}, mk.SubIndices()},
		{v1 + "/market/live", operation{ID: "getLiveMarket", Tag: "Market", Summary: "Live market table",
			Description: "Live trading data for all securities (meaningful while the market is open)." + passthroughNote, Response: kindArray}, mk.Live()},
		{v1 + "/market/supply-demand", operation{ID: "getSupplyDemand", Tag: "Market", Summary: "Supply and demand",
			Description: "Aggregate supply/demand table." + passthroughNote, Response: kindAny}, mk.SupplyDemand()},
		{v1 + "/market/turnover-overview", operation{ID: "getTurnoverOverview", Tag: "Market", Summary: "Per-scrip trade statistics with sector totals",
			Description: "Ports the original TradeTurnoverTransactionSubindices endpoint: scripsDetails (securities among today's top gainers/losers with a non-zero previous close, with turnover, transactions, volume) and sectorsDetails (totals per sector joined with its sub-index).", Response: kindObject}, mk.TurnoverOverview()},
	}
	rs = append(rs, topRoutes(mk)...)
	return append(rs,
		route{v1 + "/market/graphs/indices/{name}", operation{ID: "getIndexGraph", Tag: "Market", Summary: "Daily index graph",
			Description: "Intraday series of a main index." + passthroughNote,
			Params:      []param{{Name: "name", In: "path", Required: true, Description: "Index graph name.", Enum: market.IndexGraphNames()}}, Response: kindAny}, mk.IndexGraph()},
		route{v1 + "/market/graphs/sectors/{sector}", operation{ID: "getSubIndexGraph", Tag: "Market", Summary: "Daily sector sub-index graph",
			Description: "Intraday series of a sector sub-index." + passthroughNote,
			Params:      []param{{Name: "sector", In: "path", Required: true, Description: "Sector slug.", Enum: market.SubIndexGraphNames()}}, Response: kindAny}, mk.SubIndexGraph()},
	)
}

// topRoutes are the five top-ten lists, which differ only in kind and wording.
func topRoutes(mk handlers.Market) []route {
	tops := []struct {
		kind market.TopKind
		path string
		name string
	}{
		{market.TopGainers, "top-gainers", "gainers"}, {market.TopLosers, "top-losers", "losers"},
		{market.TopTurnover, "top-turnover", "turnover"}, {market.TopTransactions, "top-transactions", "transactions"},
		{market.TopTrades, "top-trades", "traded shares"},
	}
	rs := make([]route, 0, len(tops))
	for _, t := range tops {
		rs = append(rs, route{apiV1 + "/market/" + t.path, operation{ID: "getTop" + title(string(t.kind)), Tag: "Market",
			Summary: "Top ten by " + t.name, Description: "Today's top ten securities by " + t.name + "." + passthroughNote, Response: kindArray}, mk.Top(t.kind)})
	}
	return rs
}
