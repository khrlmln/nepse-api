package handlers

import (
	"net/http"

	"nepse-api/src/rest/middleware"
	"nepse-api/src/shared/trading"
)

// Trading serves the floorsheet endpoints.
type Trading struct{ Svc *trading.Service }

// Floorsheet serves /trading/floorsheet and /trading/floorsheet/{symbol}.
func (h Trading) Floorsheet() middleware.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		page, err := intParam(r, "page")
		if err != nil {
			return err
		}
		res, err := h.Svc.Floorsheet(r.Context(), trading.FloorsheetRequest{
			Symbol: r.PathValue("symbol"), BusinessDate: r.URL.Query().Get("business_date"), Page: page,
		})
		if err != nil {
			return err
		}
		middleware.OK(w, r, res.Rows, trading.MetaOf(res))
		return nil
	}
}
