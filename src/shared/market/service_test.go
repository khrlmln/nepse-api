package market

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/nepse"
)

// fakeUp is an in-memory Upstream recording what was asked.
type fakeUp struct {
	mu        sync.Mutex
	gets      map[nepse.Endpoint]any
	graphs    map[nepse.Endpoint]any
	companies []map[string]any
	failGet   map[nepse.Endpoint]error
	calls     []nepse.Endpoint
	blockOn   nepse.Endpoint // Get blocks until its context ends
}

func (f *fakeUp) Get(ctx context.Context, ep nepse.Endpoint) (any, error) {
	f.mu.Lock()
	f.calls = append(f.calls, ep)
	f.mu.Unlock()
	if ep == f.blockOn && ep != "" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := f.failGet[ep]; err != nil {
		return nil, err
	}
	return f.gets[ep], nil
}

func (f *fakeUp) Graph(_ context.Context, ep nepse.Endpoint) (any, error) {
	f.mu.Lock()
	f.calls = append(f.calls, ep)
	f.mu.Unlock()
	return f.graphs[ep], nil
}

func (f *fakeUp) CompanyList(context.Context) ([]map[string]any, error) { return f.companies, nil }

func num(s string) json.Number { return json.Number(s) }

func objs(items ...map[string]any) []any {
	out := make([]any, len(items))
	for i, m := range items {
		out[i] = m
	}
	return out
}

func code(err error) apierrors.Code { return apierrors.Normalize(err).Code }

func TestSummaryIndexStatusLiveAndShapes(t *testing.T) {
	up := &fakeUp{gets: map[nepse.Endpoint]any{
		nepse.EpSummary:      objs(map[string]any{"detail": "Total Turnover Rs:", "value": num("12.5")}, map[string]any{"value": 1}, map[string]any{"detail": "", "value": 2}),
		nepse.EpIndex:        objs(map[string]any{"index": "NEPSE Index", "currentValue": num("2000.5")}, map[string]any{"noindex": true}),
		nepse.EpSubIndices:   objs(map[string]any{"index": "Banking SubIndex"}),
		nepse.EpMarketStatus: map[string]any{"isOpen": "OPEN"},
		nepse.EpLiveMarket:   []any{map[string]any{"symbol": "X"}},
	}}
	s := New(up)
	ctx := context.Background()
	sum, err := s.Summary(ctx)
	if err != nil || len(sum) != 1 || sum["Total Turnover Rs:"] != num("12.5") {
		t.Fatalf("items without a label are skipped: %v %v", sum, err)
	}
	idx, err := s.Index(ctx)
	if err != nil || len(idx) != 1 || idx["NEPSE Index"] == nil {
		t.Fatalf("%v %v", idx, err)
	}
	if sub, err := s.SubIndices(ctx); err != nil || sub["Banking SubIndex"] == nil {
		t.Fatalf("%v %v", sub, err)
	}
	if st, err := s.Status(ctx); err != nil || st["isOpen"] != "OPEN" {
		t.Fatalf("%v %v", st, err)
	}
	if live, err := s.Live(ctx); err != nil || len(live) != 1 {
		t.Fatalf("%v %v", live, err)
	}

	bad := &fakeUp{gets: map[nepse.Endpoint]any{ // wrong shapes are upstream errors, never panics
		nepse.EpSummary: "text", nepse.EpIndex: map[string]any{}, nepse.EpSubIndices: []any{1},
		nepse.EpMarketStatus: []any{}, nepse.EpLiveMarket: map[string]any{}, nepse.EpSupplyDemand: "x",
	}}
	b := New(bad)
	for name, call := range map[string]func() error{
		"summary":      func() error { _, err := b.Summary(ctx); return err },
		"index":        func() error { _, err := b.Index(ctx); return err },
		"sub-indices":  func() error { _, err := b.SubIndices(ctx); return err },
		"status":       func() error { _, err := b.Status(ctx); return err },
		"live":         func() error { _, err := b.Live(ctx); return err },
		"supplydemand": func() error { _, err := b.SupplyDemand(ctx); return err },
	} {
		if code(call()) != apierrors.CodeUpstreamInvalidResp {
			t.Errorf("%s: wrong shape must be UPSTREAM_INVALID_RESPONSE", name)
		}
	}
	down := &fakeUp{failGet: map[nepse.Endpoint]error{nepse.EpSummary: apierrors.UpstreamUnavailable(errors.New("x"))}}
	if _, err := New(down).Summary(ctx); code(err) != apierrors.CodeUpstreamUnavailable {
		t.Fatalf("%v", err)
	}
}

