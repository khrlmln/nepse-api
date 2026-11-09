// Package securities holds the per-security use cases (listing, validation, details, depth, history, graphs, sectors). Both the REST and MCP services call it.
package securities

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/nepse"
	"nepse-api/src/shared/parallel"
	"nepse-api/src/shared/validation"
)

// Upstream is the part of the NEPSE client this package needs.
type Upstream interface {
	Get(ctx context.Context, ep nepse.Endpoint) (any, error)
	SecurityList(ctx context.Context) ([]map[string]any, error)
	CompanyList(ctx context.Context) ([]map[string]any, error)
	CompanyDetails(ctx context.Context, symbol string) (any, error)
	DailyScripGraph(ctx context.Context, symbol string) (any, error)
	MarketDepth(ctx context.Context, symbol string) (any, error)
	PriceVolumeHistory(ctx context.Context, symbol string, start, end time.Time) (any, error)
	Today() time.Time
}

type Service struct{ up Upstream }

func New(up Upstream) *Service { return &Service{up: up} }

const (
	promoterShare  = "Promoter Share"
	maxSuggestions = 5
)

// ListRequest is a search + pagination request for the securities list.
type ListRequest struct {
	Query string
	Page  int
	Limit int
}

// List returns securities, optionally filtered by a case-insensitive match on the symbol or name, one page at a time.
func (s *Service) List(ctx context.Context, req ListRequest) (validation.Paged[map[string]any], error) {
	q, err := validation.Query(req.Query)
	if err != nil {
		return validation.Paged[map[string]any]{}, err
	}
	page, err := validation.NewPage(req.Page, req.Limit)
	if err != nil {
		return validation.Paged[map[string]any]{}, err
	}
	all, err := s.up.SecurityList(ctx)
	if err != nil {
		return validation.Paged[map[string]any]{}, err
	}
	if q != "" {
		needle := strings.ToLower(q)
		filtered := make([]map[string]any, 0, len(all))
		for _, m := range all {
			if strings.Contains(strings.ToLower(nepse.Str(m, "symbol")), needle) ||
				strings.Contains(strings.ToLower(nepse.Str(m, "securityName")), needle) {
				filtered = append(filtered, m)
			}
		}
		all = filtered
	}
	return validation.Paginate(all, page), nil
}

// SymbolInfo is the result of a successful symbol validation.
type SymbolInfo struct {
	Symbol string `json:"symbol"`
	Name   string `json:"name,omitempty"`
	Valid  bool   `json:"valid"`
}

// Validate checks a symbol against the live security list. Unknown symbols return NOT_FOUND carrying up to five suggestions.
func (s *Service) Validate(ctx context.Context, rawSymbol string) (SymbolInfo, error) {
	symbol, err := validation.Symbol(rawSymbol)
	if err != nil {
		return SymbolInfo{}, err
	}
	all, err := s.up.SecurityList(ctx)
	if err != nil {
		return SymbolInfo{}, err
	}
	for _, m := range all {
		if strings.EqualFold(nepse.Str(m, "symbol"), symbol) {
			return SymbolInfo{Symbol: symbol, Name: nepse.Str(m, "securityName"), Valid: true}, nil
		}
	}
	return SymbolInfo{}, apierrors.NotFound(fmt.Sprintf("Symbol %q was not found.", symbol)).
		WithDetails(map[string]any{"suggestions": suggestions(all, symbol)})
}

