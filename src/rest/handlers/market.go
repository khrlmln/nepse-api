package handlers

import (
	"context"

	"nepse-api/src/rest/middleware"
	"nepse-api/src/shared/market"
)

// Market serves the market-wide endpoints.
type Market struct{ Svc *market.Service }

func (h Market) Summary() middleware.HandlerFunc      { return noInput(h.Svc.Summary) }
func (h Market) Status() middleware.HandlerFunc       { return noInput(h.Svc.Status) }
func (h Market) Index() middleware.HandlerFunc        { return noInput(h.Svc.Index) }
func (h Market) SubIndices() middleware.HandlerFunc   { return noInput(h.Svc.SubIndices) }
func (h Market) Live() middleware.HandlerFunc         { return noInput(h.Svc.Live) }
func (h Market) SupplyDemand() middleware.HandlerFunc { return noInput(h.Svc.SupplyDemand) }
func (h Market) TurnoverOverview() middleware.HandlerFunc {
	return noInput(h.Svc.TurnoverOverview)
}

// Top serves one top-ten list.
func (h Market) Top(kind market.TopKind) middleware.HandlerFunc {
	return noInput(func(ctx context.Context) ([]any, error) { return h.Svc.Top(ctx, kind) })
}

// IndexGraph serves /market/graphs/indices/{name}.
func (h Market) IndexGraph() middleware.HandlerFunc { return byParam("name", h.Svc.IndexGraph) }

// SubIndexGraph serves /market/graphs/sectors/{sector}.
func (h Market) SubIndexGraph() middleware.HandlerFunc { return byParam("sector", h.Svc.SubIndexGraph) }
