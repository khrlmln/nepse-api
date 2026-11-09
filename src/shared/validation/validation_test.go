package validation

import (
	"math"
	"strings"
	"testing"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

func TestSymbol(t *testing.T) {
	for in, want := range map[string]string{"nabil": "NABIL", " NICA ": "NICA", "C30MF": "C30MF", "a.b-c_d": "A.B-C_D"} {
		if got, err := Symbol(in); err != nil || got != want {
			t.Errorf("%q -> %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "  ", "NA BIL", "NABIL;DROP", "../etc", "ÄBC", "A\nB", "THISSYMBOLISWAYTOOLONGFORNEPSE", "-X"} {
		if _, err := Symbol(bad); apierrors.Normalize(err).Code != apierrors.CodeInvalidArgument {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDates(t *testing.T) {
	if d, err := Date("x", ""); err != nil || !d.IsZero() {
		t.Fatal("empty must be zero")
	}
	for _, bad := range []string{"2026-13-01", "2026-02-30", "01-02-2026", "20260101", "2026-1-1"} {
		if _, err := Date("x", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	today := time.Date(2026, 10, 15, 9, 30, 0, 0, time.FixedZone("npt", 5*3600+45*60))
	if _, _, err := DateRange("2026-05-01", "2026-01-01", today); err == nil {
		t.Fatal("reversed range accepted")
	}
	if _, _, err := DateRange("2026-01-01", "2026-01-01", today); err != nil {
		t.Fatal(err)
	}
	// defaults are applied BEFORE the order check
	if _, _, err := DateRange("2999-01-01", "", today); err == nil {
		t.Fatal("start after the defaulted end (today) was accepted")
	}
	s, e, err := DateRange("", "", today)
	if err != nil || e.Format(time.DateOnly) != "2026-10-15" || s.Format(time.DateOnly) != "2025-10-15" {
		t.Fatalf("defaults: %v %v %v", s, e, err)
	}
	s, e, err = DateRange("", "2020-01-01", today)
	if err != nil || s.Format(time.DateOnly) != "2019-01-01" {
		t.Fatalf("start must default relative to end_date: %v %v %v", s, e, err)
	}
}

func TestPaging(t *testing.T) {
	if p, err := NewPage(0, 0); err != nil || p.Page != 1 || p.Limit != DefaultLimit {
		t.Fatalf("%+v %v", p, err)
	}
	for _, c := range [][2]int{{-1, 10}, {1, -5}, {1, MaxLimit + 1}} {
		if _, err := NewPage(c[0], c[1]); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
	items := []int{1, 2, 3, 4, 5}
	p, _ := NewPage(3, 2)
	if got := Paginate(items, p); len(got.Items) != 1 || got.Items[0] != 5 || got.Total != 5 {
		t.Fatalf("%+v", got)
	}
	p, _ = NewPage(99, 2)
	if got := Paginate(items, p); len(got.Items) != 0 || got.Total != 5 {
		t.Fatalf("beyond end: %+v", got)
	}
	if got := Paginate([]int(nil), Page{1, 10}); got.Total != 0 || len(got.Items) != 0 {
		t.Fatalf("nil: %+v", got)
	}
}

func TestPaginationCannotOverflowOrPanic(t *testing.T) {
	items := []int{1, 2, 3}
	for _, page := range []int{math.MaxInt, math.MaxInt - 1, math.MaxInt/2 + 1, 1 << 62, MaxPage + 1, 0, -5} {
		for _, limit := range []int{1, 500, math.MaxInt, 0, -1} {
			got := Paginate(items, Page{Page: page, Limit: limit}) // must not panic
			if len(got.Items) > len(items) || got.Total != 3 {
				t.Fatalf("page=%d limit=%d: %+v", page, limit, got)
			}
		}
	}
	if _, err := NewPage(MaxPage+1, 10); err == nil {
		t.Fatal("page above MaxPage accepted")
	}
	if _, err := NewPage(math.MaxInt, 10); err == nil {
		t.Fatal("MaxInt page accepted")
	}
	if p, err := NewPage(MaxPage, 500); err != nil || len(Paginate(items, p).Items) != 0 {
		t.Fatalf("MaxPage must work and be empty: %v", err)
	}
}

func TestQueryAndFloorPage(t *testing.T) {
	if q, err := Query("  nabil  "); err != nil || q != "nabil" {
		t.Fatalf("%q %v", q, err)
	}
	if _, err := Query(strings.Repeat("x", 65)); err == nil {
		t.Fatal("over-long query accepted")
	}
	if p, err := FloorPage(0); err != nil || p != 0 {
		t.Fatal(err)
	}
	if _, err := FloorPage(-1); err == nil {
		t.Fatal("negative floorsheet page accepted")
	}
}

func TestPaginateNeverReturnsNullSlices(t *testing.T) {
	if got := Paginate([]int(nil), Page{Page: 1, Limit: 10}); got.Items == nil {
		t.Fatal("nil items would encode as JSON null")
	}
	if got := Paginate([]int{1, 2}, Page{Page: 9, Limit: 1}); got.Items == nil || len(got.Items) != 0 {
		t.Fatal("a page past the end must be an empty (non-nil) slice")
	}
}

func TestPagedMeta(t *testing.T) {
	res := Paginate([]int{1, 2, 3, 4, 5}, Page{Page: 2, Limit: 2})
	if m := res.Meta(); m != (PageMeta{Page: 2, Limit: 2, Total: 5}) {
		t.Fatalf("%+v", m)
	}
}
