package securities

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/nepse"
)

type fakeUp struct {
	mu         sync.Mutex
	securities []map[string]any
	companies  []map[string]any
	priceVol   any
	today      time.Time
	listErr    error
	companyErr error
	result     any // returned by the per-symbol calls
	err        error
	symbols    []string // symbols the upstream was asked about
	start, end time.Time
}

func (f *fakeUp) record(symbol string) {
	f.mu.Lock()
	f.symbols = append(f.symbols, symbol)
	f.mu.Unlock()
}
func (f *fakeUp) Get(_ context.Context, ep nepse.Endpoint) (any, error) {
	if ep == nepse.EpPriceVolume {
		return f.priceVol, f.err
	}
	return nil, errors.New("unexpected endpoint " + string(ep))
}
func (f *fakeUp) SecurityList(context.Context) ([]map[string]any, error) {
	return f.securities, f.listErr
}
func (f *fakeUp) CompanyList(context.Context) ([]map[string]any, error) {
	return f.companies, f.companyErr
}
func (f *fakeUp) CompanyDetails(_ context.Context, s string) (any, error) {
	f.record(s)
	return f.result, f.err
}
func (f *fakeUp) DailyScripGraph(_ context.Context, s string) (any, error) {
	f.record(s)
	return f.result, f.err
}
func (f *fakeUp) MarketDepth(_ context.Context, s string) (any, error) {
	f.record(s)
	return f.result, f.err
}
func (f *fakeUp) PriceVolumeHistory(_ context.Context, s string, start, end time.Time) (any, error) {
	f.record(s)
	f.start, f.end = start, end
	return f.result, f.err
}
func (f *fakeUp) Today() time.Time { return f.today }

func sec(id int, symbol, name string) map[string]any {
	return map[string]any{"id": id, "symbol": symbol, "securityName": name}
}

func newFake() *fakeUp {
	return &fakeUp{
		securities: []map[string]any{sec(1, "NABIL", "Nabil Bank Limited"), sec(2, "NICA", "NIC Asia Bank"), sec(3, "NBL", "Nepal Bank"),
			sec(4, "NLIC", "Nepal Life Insurance"), sec(5, "NMB", "NMB Bank"), sec(6, "NIFRA", "Nepal Infrastructure"), sec(7, "HDL", "Himalayan Distillery"),
			sec(8, "NBB", "Nepal Bangladesh Bank"), sec(9, "PROMO", "Promoter Share Co")},
		companies: []map[string]any{
			{"symbol": "NABIL", "sectorName": "Commercial Banks"}, {"symbol": "NICA", "sectorName": "Commercial Banks"}, {"symbol": "NBL", "sectorName": "Commercial Banks"},
			{"symbol": "NLIC", "sectorName": "Life Insurance"}, {"symbol": "HDL", "sectorName": "Manufacturing And Processing"}},
		today: time.Date(2026, 10, 15, 22, 0, 0, 0, time.FixedZone("npt", 5*3600+45*60)),
	}
}

