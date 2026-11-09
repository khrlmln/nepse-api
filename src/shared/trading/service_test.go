package trading

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/nepse"
)

type fakeUp struct {
	symbol string
	date   time.Time
	page   int
	calls  int
	err    error
}

func (f *fakeUp) FloorSheet(_ context.Context, symbol string, d time.Time, page int) (nepse.FloorSheetPage, error) {
	f.symbol, f.date, f.page = symbol, d, page
	f.calls++
	return nepse.FloorSheetPage{Page: page, TotalPages: 7}, f.err
}

func TestFloorsheetValidationAndPassThrough(t *testing.T) {
	up := &fakeUp{}
	s := New(up)
	ctx := context.Background()
	if p, err := s.Floorsheet(ctx, FloorsheetRequest{}); err != nil || up.symbol != "" || !up.date.IsZero() || up.page != 0 || p.TotalPages != 7 {
		t.Fatalf("%+v %v %+v", p, err, up)
	}
	if _, err := s.Floorsheet(ctx, FloorsheetRequest{Symbol: " nabil ", BusinessDate: "2026-01-15", Page: 3}); err != nil ||
		up.symbol != "NABIL" || up.date.Format(time.DateOnly) != "2026-01-15" || up.page != 3 {
		t.Fatalf("%+v %v", up, err)
	}
	calls := up.calls
	for _, bad := range []FloorsheetRequest{
		{Page: -1}, {BusinessDate: "2026-01-15"} /* date needs a symbol */, {Symbol: "bad symbol"},
		{Symbol: "NABIL", BusinessDate: "15-01-2026"}, {Symbol: "NABIL", BusinessDate: "2026-13-01"},
	} {
		if _, err := s.Floorsheet(ctx, bad); apierrors.Normalize(err).Code != apierrors.CodeInvalidArgument {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if up.calls != calls {
		t.Fatal("invalid requests must never reach upstream")
	}
	up.err = apierrors.UpstreamUnavailable(errors.New("x"))
	if _, err := s.Floorsheet(ctx, FloorsheetRequest{}); apierrors.Normalize(err).Code != apierrors.CodeUpstreamUnavailable {
		t.Fatal(err)
	}
}

func TestMetaOfOmitsUnreportedTotals(t *testing.T) {
	full := MetaOf(nepse.FloorSheetPage{Page: 2, TotalPages: 5, TotalElements: 2400})
	if full.Page != 2 || full.PageSize != nepse.FloorSheetPageSize || full.TotalPages == nil || *full.TotalPages != 5 || full.TotalElements == nil || *full.TotalElements != 2400 {
		t.Fatalf("%+v", full)
	}
	unknown := MetaOf(nepse.FloorSheetPage{Page: 1, TotalPages: -1, TotalElements: -1})
	b, _ := json.Marshal(unknown)
	if string(b) != `{"page":1,"page_size":500}` {
		t.Fatalf("unreported totals must be omitted, got %s", b)
	}
	zero := MetaOf(nepse.FloorSheetPage{TotalPages: 0, TotalElements: 0})
	if zero.TotalPages == nil || zero.TotalElements == nil {
		t.Fatal("a reported total of 0 must be kept")
	}
}
