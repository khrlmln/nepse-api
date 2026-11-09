// Package trading holds the trade-level use cases (floorsheet).
package trading

import (
	"context"
	"time"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/nepse"
	"nepse-api/src/shared/validation"
)

// Upstream is the part of the NEPSE client this package needs.
type Upstream interface {
	FloorSheet(ctx context.Context, symbol string, businessDate time.Time, page int) (nepse.FloorSheetPage, error)
}

type Service struct{ up Upstream }

func New(up Upstream) *Service { return &Service{up: up} }

// FloorsheetRequest selects one page (zero-based, 500 rows) of a floorsheet: the whole market (empty Symbol) or one symbol filtered from the latest available session. BusinessDate, when supplied for a symbol, must match that latest session; NEPSE does not expose historical per-symbol floorsheets.
type FloorsheetRequest struct {
	Symbol       string
	BusinessDate string
	Page         int
}

// Floorsheet returns one page of the floorsheet.
func (s *Service) Floorsheet(ctx context.Context, req FloorsheetRequest) (nepse.FloorSheetPage, error) {
	page, err := validation.FloorPage(req.Page)
	if err != nil {
		return nepse.FloorSheetPage{}, err
	}
	date, err := validation.Date("business_date", req.BusinessDate)
	if err != nil {
		return nepse.FloorSheetPage{}, err
	}
	symbol := ""
	if req.Symbol != "" {
		if symbol, err = validation.Symbol(req.Symbol); err != nil {
			return nepse.FloorSheetPage{}, err
		}
	} else if !date.IsZero() {
		return nepse.FloorSheetPage{}, apierrors.InvalidArgument("business_date is supported only for a single symbol's floorsheet.")
	}
	return s.up.FloorSheet(ctx, symbol, date, page)
}

// PageMeta describes one returned floorsheet page for both transports. Totals are omitted when NEPSE did not report them.
type PageMeta struct {
	Page          int    `json:"page"`
	PageSize      int    `json:"page_size"`
	TotalPages    *int   `json:"total_pages,omitempty"`
	TotalElements *int64 `json:"total_elements,omitempty"`
}

// MetaOf builds the metadata of a floorsheet page.
func MetaOf(p nepse.FloorSheetPage) PageMeta {
	meta := PageMeta{Page: p.Page, PageSize: nepse.FloorSheetPageSize}
	if p.TotalPages >= 0 {
		meta.TotalPages = &p.TotalPages
	}
	if p.TotalElements >= 0 {
		meta.TotalElements = &p.TotalElements
	}
	return meta
}
