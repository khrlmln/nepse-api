package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"nepse-api/src/shared/market"
)

const dataNote = " Field names inside items are defined by NEPSE and returned unchanged. Data is delayed/unofficial and not investment advice."

// plain adapts a no-input use case.
func plain[T any](fn func(context.Context) (T, error)) func(context.Context, NoInput) (Result, error) {
	return func(ctx context.Context, _ NoInput) (Result, error) {
		v, err := fn(ctx)
		return Result{Data: v}, err
	}
}

func registerMarket(s *mcp.Server, d Deps) {
	m := d.App.Market
	add(s, d, "get_market_summary", "Market summary",
		"Headline figures for the trading day (total turnover, traded shares, transactions, scrips traded) as a label-to-value object.", plain(m.Summary))
	add(s, d, "get_market_status", "Market status",
		"Whether the NEPSE market is currently open. Check this before interpreting live data; NEPSE trades Sunday to Thursday, about 11:00-15:00 Nepal time.", plain(m.Status))
	add(s, d, "get_nepse_indices", "NEPSE indices",
		"Current value and change of the NEPSE indices (NEPSE, Sensitive, Float, ...) keyed by index name."+dataNote, plain(m.Index))
	add(s, d, "get_sub_indices", "Sector sub-indices",
		"Current value and change of every sector sub-index (banking, hydropower, ...) keyed by name."+dataNote, plain(m.SubIndices))
	add(s, d, "get_live_market", "Live market table",
		"Live trading data for all listed securities, paginated. Meaningful while the market is open."+dataNote,
		func(ctx context.Context, in LiveMarketInput) (Result, error) {
			v, err := m.Live(ctx)
			if err != nil {
				return Result{}, err
			}
			items, meta, err := pageAny(any(v), in.PageInput)
			return Result{Data: items, Meta: meta}, err
		})
	for _, t := range []struct {
		name, title, what string
		kind              market.TopKind
	}{
		{"get_top_gainers", "Top gainers", "securities with the largest price gain today", market.TopGainers},
		{"get_top_losers", "Top losers", "securities with the largest price loss today", market.TopLosers},
		{"get_top_turnover", "Top turnover", "securities with the highest traded turnover today", market.TopTurnover},
		{"get_top_transactions", "Top transactions", "securities with the most transactions today", market.TopTransactions},
		{"get_top_traded_shares", "Top traded shares", "securities with the most shares traded today", market.TopTrades},
	} {
		kind := t.kind
		add(s, d, t.name, t.title, "Top ten "+t.what+"."+dataNote, func(ctx context.Context, _ NoInput) (Result, error) {
			v, err := m.Top(ctx, kind)
			return Result{Data: v}, err
		})
	}
	add(s, d, "get_supply_demand", "Supply and demand",
		"Aggregate supply and demand table across securities."+dataNote, plain(m.SupplyDemand))
	add(s, d, "get_index_graph", "Index intraday graph",
		"Intraday time series of a main index, useful for trend and volatility analysis."+dataNote,
		func(ctx context.Context, in IndexGraphInput) (Result, error) {
			v, err := m.IndexGraph(ctx, in.Name)
			return Result{Data: v}, err
		})
	add(s, d, "get_sector_graph", "Sector sub-index intraday graph",
		"Intraday time series of a sector sub-index."+dataNote,
		func(ctx context.Context, in SectorGraphInput) (Result, error) {
			v, err := m.SubIndexGraph(ctx, in.Sector)
			return Result{Data: v}, err
		})
	add(s, d, "get_turnover_overview", "Per-scrip trade statistics with sector totals",
		"Combines today's top gainers/losers with turnover, transactions, volume and previous close per scrip (scripsDetails), plus totals per sector joined with the sector sub-index (sectorsDetails). Only scrips that appear among the top gainers or losers are included.", plain(m.TurnoverOverview))
}
