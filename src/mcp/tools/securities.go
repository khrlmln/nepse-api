package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"nepse-api/src/shared/securities"
)

func registerSecurities(s *mcp.Server, d Deps) {
	sec := d.App.Securities
	bySymbol := func(fn func(context.Context, string) (any, error)) func(context.Context, SymbolInput) (Result, error) {
		return func(ctx context.Context, in SymbolInput) (Result, error) {
			v, err := fn(ctx, in.Symbol)
			return Result{Data: v}, err
		}
	}
	add(s, d, "list_securities", "List or search securities",
		"Lists listed securities (symbol, id, name, ...), optionally filtered by symbol or company name, paginated. Use it to find a symbol from a company name."+dataNote,
		func(ctx context.Context, in ListSecuritiesInput) (Result, error) {
			res, err := sec.List(ctx, securities.ListRequest{Query: in.Query, Page: in.Page, Limit: in.Limit})
			if err != nil {
				return Result{}, err
			}
			return Result{Data: res.Items, Meta: res.Meta()}, nil
		})
	add(s, d, "validate_symbol", "Validate a symbol",
		"Checks that a symbol is a listed security and returns its name. For unknown symbols the error carries suggestions.",
		func(ctx context.Context, in SymbolInput) (Result, error) {
			v, err := sec.Validate(ctx, in.Symbol)
			return Result{Data: v}, err
		})
	add(s, d, "get_security_details", "Security details",
		"Company and security details for a symbol."+dataNote, bySymbol(sec.Details))
	add(s, d, "get_market_depth", "Market depth",
		"Buy and sell order depth for a symbol. Meaningful only while the market is open."+dataNote, bySymbol(sec.MarketDepth))
	add(s, d, "get_security_price_volume", "Today's price and volume",
		"Today's price and volume row for a symbol (last traded price, high, low, previous close, volume, ...). NOT_FOUND when the security has no data today."+dataNote,
		func(ctx context.Context, in SymbolInput) (Result, error) {
			v, err := sec.PriceVolume(ctx, in.Symbol)
			return Result{Data: v}, err
		})
	add(s, d, "get_price_history", "Daily price and volume history",
		"Daily price and volume history of a symbol for technical analysis (default: last 365 days, newest first as returned by NEPSE), paginated."+dataNote,
		func(ctx context.Context, in HistoryInput) (Result, error) {
			v, err := sec.PriceVolumeHistory(ctx, securities.HistoryRequest{Symbol: in.Symbol, StartDate: in.StartDate, EndDate: in.EndDate})
			if err != nil {
				return Result{}, err
			}
			items, meta, err := pageAny(v, in.PageInput)
			return Result{Data: items, Meta: meta}, err
		})
	add(s, d, "get_security_graph", "Security intraday graph",
		"Intraday price time series of a symbol."+dataNote, bySymbol(sec.Graph))
	add(s, d, "list_sectors", "List sectors",
		"Names of all sectors, including Promoter Share.", plain(sec.Sectors))
	add(s, d, "get_sector_securities", "Securities of a sector",
		"Symbols and names of the securities in a sector.",
		func(ctx context.Context, in SectorInput) (Result, error) {
			name, members, err := sec.SectorSecurities(ctx, in.Sector)
			return Result{Data: map[string]any{"sector": name, "securities": members}}, err
		})
}