func TestEveryOperationPropagatesUpstreamFailures(t *testing.T) {
	boom := apierrors.UpstreamUnavailable(errors.New("down"))
	up := &fakeUp{failGet: map[nepse.Endpoint]error{}}
	for _, ep := range []nepse.Endpoint{nepse.EpSummary, nepse.EpIndex, nepse.EpSubIndices, nepse.EpMarketStatus, nepse.EpLiveMarket,
		nepse.EpSupplyDemand, nepse.EpTopGainers, nepse.EpTopLosers, nepse.EpTopTurnover, nepse.EpTopTransaction, nepse.EpTopTrade} {
		up.failGet[ep] = boom
	}
	s := New(up)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"summary":      func() error { _, err := s.Summary(ctx); return err },
		"index":        func() error { _, err := s.Index(ctx); return err },
		"sub-indices":  func() error { _, err := s.SubIndices(ctx); return err },
		"status":       func() error { _, err := s.Status(ctx); return err },
		"live":         func() error { _, err := s.Live(ctx); return err },
		"supplydemand": func() error { _, err := s.SupplyDemand(ctx); return err },
		"gainers":      func() error { _, err := s.Top(ctx, TopGainers); return err },
		"trades":       func() error { _, err := s.Top(ctx, TopTrades); return err },
	} {
		if err := call(); code(err) != apierrors.CodeUpstreamUnavailable {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTopListsMapToTheRightEndpoints(t *testing.T) {
	want := map[TopKind]nepse.Endpoint{
		TopGainers: nepse.EpTopGainers, TopLosers: nepse.EpTopLosers, TopTurnover: nepse.EpTopTurnover,
		TopTransactions: nepse.EpTopTransaction, TopTrades: nepse.EpTopTrade,
	}
	for kind, ep := range want {
		up := &fakeUp{gets: map[nepse.Endpoint]any{ep: []any{map[string]any{"symbol": string(kind)}}}}
		got, err := New(up).Top(context.Background(), kind)
		if err != nil || len(got) != 1 || !reflect.DeepEqual(up.calls, []nepse.Endpoint{ep}) {
			t.Errorf("%s: %v %v calls=%v", kind, got, err, up.calls)
		}
	}
	if _, err := New(&fakeUp{}).Top(context.Background(), TopKind("bogus")); code(err) != apierrors.CodeInvalidArgument {
		t.Fatal("unknown kinds are caller errors")
	}
	if _, err := New(&fakeUp{gets: map[nepse.Endpoint]any{nepse.EpTopGainers: "x"}}).Top(context.Background(), TopGainers); code(err) != apierrors.CodeUpstreamInvalidResp {
		t.Fatal("non-array top list")
	}
}

type failingGraphs struct{ fakeUp }

func (f *failingGraphs) Graph(context.Context, nepse.Endpoint) (any, error) {
	return nil, apierrors.UpstreamAuthFailed(errors.New("x"))
}

func TestGraphNameHandling(t *testing.T) {
	up := &fakeUp{graphs: map[nepse.Endpoint]any{"nepse_index_daily_graph": []any{}, "banking_sub_index_graph": []any{}, "sensitive_float_index_daily_graph": map[string]any{}}}
	s := New(up)
	ctx := context.Background()
	for _, name := range []string{"", "  ", "nepse", " NEPSE "} {
		if _, err := s.IndexGraph(ctx, name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := s.IndexGraph(ctx, "sensitive-float"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubIndexGraph(ctx, "Banking"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"bogus", "nepse-index", "../x"} {
		if _, err := s.IndexGraph(ctx, bad); code(err) != apierrors.CodeInvalidArgument {
			t.Errorf("%q: %v", bad, err)
		}
	}
	_, err := s.SubIndexGraph(ctx, "")
	if code(err) != apierrors.CodeInvalidArgument {
		t.Fatalf("an empty sector has no default: %v", err)
	}
	if msg := apierrors.Normalize(err).Message; !strings.Contains(msg, "banking") || !strings.Contains(msg, "hydropower") {
		t.Fatalf("the message must list the valid names so callers can self-correct: %q", msg)
	}
	if got := IndexGraphNames(); !sort.StringsAreSorted(got) || len(got) != 4 {
		t.Fatalf("%v", got)
	}
	if got := SubIndexGraphNames(); !sort.StringsAreSorted(got) || len(got) != 13 {
		t.Fatalf("%v", got)
	}
	if _, err := New(&failingGraphs{}).IndexGraph(ctx, "nepse"); code(err) != apierrors.CodeUpstreamAuthFailed {
		t.Fatal(err)
	}
	if _, err := New(&fakeUp{graphs: map[nepse.Endpoint]any{"nepse_index_daily_graph": "text"}}).IndexGraph(ctx, "nepse"); code(err) != apierrors.CodeUpstreamInvalidResp {
		t.Fatal(err)
	}
}

func TestEverySectorSlugReachesItsOwnEndpoint(t *testing.T) {
	want := map[string]nepse.Endpoint{
		"banking": "banking_sub_index_graph", "development-bank": "development_bank_sub_index_graph", "finance": "finance_sub_index_graph",
		"hotels-tourism": "hotel_tourism_sub_index_graph", "hydropower": "hydro_sub_index_graph", "investment": "investment_sub_index_graph",
		"life-insurance": "life_insurance_sub_index_graph", "manufacturing": "manufacturing_sub_index_graph", "microfinance": "microfinance_sub_index_graph",
		"mutual-fund": "mutual_fund_sub_index_graph", "non-life-insurance": "non_life_insurance_sub_index_graph", "others": "others_sub_index_graph",
		"trading": "trading_sub_index_graph",
	}
	names := make([]string, 0, len(want))
	for slug, ep := range want {
		names = append(names, slug)
		up := &fakeUp{graphs: map[nepse.Endpoint]any{ep: []any{}}}
		if _, err := New(up).SubIndexGraph(context.Background(), slug); err != nil || !reflect.DeepEqual(up.calls, []nepse.Endpoint{ep}) {
			t.Errorf("%s: %v calls=%v", slug, err, up.calls)
		}
	}
	sort.Strings(names)
	if !reflect.DeepEqual(SubIndexGraphNames(), names) {
		t.Fatalf("public names drifted: %v", SubIndexGraphNames())
	}
}

func overviewData() *fakeUp {
	return &fakeUp{gets: map[nepse.Endpoint]any{
		nepse.EpCompanyList: objs(map[string]any{"symbol": "NABIL", "sectorName": "Commercial Banks", "securityName": "Nabil Bank", "instrumentType": "Equity"}),
		nepse.EpTopTurnover: objs(map[string]any{"symbol": "NABIL", "turnover": num("1000")}), nepse.EpTopTransaction: objs(map[string]any{"symbol": "NABIL", "totalTrades": num("10")}),
		nepse.EpTopTrade: objs(map[string]any{"symbol": "NABIL", "shareTraded": num("500")}), nepse.EpTopGainers: objs(map[string]any{"symbol": "NABIL", "ltp": num("900"), "pointChange": num("5"), "percentageChange": num("0.5")}),
		nepse.EpTopLosers: []any{}, nepse.EpPriceVolume: objs(map[string]any{"symbol": "NABIL", "previousClose": num("895"), "lastUpdatedDateTime": "t"}),
		nepse.EpSubIndices: objs(map[string]any{"index": "Banking SubIndex", "currentValue": num("1500")}),
	}}
}

func TestTurnoverOverviewFetchesAllEightAndJoins(t *testing.T) {
	up := overviewData()
	got, err := New(up).TurnoverOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(up.calls) != 8 {
		t.Fatalf("calls: %v", up.calls)
	}
	m := got.(map[string]any)
	scrip := m["scripsDetails"].(map[string]map[string]any)["NABIL"]
	if scrip["ltp"] != num("900") || scrip["Turnover"] != num("1000") || scrip["name"] != "Nabil Bank" || scrip["category"] != "Equity" {
		t.Fatalf("%v", scrip)
	}
	sector := m["sectorsDetails"].(map[string]map[string]any)["Commercial Banks"]
	if sector["totalTurnover"] != float64(1000) || sector["transaction"] != float64(10) || sector["volume"] != float64(500) || sector["turnover"] == nil {
		t.Fatalf("%v", sector)
	}
}

func TestTurnoverOverviewFailureCancelsTheRestAndReportsTheRealError(t *testing.T) {
	up := overviewData()
	up.failGet = map[nepse.Endpoint]error{nepse.EpTopGainers: apierrors.UpstreamUnavailable(errors.New("down"))}
	up.blockOn = nepse.EpPriceVolume // would hang forever unless cancelled
	done := make(chan error, 1)
	go func() { _, err := New(up).TurnoverOverview(context.Background()); done <- err }()
	select {
	case err := <-done:
		if code(err) != apierrors.CodeUpstreamUnavailable {
			t.Fatalf("the original failure must win over collateral cancellations: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a failed sibling did not cancel the blocked fetch")
	}
	bad := overviewData()
	bad.gets[nepse.EpTopLosers] = "not a list"
	if _, err := New(bad).TurnoverOverview(context.Background()); code(err) != apierrors.CodeUpstreamInvalidResp {
		t.Fatalf("%v", err)
	}
}

func TestCompositeEdgeCases(t *testing.T) {
	if _, err := buildTurnoverOverview(overviewInput{companies: "x"}); code(err) != apierrors.CodeUpstreamInvalidResp {
		t.Fatal("company list must be an array")
	}
	field := func(set func(*overviewInput)) overviewInput {
		in := overviewInput{companies: []any{}, turnover: []any{}, transactions: []any{}, trades: []any{}, gainers: []any{}, losers: []any{}, priceVolume: []any{}, subIndices: []any{}}
		set(&in)
		return in
	}
	for name, in := range map[string]overviewInput{
		"turnover": field(func(i *overviewInput) { i.turnover = 1 }), "transactions": field(func(i *overviewInput) { i.transactions = 1 }),
		"trades": field(func(i *overviewInput) { i.trades = 1 }), "gainers": field(func(i *overviewInput) { i.gainers = 1 }),
		"losers": field(func(i *overviewInput) { i.losers = 1 }), "priceVolume": field(func(i *overviewInput) { i.priceVolume = 1 }),
	} {
		if _, err := buildTurnoverOverview(in); code(err) != apierrors.CodeUpstreamInvalidResp {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a scrip needs both ltp and previous close; losers are used when it is not a gainer
	companies := objs(map[string]any{"symbol": "A", "sectorName": "Hydro Power"}, map[string]any{"symbol": "B", "sectorName": "Unmapped Sector"},
		map[string]any{"symbol": "C", "sectorName": "Hydro Power"}, map[string]any{"symbol": "A", "sectorName": "Hydro Power"})
	out, err := buildTurnoverOverview(overviewInput{
		companies: companies, turnover: []any{}, transactions: []any{}, trades: []any{},
		gainers:     objs(map[string]any{"symbol": "C", "ltp": 0, "pointChange": 1}), // ltp zero -> skipped
		losers:      objs(map[string]any{"symbol": "A", "ltp": num("10"), "pointChange": num("-1")}, map[string]any{"symbol": "B", "ltp": num("5")}),
		priceVolume: objs(map[string]any{"symbol": "A", "previousClose": num("11")}, map[string]any{"symbol": "B", "previousClose": 0}, map[string]any{"symbol": "C", "previousClose": 3}),
		subIndices:  objs(map[string]any{"index": "HydroPower Index", "currentValue": 1}, map[string]any{"index": "Unmapped Sector", "currentValue": 2}),
	})
	if err != nil {
		t.Fatal(err)
	}
	scrips := out.(map[string]any)["scripsDetails"].(map[string]map[string]any)
	if len(scrips) != 1 || scrips["A"]["pointChange"] != num("-1") || scrips["A"]["name"] != "" || scrips["A"]["Turnover"] != 0 {
		t.Fatalf("filtering/defaults wrong: %v", scrips)
	}
	sectors := out.(map[string]any)["sectorsDetails"].(map[string]map[string]any)
	if len(sectors) != 2 || sectors["Hydro Power"]["turnover"] == nil || sectors["Unmapped Sector"]["turnover"] == nil {
		t.Fatalf("sector join (incl. fallback to the sector name): %v", sectors)
	}
	if got := symbolOrder(companies); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Fatalf("duplicates keep first position: %v", got)
	}
	if asFloat(num("1.5")) != 1.5 || asFloat(2) != 2 || asFloat(int64(3)) != 3 || asFloat(2.5) != 2.5 || asFloat("x") != 0 || asFloat(nil) != 0 || asFloat(json.Number("zz")) != 0 {
		t.Fatal("asFloat")
	}
	if pick(nil, "k", 7) != 7 || pick(map[string]any{"k": nil}, "k", 7) != nil || pick(map[string]any{}, "k", 7) != 7 {
		t.Fatal("pick must behave like dict.get")
	}
}
