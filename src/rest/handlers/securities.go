package handlers

import (
	"net/http"

	"nepse-api/src/rest/middleware"
	"nepse-api/src/shared/securities"
)

// Securities serves the per-security and sector endpoints.
type Securities struct{ Svc *securities.Service }

func (h Securities) List() middleware.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		page, err := intParam(r, "page")
		if err != nil {
			return err
		}
		limit, err := intParam(r, "limit")
		if err != nil {
			return err
		}
		res, err := h.Svc.List(r.Context(), securities.ListRequest{Query: r.URL.Query().Get("q"), Page: page, Limit: limit})
		if err != nil {
			return err
		}
		middleware.OK(w, r, res.Items, res.Meta())
		return nil
	}
}

func (h Securities) Validate() middleware.HandlerFunc    { return bySymbol(h.Svc.Validate) }
func (h Securities) Details() middleware.HandlerFunc     { return bySymbol(h.Svc.Details) }
func (h Securities) MarketDepth() middleware.HandlerFunc { return bySymbol(h.Svc.MarketDepth) }
func (h Securities) PriceVolume() middleware.HandlerFunc { return bySymbol(h.Svc.PriceVolume) }
func (h Securities) Graph() middleware.HandlerFunc       { return bySymbol(h.Svc.Graph) }

func (h Securities) PriceHistory() middleware.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		v, err := h.Svc.PriceVolumeHistory(r.Context(), securities.HistoryRequest{
			Symbol: r.PathValue("symbol"), StartDate: q.Get("start_date"), EndDate: q.Get("end_date"),
		})
		if err != nil {
			return err
		}
		middleware.OK(w, r, v, nil)
		return nil
	}
}

func (h Securities) Sectors() middleware.HandlerFunc { return noInput(h.Svc.Sectors) }

// SectorMembers is the body of /sectors/{sector}/securities.
type SectorMembers struct {
	Sector     string                    `json:"sector"`
	Securities []securities.SectorMember `json:"securities"`
}

func (h Securities) SectorSecurities() middleware.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		name, members, err := h.Svc.SectorSecurities(r.Context(), r.PathValue("sector"))
		if err != nil {
			return err
		}
		middleware.OK(w, r, SectorMembers{Sector: name, Securities: members}, nil)
		return nil
	}
}