func suggestions(all []map[string]any, symbol string) []string {
	prefix := symbol
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	out := []string{}
	for _, m := range all {
		if sym := strings.ToUpper(nepse.Str(m, "symbol")); sym != "" && strings.HasPrefix(sym, prefix) {
			out = append(out, sym)
		}
	}
	sort.Strings(out)
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

// forSymbol validates the raw symbol, runs fn and checks the result is a JSON collection: the pattern shared by every per-symbol lookup.
func (s *Service) forSymbol(ctx context.Context, rawSymbol, what string, fn func(context.Context, string) (any, error)) (any, error) {
	symbol, err := validation.Symbol(rawSymbol)
	if err != nil {
		return nil, err
	}
	v, err := fn(ctx, symbol)
	if err != nil {
		return nil, err
	}
	return nepse.ExpectCollection(v, what)
}

// Details returns the company details of a symbol.
func (s *Service) Details(ctx context.Context, rawSymbol string) (any, error) {
	return s.forSymbol(ctx, rawSymbol, "company details", s.up.CompanyDetails)
}

// MarketDepth returns the buy/sell depth of a symbol (meaningful while the market is open).
func (s *Service) MarketDepth(ctx context.Context, rawSymbol string) (any, error) {
	return s.forSymbol(ctx, rawSymbol, "market depth", s.up.MarketDepth)
}

// Graph returns the daily intraday price graph of a symbol.
func (s *Service) Graph(ctx context.Context, rawSymbol string) (any, error) {
	return s.forSymbol(ctx, rawSymbol, "daily graph", s.up.DailyScripGraph)
}

// PriceVolume returns today's price/volume row of a symbol from the live table.
func (s *Service) PriceVolume(ctx context.Context, rawSymbol string) (map[string]any, error) {
	symbol, err := validation.Symbol(rawSymbol)
	if err != nil {
		return nil, err
	}
	v, err := s.up.Get(ctx, nepse.EpPriceVolume)
	if err != nil {
		return nil, err
	}
	rows, err := nepse.ExpectObjects(v, "price volume")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if strings.EqualFold(nepse.Str(row, "symbol"), symbol) {
			return row, nil
		}
	}
	return nil, apierrors.NotFound(fmt.Sprintf("No price and volume data is available for %q right now.", symbol))
}

// HistoryRequest asks for daily history of one symbol (dates are YYYY-MM-DD, both optional).
type HistoryRequest struct{ Symbol, StartDate, EndDate string }

// PriceVolumeHistory returns up to 500 daily rows (default: the last 365 days).
func (s *Service) PriceVolumeHistory(ctx context.Context, req HistoryRequest) (any, error) {
	symbol, err := validation.Symbol(req.Symbol)
	if err != nil {
		return nil, err
	}
	start, end, err := validation.DateRange(req.StartDate, req.EndDate, s.up.Today())
	if err != nil {
		return nil, err
	}
	v, err := s.up.PriceVolumeHistory(ctx, symbol, start, end)
	if err != nil {
		return nil, err
	}
	return nepse.ExpectCollection(v, "price history")
}

// Sectors lists every sector name, including "Promoter Share" for securities that have no company entry.
func (s *Service) Sectors(ctx context.Context) ([]string, error) {
	groups, err := s.groups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(groups))
	for name := range groups {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// SectorMember is one security of a sector.
type SectorMember struct {
	Symbol string `json:"symbol"`
	Name   string `json:"name,omitempty"`
}

// SectorSecurities returns the members of a sector (name matched ignoring case, spaces and punctuation, so "commercial-banks" works).
func (s *Service) SectorSecurities(ctx context.Context, sector string) (string, []SectorMember, error) {
	key := sectorKey(sector)
	if key == "" {
		return "", nil, apierrors.InvalidArgument("The sector is required.")
	}
	groups, err := s.groups(ctx)
	if err != nil {
		return "", nil, err
	}
	for name, members := range groups {
		if sectorKey(name) == key {
			return name, members, nil
		}
	}
	return "", nil, apierrors.NotFound(fmt.Sprintf("Sector %q was not found.", strings.TrimSpace(sector)))
}

func sectorKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// groups maps sector name -> members, in security-list order within a sector.
func (s *Service) groups(ctx context.Context) (map[string][]SectorMember, error) {
	var companies, securities []map[string]any
	err := parallel.Do(ctx,
		func(ctx context.Context) (err error) { companies, err = s.up.CompanyList(ctx); return },
		func(ctx context.Context) (err error) { securities, err = s.up.SecurityList(ctx); return },
	)
	if err != nil {
		return nil, err
	}
	sectorOf := make(map[string]string, len(companies))
	for _, c := range companies {
		if sym := strings.ToUpper(strings.TrimSpace(nepse.Str(c, "symbol"))); sym != "" {
			sectorOf[sym] = nepse.Str(c, "sectorName")
		}
	}
	out := map[string][]SectorMember{}
	for _, sec := range securities {
		sym := strings.ToUpper(strings.TrimSpace(nepse.Str(sec, "symbol")))
		if sym == "" {
			continue
		}
		name := sectorOf[sym]
		if name == "" {
			name = promoterShare
		}
		out[name] = append(out[name], SectorMember{Symbol: sym, Name: nepse.Str(sec, "securityName")})
	}
	return out, nil
}
