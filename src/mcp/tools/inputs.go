package tools

import (
	"nepse-api/src/shared/validation"
)

// Input types. The jsonschema tags become the descriptions the model sees.

// NoInput is for tools without parameters.
type NoInput struct{}

// PageInput is optional 1-based pagination.
type PageInput struct {
	Page  int `json:"page,omitempty" jsonschema:"1-based page number (default 1)"`
	Limit int `json:"limit,omitempty" jsonschema:"items per page (default 50, max 500)"`
}

// SymbolInput selects one security.
type SymbolInput struct {
	Symbol string `json:"symbol" jsonschema:"NEPSE security symbol, for example NABIL"`
}

// ListSecuritiesInput filters and paginates the securities list.
type ListSecuritiesInput struct {
	Query string `json:"query,omitempty" jsonschema:"optional case-insensitive filter on symbol or company name"`
	PageInput
}

// LiveMarketInput paginates the live market table.
type LiveMarketInput struct{ PageInput }

// HistoryInput asks for daily price/volume history.
type HistoryInput struct {
	Symbol    string `json:"symbol" jsonschema:"NEPSE security symbol, for example NABIL"`
	StartDate string `json:"start_date,omitempty" jsonschema:"start date YYYY-MM-DD (default: 365 days before end_date)"`
	EndDate   string `json:"end_date,omitempty" jsonschema:"end date YYYY-MM-DD (default: today)"`
	PageInput
}

// IndexGraphInput selects a main index graph.
type IndexGraphInput struct {
	Name string `json:"name,omitempty" jsonschema:"index graph: nepse, sensitive, float or sensitive-float (default nepse)"`
}

// SectorGraphInput selects a sector sub-index graph.
type SectorGraphInput struct {
	Sector string `json:"sector" jsonschema:"sector slug, for example banking, hydropower, life-insurance"`
}

// SectorInput selects a sector by name.
type SectorInput struct {
	Sector string `json:"sector" jsonschema:"sector name, for example Commercial Banks (spaces and case are ignored)"`
}

// FloorsheetInput selects one floorsheet page.
type FloorsheetInput struct {
	Symbol       string `json:"symbol,omitempty" jsonschema:"optional symbol; omit for the whole market"`
	BusinessDate string `json:"business_date,omitempty" jsonschema:"YYYY-MM-DD, only with a symbol; must match the latest available session"`
	Page         int    `json:"page,omitempty" jsonschema:"zero-based page index, 500 rows per page (default 0)"`
}

// pageAny paginates a JSON array result; other shapes pass through unchanged.
func pageAny(data any, in PageInput) (any, any, error) {
	items, ok := data.([]any)
	if !ok {
		return data, nil, nil
	}
	p, err := validation.NewPage(in.Page, in.Limit)
	if err != nil {
		return nil, nil, err
	}
	res := validation.Paginate(items, p)
	return res.Items, res.Meta(), nil
}