func code(err error) apierrors.Code { return apierrors.Normalize(err).Code }

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestListFiltersPaginatesAndValidates(t *testing.T) {
	s := New(newFake())
	ctx := context.Background()
	all, err := s.List(ctx, ListRequest{})
	if err != nil || all.Total != 9 || all.Page != 1 || all.Limit != 50 || len(all.Items) != 9 {
		t.Fatalf("%+v %v", all, err)
	}
	if byName, _ := s.List(ctx, ListRequest{Query: "  BANK "}); byName.Total != 5 { // Nabil, NIC Asia, Nepal, NMB, Nepal Bangladesh
		t.Fatalf("case-insensitive match on the name: %d", byName.Total)
	}
	if bySymbol, _ := s.List(ctx, ListRequest{Query: "nic"}); bySymbol.Total != 1 || bySymbol.Items[0]["symbol"] != "NICA" {
		t.Fatalf("symbol match: %v", bySymbol.Items)
	}
	page2, _ := s.List(ctx, ListRequest{Page: 2, Limit: 4})
	if len(page2.Items) != 4 || page2.Page != 2 || page2.Limit != 4 || page2.Total != 9 || page2.Items[0]["symbol"] != "NMB" {
		t.Fatalf("%+v", page2)
	}
	if past, _ := s.List(ctx, ListRequest{Page: 99, Limit: 4}); past.Items == nil || len(past.Items) != 0 {
		t.Fatal("a page past the end is an empty, non-nil page")
	}
	if none, _ := s.List(ctx, ListRequest{Query: "zzzz"}); none.Total != 0 || none.Items == nil {
		t.Fatalf("%+v", none)
	}
	for _, bad := range []ListRequest{{Page: -1}, {Limit: 501}, {Limit: -2}, {Page: 100001}, {Query: string(make([]byte, 65))}} {
		if _, err := s.List(ctx, bad); code(err) != apierrors.CodeInvalidArgument {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	down := newFake()
	down.listErr = apierrors.UpstreamUnavailable(errors.New("x"))
	if _, err := New(down).List(ctx, ListRequest{}); code(err) != apierrors.CodeUpstreamUnavailable {
		t.Fatal(err)
	}
}

func TestValidateFindsOrSuggests(t *testing.T) {
	s := New(newFake())
	ctx := context.Background()
	info, err := s.Validate(ctx, " nabil ")
	if err != nil || !info.Valid || info.Symbol != "NABIL" || info.Name != "Nabil Bank Limited" {
		t.Fatalf("%+v %v", info, err)
	}
	// suggestions: symbols sharing the first two letters, sorted, at most five
	crowded := newFake()
	crowded.securities = nil
	for i, sym := range []string{"NBG", "NBL", "NBB", "NBF", "NBC", "NBD", "NBE", "NICA", "XYZ"} {
		crowded.securities = append(crowded.securities, sec(i+1, sym, sym+" Co"))
	}
	_, err = New(crowded).Validate(ctx, "NBX")
	e := apierrors.Normalize(err)
	if e.Code != apierrors.CodeNotFound {
		t.Fatal(err)
	}
	if got, want := e.Details.(map[string]any)["suggestions"].([]string), []string{"NBB", "NBC", "NBD", "NBE", "NBF"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("suggestions %v, want %v", got, want)
	}
	_, err = New(crowded).Validate(ctx, "X") // a one-letter symbol suggests by that letter
	if got := apierrors.Normalize(err).Details.(map[string]any)["suggestions"].([]string); !reflect.DeepEqual(got, []string{"XYZ"}) {
		t.Fatalf("%v", got)
	}
	_, err = s.Validate(ctx, "QQ") // no match gives an empty (non-nil) list
	if got := apierrors.Normalize(err).Details.(map[string]any)["suggestions"].([]string); got == nil || len(got) != 0 {
		t.Fatalf("%v", got)
	}
	for _, bad := range []string{"", "bad symbol", "A;B"} {
		if _, err := s.Validate(ctx, bad); code(err) != apierrors.CodeInvalidArgument {
			t.Errorf("%q: %v", bad, err)
		}
	}
	down := newFake()
	down.listErr = errors.New("x")
	if _, err := New(down).Validate(ctx, "NABIL"); err == nil {
		t.Fatal("list failure must propagate")
	}
}

func TestPerSymbolLookupsNormalizeValidateAndCheckShape(t *testing.T) {
	type lookup func(*Service, context.Context, string) (any, error)
	lookups := map[string]lookup{
		"details": func(s *Service, c context.Context, sym string) (any, error) { return s.Details(c, sym) },
		"depth":   func(s *Service, c context.Context, sym string) (any, error) { return s.MarketDepth(c, sym) },
		"graph":   func(s *Service, c context.Context, sym string) (any, error) { return s.Graph(c, sym) },
	}
	for name, call := range lookups {
		up := newFake()
		up.result = map[string]any{"ok": true}
		s := New(up)
		if _, err := call(s, context.Background(), " nabil "); err != nil || !reflect.DeepEqual(up.symbols, []string{"NABIL"}) {
			t.Errorf("%s: the upstream must receive the normalised symbol: %v %v", name, up.symbols, err)
		}
		before := len(up.symbols)
		if _, err := call(s, context.Background(), "bad symbol"); code(err) != apierrors.CodeInvalidArgument || len(up.symbols) != before {
			t.Errorf("%s: invalid input must not reach upstream", name)
		}
		up.result = "a string"
		if _, err := call(s, context.Background(), "NABIL"); code(err) != apierrors.CodeUpstreamInvalidResp {
			t.Errorf("%s: wrong shape: %v", name, err)
		}
		up.err = apierrors.NotFound("symbol")
		if _, err := call(s, context.Background(), "NABIL"); code(err) != apierrors.CodeNotFound {
			t.Errorf("%s: upstream errors pass through: %v", name, err)
		}
	}
}

func TestPriceVolumePicksTheSymbolsRow(t *testing.T) {
	up := newFake()
	up.priceVol = []any{map[string]any{"symbol": "NICA", "x": 1}, map[string]any{"symbol": "NABIL", "x": 2}}
	s := New(up)
	row, err := s.PriceVolume(context.Background(), "nabil")
	if err != nil || row["x"] != 2 {
		t.Fatalf("%v %v", row, err)
	}
	if _, err := s.PriceVolume(context.Background(), "HDL"); code(err) != apierrors.CodeNotFound {
		t.Fatalf("a listed symbol without data today: %v", err)
	}
	if _, err := s.PriceVolume(context.Background(), "x y"); code(err) != apierrors.CodeInvalidArgument {
		t.Fatal(err)
	}
	up.priceVol = "nope"
	if _, err := s.PriceVolume(context.Background(), "NABIL"); code(err) != apierrors.CodeUpstreamInvalidResp {
		t.Fatal(err)
	}
	up.priceVol, up.err = nil, errors.New("x")
	if _, err := s.PriceVolume(context.Background(), "NABIL"); err == nil {
		t.Fatal("upstream error must propagate")
	}
}

func TestPriceHistoryAppliesDefaultsBeforeCallingUpstream(t *testing.T) {
	up := newFake()
	up.result = []any{}
	s := New(up)
	ctx := context.Background()
	date := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }

	if _, err := s.PriceVolumeHistory(ctx, HistoryRequest{Symbol: "nabil"}); err != nil {
		t.Fatal(err)
	}
	if up.end != date("2026-10-15") || up.start != date("2025-10-15") {
		t.Fatalf("defaults: end=today (Nepal), start=end-365d: %v..%v", up.start, up.end)
	}
	if _, err := s.PriceVolumeHistory(ctx, HistoryRequest{Symbol: "NABIL", EndDate: "2024-03-01"}); err != nil || up.start != date("2023-03-02") {
		t.Fatalf("start follows an explicit end: %v %v", up.start, err)
	}
	if _, err := s.PriceVolumeHistory(ctx, HistoryRequest{Symbol: "NABIL", StartDate: "2026-01-01", EndDate: "2026-02-01"}); err != nil || up.start != date("2026-01-01") || up.end != date("2026-02-01") {
		t.Fatalf("explicit range passes through: %v..%v", up.start, up.end)
	}
	calls := len(up.symbols)
	for _, bad := range []HistoryRequest{
		{Symbol: "NABIL", StartDate: "2999-01-01"}, {Symbol: "NABIL", StartDate: "2026-02-01", EndDate: "2026-01-01"},
		{Symbol: "NABIL", StartDate: "nonsense"}, {Symbol: "NABIL", EndDate: "01/02/2026"}, {Symbol: "bad sym"}, {},
	} {
		if _, err := s.PriceVolumeHistory(ctx, bad); code(err) != apierrors.CodeInvalidArgument {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if len(up.symbols) != calls {
		t.Fatal("invalid requests must never reach upstream")
	}
	up.result = 5
	if _, err := s.PriceVolumeHistory(ctx, HistoryRequest{Symbol: "NABIL"}); code(err) != apierrors.CodeUpstreamInvalidResp {
		t.Fatal(err)
	}
}

func TestSectorsAndMembers(t *testing.T) {
	up := newFake()
	s := New(up)
	ctx := context.Background()
	names, err := s.Sectors(ctx)
	if err != nil || !sort.StringsAreSorted(names) || len(names) != 4 || !contains(names, "Promoter Share") {
		t.Fatalf("%v %v", names, err)
	}
	name, members, err := s.SectorSecurities(ctx, "commercial-banks")
	if err != nil || name != "Commercial Banks" || len(members) != 3 || members[0].Symbol != "NABIL" {
		t.Fatalf("%s %v %v", name, members, err)
	}
	for _, alias := range []string{"COMMERCIAL BANKS", " commercial_banks ", "Commercial.Banks"} {
		if n, _, err := s.SectorSecurities(ctx, alias); err != nil || n != "Commercial Banks" {
			t.Errorf("%q: %v", alias, err)
		}
	}
	n, members, err := s.SectorSecurities(ctx, "promoter share") // securities with no company entry
	if err != nil || n != "Promoter Share" || len(members) != 4 {
		t.Fatalf("%s %v %v", n, members, err)
	}
	if _, _, err := s.SectorSecurities(ctx, "nonsense"); code(err) != apierrors.CodeNotFound {
		t.Fatal(err)
	}
	for _, empty := range []string{"", "  ", "---"} {
		if _, _, err := s.SectorSecurities(ctx, empty); code(err) != apierrors.CodeInvalidArgument {
			t.Errorf("%q: %v", empty, err)
		}
	}
	up.companyErr = apierrors.UpstreamUnavailable(errors.New("x")) // either list failing fails the call with the real error
	if _, err := s.Sectors(ctx); code(err) != apierrors.CodeUpstreamUnavailable {
		t.Fatal(err)
	}
	up.companyErr, up.listErr = nil, apierrors.UpstreamAuthFailed(errors.New("x"))
	if _, _, err := s.SectorSecurities(ctx, "life insurance"); code(err) != apierrors.CodeUpstreamAuthFailed {
		t.Fatal(err)
	}
	odd := newFake() // blank symbols are ignored
	odd.securities = append(odd.securities, sec(99, "  ", "blank"), map[string]any{"id": 100})
	if names, err := New(odd).Sectors(ctx); err != nil || len(names) != 4 {
		t.Fatalf("%v %v", names, err)
	}
}
