package nepse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/nepse/assets"
)

// fake verifies on every POST that the payload id derives from the salts of the token presented with it.
type fake struct {
	proveBody      atomic.Value // raw body to serve from the login endpoint instead of a real token
	histBody       atomic.Value // body override for the price-history endpoint
	floorBody      atomic.Value // body override for the all-market floorsheet endpoint
	proveFail      atomic.Bool
	proveDelay     atomic.Int64
	proveStarted   chan struct{}
	summary        atomic.Value // body override for the market-summary endpoint
	getCount       atomic.Int32
	t              *testing.T
	srv            *httptest.Server
	loc            *time.Location
	mu             sync.Mutex
	issued         int
	tokens         map[string][5]int
	parser         *wasmTokenParser
	assets         assetsData
	marketStatusID int
	prove          atomic.Int32
	badPay         atomic.Int32
	pages          int
	empties        sync.Map // page -> *atomic.Int32 remaining empty answers
	seen           sync.Map
	bigBody        atomic.Bool
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, tokens: map[string][5]int{}, loc: time.FixedZone("npt", 5*3600+45*60), marketStatusID: 7, pages: 5}
	var err error
	if f.parser, err = newWASMTokenParser(context.Background(), assets.CSSWASM()); err != nil {
		t.Fatal(err)
	}
	if f.assets, err = loadAssets(); err != nil {
		t.Fatal(err)
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(func() { f.srv.Close(); _ = f.parser.Close(context.Background()) })
	return f
}

