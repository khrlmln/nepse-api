// Package market holds the market-wide use cases (summary, indices, live data, top lists, graphs). Both the REST and MCP services call it.
package market

import (
	"context"
	"fmt"
	"sort"
	"strings"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/nepse"
	"nepse-api/src/shared/parallel"
)

// Upstream is the part of the NEPSE client this package needs.
type Upstream interface {
	Get(ctx context.Context, ep nepse.Endpoint) (any, error)
	Graph(ctx context.Context, ep nepse.Endpoint) (any, error)
	CompanyList(ctx context.Context) ([]map[string]any, error)
}

type Service struct{ up Upstream }

func New(up Upstream) *Service { return &Service{up: up} }

// Summary returns the market summary as {label: value}.
func (s *Service) Summary(ctx context.Context) (map[string]any, error) {
	v, err := s.up.Get(ctx, nepse.EpSummary)
	if err != nil {
		return nil, err
	}
	items, err := nepse.ExpectObjects(v, "market summary")
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(items))
	for _, item := range items {
		if key, ok := item["detail"].(string); ok && key != "" {
			out[key] = item["value"]
		}
	}
	return out, nil
}

func (s *Service) indexMap(ctx context.Context, ep nepse.Endpoint, what string) (map[string]any, error) {
	v, err := s.up.Get(ctx, ep)
	if err != nil {
		return nil, err
	}
	items, err := nepse.ExpectObjects(v, what)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(items))
	for _, item := range items {
		if key, ok := item["index"].(string); ok && key != "" {
			out[key] = item
		}
	}
	return out, nil
}

// Index returns the NEPSE indices keyed by index name.
func (s *Service) Index(ctx context.Context) (map[string]any, error) {
	return s.indexMap(ctx, nepse.EpIndex, "NEPSE index")
}

// SubIndices returns the sector sub-indices keyed by name.
func (s *Service) SubIndices(ctx context.Context) (map[string]any, error) {
	return s.indexMap(ctx, nepse.EpSubIndices, "NEPSE sub-indices")
}

// Status returns the market-open status object.
func (s *Service) Status(ctx context.Context) (map[string]any, error) {
	v, err := s.up.Get(ctx, nepse.EpMarketStatus)
	if err != nil {
		return nil, err
	}
	return nepse.ExpectObject(v, "market status")
}

// Live returns the live market table.
func (s *Service) Live(ctx context.Context) ([]any, error) {
	v, err := s.up.Get(ctx, nepse.EpLiveMarket)
	if err != nil {
		return nil, err
	}
	return nepse.ExpectArray(v, "live market")
}

// TopKind selects a top-ten list.
type TopKind string

const (
	TopGainers      TopKind = "gainers"
	TopLosers       TopKind = "losers"
	TopTurnover     TopKind = "turnover"
	TopTransactions TopKind = "transactions"
	TopTrades       TopKind = "trades"
)

var topEndpoints = map[TopKind]nepse.Endpoint{
	TopGainers: nepse.EpTopGainers, TopLosers: nepse.EpTopLosers, TopTurnover: nepse.EpTopTurnover,
	TopTransactions: nepse.EpTopTransaction, TopTrades: nepse.EpTopTrade,
}

// Top returns one of the top lists.
func (s *Service) Top(ctx context.Context, kind TopKind) ([]any, error) {
	ep, ok := topEndpoints[kind]
	if !ok {
		return nil, apierrors.InvalidArgument("Unknown top-list kind.")
	}
	v, err := s.up.Get(ctx, ep)
	if err != nil {
		return nil, err
	}
	return nepse.ExpectArray(v, "top "+string(kind))
}

// SupplyDemand returns the supply/demand table.
func (s *Service) SupplyDemand(ctx context.Context) (any, error) {
	v, err := s.up.Get(ctx, nepse.EpSupplyDemand)
	if err != nil {
		return nil, err
	}
	return nepse.ExpectCollection(v, "supply and demand")
}

// IndexGraphNames lists the accepted main-index graph names.
func IndexGraphNames() []string { return sortedKeys(nepse.IndexGraphs) }

// SubIndexGraphNames lists the accepted sector slugs.
func SubIndexGraphNames() []string { return sortedKeys(nepse.SubIndexGraphs) }

func sortedKeys(m map[string]nepse.Endpoint) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Service) graph(ctx context.Context, table map[string]nepse.Endpoint, field, name string, allowed []string) (any, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	ep, ok := table[name]
	if !ok {
		return nil, apierrors.InvalidArgument(fmt.Sprintf("%s must be one of: %s.", field, strings.Join(allowed, ", ")))
	}
	v, err := s.up.Graph(ctx, ep)
	if err != nil {
		return nil, err
	}
	return nepse.ExpectCollection(v, field+" graph")
}

// IndexGraph returns a main-index daily graph (nepse, sensitive, float, sensitive-float).
func (s *Service) IndexGraph(ctx context.Context, name string) (any, error) {
	if strings.TrimSpace(name) == "" {
		name = "nepse"
	}
	return s.graph(ctx, nepse.IndexGraphs, "index", name, IndexGraphNames())
}

// SubIndexGraph returns a sector sub-index daily graph.
func (s *Service) SubIndexGraph(ctx context.Context, sector string) (any, error) {
	return s.graph(ctx, nepse.SubIndexGraphs, "sector", sector, SubIndexGraphNames())
}

// Per-scrip trade statistics and per-sector totals joined with the sub-index. Like the original it lists only scrips present in the top gainers or losers with a non-zero previous close. The eight upstream collections it needs are fetched concurrently.
func (s *Service) TurnoverOverview(ctx context.Context) (any, error) {
	endpoints := []nepse.Endpoint{nepse.EpCompanyList, nepse.EpTopTurnover, nepse.EpTopTransaction, nepse.EpTopTrade,
		nepse.EpTopGainers, nepse.EpTopLosers, nepse.EpPriceVolume, nepse.EpSubIndices}
	data := make([]any, len(endpoints))
	fetches := make([]func(context.Context) error, len(endpoints))
	for i, ep := range endpoints {
		fetches[i] = func(ctx context.Context) (err error) {
			data[i], err = s.up.Get(ctx, ep)
			return err
		}
	}
	if err := parallel.Do(ctx, fetches...); err != nil {
		return nil, err
	}
	return buildTurnoverOverview(overviewInput{
		companies: data[0], turnover: data[1], transactions: data[2], trades: data[3],
		gainers: data[4], losers: data[5], priceVolume: data[6], subIndices: data[7],
	})
}
