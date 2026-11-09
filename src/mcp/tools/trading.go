package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"nepse-api/src/shared/trading"
)

func registerTrading(s *mcp.Server, d Deps) {
	add(s, d, "get_floorsheet", "Floorsheet page",
		"One page (500 trades, zero-based) of the floorsheet: every executed trade with buyer/seller broker, quantity and rate. Whole market by default, or one symbol filtered from the latest available session. A symbol query scans the bounded all-market session pages and may take longer. NEPSE does not expose historical per-symbol floorsheets; an optional business_date must match the latest session. The result meta reports total_pages; request further pages with the page parameter."+dataNote,
		func(ctx context.Context, in FloorsheetInput) (Result, error) {
			res, err := d.App.Trading.Floorsheet(ctx, trading.FloorsheetRequest{Symbol: in.Symbol, BusinessDate: in.BusinessDate, Page: in.Page})
			if err != nil {
				return Result{}, err
			}
			return Result{Data: res.Rows, Meta: trading.MetaOf(res)}, nil
		})
}