func (f *fake) client(mod func(*Config)) *Client {
	cfg := Config{BaseURL: f.srv.URL, AllowInsecureHTTP: true, Timeout: 5 * time.Second, Location: f.loc, MaxFloorPages: 50, MaxFloorRows: 25000, MaxResponseBytes: 1 << 20}
	if mod != nil {
		mod(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func (f *fake) issue() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issued++
	n := f.issued
	a := AuthResponse{Salt1: 11 + n, Salt2: 22 + 2*n, Salt3: 33 + 3*n, Salt4: 44 + 4*n, Salt5: 55 + 5*n,
		AccessToken: fmt.Sprintf("%s%04d", strings.Repeat("A", 196), n), RefreshToken: fmt.Sprintf("%s%04d", strings.Repeat("B", 196), n)}
	acc, _, err := f.parser.parse(context.Background(), a)
	if err != nil {
		f.t.Fatalf("fake parse: %v", err)
	}
	f.tokens[acc] = [5]int{a.Salt1, a.Salt2, a.Salt3, a.Salt4, a.Salt5}
	b, _ := json.Marshal(a)
	return b
}

func (f *fake) mode(path string) payloadMode {
	switch {
	case strings.Contains(path, "graph"), strings.Contains(path, "/graph/"):
		return modeGraph
	case strings.Contains(path, "floorsheet"):
		return modeFloor
	}
	return modeScrip
}

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	f.seen.Store(r.URL.RequestURI(), true)
	if r.URL.Path == proveEndpoint {
		f.prove.Add(1)
		if f.proveStarted != nil {
			select {
			case f.proveStarted <- struct{}{}:
			default:
			}
		}
		if d := f.proveDelay.Load(); d > 0 {
			time.Sleep(time.Duration(d))
		}
		if f.proveFail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if b := f.proveBody.Load(); b != nil {
			_, _ = w.Write([]byte(b.(string)))
			return
		}
		_, _ = w.Write(f.issue())
		return
	}
	f.mu.Lock()
	salts, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Salter ")]
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet {
		f.getCount.Add(1)
		switch {
		case strings.Contains(r.URL.Path, "/market/history/") && f.histBody.Load() != nil:
			_, _ = w.Write([]byte(f.histBody.Load().(string)))
		case strings.HasSuffix(r.URL.Path, "market-summary/") && f.summary.Load() != nil:
			_, _ = w.Write([]byte(f.summary.Load().(string)))
		case r.URL.Path == "/api/nots/nepse-data/market-open":
			fmt.Fprintf(w, `{"isOpen":"CLOSE","asOf":"2026-10-08T15:00:00","id":%d}`, f.marketStatusID)
		case r.URL.Path == "/api/nots/security":
			_, _ = w.Write([]byte(`[{"id":101,"symbol":"NABIL","securityName":"Nabil Bank"},{"id":102,"symbol":"NICA"}]`))
		case f.bigBody.Load():
			_, _ = w.Write([]byte(`[` + strings.Repeat(`{"a":1},`, 200000) + `{}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	want, _ := computePayloadID(f.assets.payloadSeeds[f.marketStatusID], f.marketStatusID, time.Now().In(f.loc).Day(), salts, f.mode(r.URL.Path))
	if body.ID != want {
		f.badPay.Add(1)
		w.WriteHeader(400)
		return
	}
	if strings.Contains(r.URL.Path, "floorsheet") {
		page := 0
		if p := r.URL.Query().Get("page"); p != "" {
			page, _ = strconv.Atoi(p)
		}
		if v, ok := f.empties.Load(page); ok && v.(*atomic.Int32).Add(-1) >= 0 {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if body := f.floorBody.Load(); body != nil {
			_, _ = w.Write([]byte(body.(string)))
			return
		}
		fmt.Fprintf(w, `{"floorsheets":{"content":[{"contractId":%d,"stockSymbol":"X","businessDate":"2026-10-08"},{"contractId":%d,"stockSymbol":"Y","businessDate":"2026-10-08"}],"totalPages":%d,"totalElements":%d}}`,
			20260000000000000+int64(page*2), 20260000000000000+int64(page*2+1), f.pages, f.pages*2)
		return
	}
	fmt.Fprintf(w, `{"ok":true,"id":%d}`, body.ID)
}

func TestPayloadMatchesTokenSaltsAndSurvives401(t *testing.T) {
	f := newFake(t)
	c := f.client(nil)
	ctx := context.Background()
	if _, err := c.CompanyDetails(ctx, "NABIL"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.tokens = map[string][5]int{} // upstream forgets the cached token
	f.mu.Unlock()
	if _, err := c.Graph(ctx, "nepse_index_daily_graph"); err != nil { // salt-dependent payload
		t.Fatalf("graph after expiry: %v", err)
	}
	f.mu.Lock()
	f.tokens = map[string][5]int{}
	f.mu.Unlock()
	if _, err := c.FloorSheet(ctx, "", time.Time{}, 0); err != nil {
		t.Fatalf("floorsheet after expiry: %v", err)
	}
	if f.badPay.Load() != 0 || f.prove.Load() < 3 {
		t.Fatalf("badPayloads=%d prove=%d", f.badPay.Load(), f.prove.Load())
	}
}

func TestSingleFlightTokenRefreshUnderConcurrency(t *testing.T) {
	f := newFake(t)
	c := f.client(nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Graph(context.Background(), "banking_sub_index_graph"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.prove.Load() != 1 || f.badPay.Load() != 0 {
		t.Fatalf("prove=%d bad=%d", f.prove.Load(), f.badPay.Load())
	}
}

func TestFloorSheetPagingAndLimits(t *testing.T) {
	f := newFake(t)
	f.pages = 4
	c := f.client(nil)
	ctx := context.Background()
	p, err := c.FloorSheet(ctx, "", time.Time{}, 2)
	if err != nil || len(p.Rows) != 2 || p.Page != 2 || p.TotalPages != 4 || p.TotalElements != 8 {
		t.Fatalf("page: %+v err=%v", p, err)
	}
	b, _ := json.Marshal(p.Rows)
	if !strings.Contains(string(b), "20260000000000004") { // exact large id preserved
		t.Fatalf("rows: %s", b)
	}
	for _, bad := range []int{-1, 50, 1000} {
		if _, err := c.FloorSheet(ctx, "", time.Time{}, bad); apierrors.Normalize(err).Code != apierrors.CodeInvalidArgument {
			t.Fatalf("page %d: %v", bad, err)
		}
	}
	// The page limit rounds up so a non-multiple row cap can use its final partially filled page, but never a page beyond that cap.
	c2 := f.client(func(cfg *Config) { cfg.MaxFloorRows = 501 })
	if _, err := c2.FloorSheet(ctx, "", time.Time{}, 1); err != nil {
		t.Fatalf("final partial page should fit a 501-row cap: %v", err)
	}
	if _, err := c2.FloorSheet(ctx, "", time.Time{}, 2); apierrors.Normalize(err).Code != apierrors.CodeInvalidArgument {
		t.Fatalf("row cap not enforced: %v", err)
	}
}

func TestFloorSheetEmptyShapes(t *testing.T) {
	f := newFake(t)
	f.pages = 1
	c := f.client(nil)
	ctx := context.Background()
	p, err := c.FloorSheet(ctx, "NICA", time.Time{}, 0)
	if err != nil || len(p.Rows) != 0 || p.TotalPages != 0 || p.TotalElements != 0 {
		t.Fatalf("empty filtered floorsheet: %+v err=%v", p, err)
	}
	if _, ok := f.seen.Load("/api/nots/nepse-data/floorsheet?&size=500&sort=contractId,desc"); !ok {
		t.Fatal("symbol-specific floorsheet must use the supported all-market endpoint")
	}
	// A real symbol is filtered from the complete session and the requested page is applied after filtering.
	f.floorBody.Store(`{"floorsheets":{"content":[{"contractId":"N1","stockSymbol":"NABIL","businessDate":"2026-10-08"},{"contractId":"X1","stockSymbol":"X","businessDate":"2026-10-08"},{"contractId":"N2","stockSymbol":"NABIL","businessDate":"2026-10-08"},{"contractId":"N1","stockSymbol":"NABIL","businessDate":"2026-10-08"}],"totalPages":1,"totalElements":4}}`)
	c.cache = newResponseCache()
	filtered, err := c.FloorSheet(ctx, "NABIL", time.Date(2026, 10, 8, 0, 0, 0, 0, f.loc), 0)
	if err != nil || len(filtered.Rows) != 2 || filtered.TotalPages != 1 || filtered.TotalElements != 2 {
		t.Fatalf("filtered symbol page: %+v err=%v", filtered, err)
	}
	var first map[string]any
	if err := json.Unmarshal(filtered.Rows[0], &first); err != nil || first["contractId"] != "N1" {
		t.Fatalf("filtered rows do not belong to NABIL: %s err=%v", filtered.Rows[0], err)
	}
	past, err := c.FloorSheet(ctx, "NABIL", time.Date(2026, 10, 7, 0, 0, 0, 0, f.loc), 0)
	if err == nil || apierrors.Normalize(err).Code != apierrors.CodeUnavailable {
		t.Fatalf("historical per-symbol query should be unavailable: page=%+v err=%v", past, err)
	}

	// A mixed-date scan must fail rather than silently returning a partial symbol result from a response whose pages crossed session boundaries.
	f.floorBody.Store(`{"floorsheets":{"content":[{"contractId":"N1","stockSymbol":"NABIL","businessDate":"2026-10-08"},{"contractId":"N0","stockSymbol":"NABIL","businessDate":"2026-10-07"}],"totalPages":1,"totalElements":2}}`)
	c.cache = newResponseCache()
	_, err = c.FloorSheet(ctx, "NABIL", time.Time{}, 0)
	got := apierrors.Normalize(err)
	if got == nil || got.Code != apierrors.CodeUpstreamInvalidResp || !got.Retryable {
		t.Fatalf("mixed-session floorsheet must fail as a retryable upstream inconsistency: %v", err)
	}

	f.floorBody.Store(`{"floorsheets":{"content":[{"contractId":"N1","businessDate":"2026-10-08"}],"totalPages":1,"totalElements":1}}`)
	c.cache = newResponseCache()
	if _, err := c.FloorSheet(ctx, "NABIL", time.Time{}, 0); apierrors.Normalize(err).Code != apierrors.CodeUpstreamInvalidResp {
		t.Fatalf("row without a symbol must be rejected: %v", err)
	}

	// Without totalPages, totalElements must explicitly prove that the short first page is the complete session; otherwise filtering could be partial.
	f.floorBody.Store(`{"floorsheets":{"content":[{"contractId":"N1","stockSymbol":"NABIL","businessDate":"2026-10-08"}]}}`)
	c.cache = newResponseCache()
	if _, err := c.FloorSheet(ctx, "NABIL", time.Time{}, 0); apierrors.Normalize(err).Code != apierrors.CodeUpstreamInvalidResp {
		t.Fatalf("floorsheet without pagination metadata must fail closed: %v", err)
	}
	f.floorBody.Store(`{"floorsheets":{"content":[{"contractId":"N1","stockSymbol":"NABIL","businessDate":"2026-10-08"}],"totalElements":1}}`)
	c.cache = newResponseCache()
	if result, err := c.FloorSheet(ctx, "NABIL", time.Time{}, 0); err != nil || len(result.Rows) != 1 {
		t.Fatalf("matching totalElements should prove a short single-page session: %+v %v", result, err)
	}

	// Spurious empty mid-page responses are retried before being cached.
	f.floorBody.Store(`{"floorsheets":{"content":[{"contractId":"P1","stockSymbol":"X","businessDate":"2026-10-08"},{"contractId":"P2","stockSymbol":"Y","businessDate":"2026-10-08"}],"totalPages":4,"totalElements":8}}`)
	n := &atomic.Int32{}
	n.Store(2)
	f.empties.Store(3, n)
	c.cache = newResponseCache()
	if p, err := c.FloorSheet(ctx, "", time.Time{}, 3); err != nil || len(p.Rows) != 2 {
		t.Fatalf("retry of spurious empty page: %+v %v", p, err)
	}
}

func TestNotFoundSymbolIsApiError(t *testing.T) {
	f := newFake(t)
	c := f.client(nil)
	_, err := c.SecurityID(context.Background(), "NOPE")
	if apierrors.Normalize(err).Code != apierrors.CodeNotFound {
		t.Fatalf("err=%v", err)
	}
	if id, err := c.SecurityID(context.Background(), "NABIL"); err != nil || id != 101 {
		t.Fatalf("%d %v", id, err)
	}
}

func TestCancellationAndOversizeClassification(t *testing.T) {
	f := newFake(t)
	c := f.client(func(cfg *Config) { cfg.MaxResponseBytes = 4096 })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(ctx, EpSummary); apierrors.Normalize(err).Code != apierrors.CodeCanceled {
		t.Fatalf("canceled ctx: %v", err)
	}
	f.bigBody.Store(true)
	_, err := c.Get(context.Background(), EpSummary)
	if got := apierrors.Normalize(err); got.Code != apierrors.CodeUpstreamInvalidResp {
		t.Fatalf("oversize: %v", err)
	}
}

func TestUpstreamDownIsUnavailableAndNoLeak(t *testing.T) {
	f := newFake(t)
	c := f.client(nil)
	f.srv.Close()
	_, err := c.Get(context.Background(), EpSummary)
	got := apierrors.Normalize(err)
	if got.Code != apierrors.CodeUpstreamUnavailable || strings.Contains(got.Message, "127.0.0.1") {
		t.Fatalf("got %+v", got)
	}
}
