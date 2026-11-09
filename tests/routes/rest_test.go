package routes_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nepse-api/src/rest"
	"nepse-api/src/shared/application"
	"nepse-api/src/shared/config"
	"nepse-api/src/shared/nepse"
)

func newApp(t *testing.T, u *fakeUpstream) *application.App {
	t.Helper()
	cfg := config.Common{
		ServiceName: "routes-test",
		NEPSE: nepse.Config{
			BaseURL: u.srv.URL, AllowInsecureHTTP: true, Timeout: 5 * time.Second,
			CacheTTL: 0, MaxStale: 0, MaxFloorPages: 10, MaxFloorRows: 5000,
		},
	}
	app, err := application.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Close(ctx); err != nil {
			t.Errorf("close test application: %v", err)
		}
	})
	return app
}

func newRESTHandler(t *testing.T, app *application.App) http.Handler {
	t.Helper()
	cfg := config.REST{
		Common: config.Common{ServiceName: "rest-routes-test", SlowRequestThreshold: time.Hour},
		Host:   "127.0.0.1", Port: 8000,
		ReadTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute,
		RequestTimeout: 10 * time.Second, MaxConcurrent: 16, DocsEnabled: true,
	}
	srv, err := rest.New(cfg, app, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("create REST server: %v", err)
	}
	return srv.Handler()
}

type restCase struct {
	name   string
	method string
	url    string
	status int
	check  func(t *testing.T, body map[string]any)
}

func get(name, url string, status int, check func(*testing.T, map[string]any)) restCase {
	return restCase{name: name, method: http.MethodGet, url: url, status: status, check: check}
}

// dataIs asserts data[path...] == want (numbers are compared as decoded float64).
func dataIs(want any, path ...any) func(*testing.T, map[string]any) {
	return func(t *testing.T, body map[string]any) {
		t.Helper()
		got := field(body, append([]any{"data"}, path...)...)
		if show(got) != show(want) {
			t.Errorf("data%v = %v, want %v\nbody: %v", path, got, want, body)
		}
	}
}

func errorCode(want string) func(*testing.T, map[string]any) {
	return func(t *testing.T, body map[string]any) {
		t.Helper()
		if got := field(body, "error", "code"); got != want {
			t.Errorf("error code = %v, want %s\nbody: %v", got, want, body)
		}
		if field(body, "error", "request_id") == nil {
			t.Errorf("error body has no request_id: %v", body)
		}
	}
}

func both(checks ...func(*testing.T, map[string]any)) func(*testing.T, map[string]any) {
	return func(t *testing.T, body map[string]any) {
		t.Helper()
		for _, c := range checks {
			c(t, body)
		}
	}
}

func metaIs(want any, path ...any) func(*testing.T, map[string]any) {
	return func(t *testing.T, body map[string]any) {
		t.Helper()
		if got := field(body, append([]any{"meta"}, path...)...); show(got) != show(want) {
			t.Errorf("meta%v = %v, want %v\nbody: %v", path, got, want, body)
		}
	}
}

func lenIs(n int, path ...any) func(*testing.T, map[string]any) {
	return func(t *testing.T, body map[string]any) {
		t.Helper()
		s, _ := field(body, append([]any{"data"}, path...)...).([]any)
		if len(s) != n {
			t.Errorf("len(data%v) = %d, want %d\nbody: %v", path, len(s), n, body)
		}
	}
}

var indexGraphs = map[string]string{
	"nepse": "/api/nots/graph/index/58", "sensitive": "/api/nots/graph/index/57",
	"float": "/api/nots/graph/index/62", "sensitive-float": "/api/nots/graph/index/63",
}

var sectorGraphs = map[string]string{
	"banking": "/api/nots/graph/index/51", "development-bank": "/api/nots/graph/index/55",
	"finance": "/api/nots/graph/index/60", "hotels-tourism": "/api/nots/graph/index/52",
	"hydropower": "/api/nots/graph/index/54", "investment": "/api/nots/graph/index/67",
	"life-insurance": "/api/nots/graph/index/65", "manufacturing": "/api/nots/graph/index/56",
	"microfinance": "/api/nots/graph/index/64", "mutual-fund": "/api/nots/graph/index/66",
	"non-life-insurance": "/api/nots/graph/index/59", "others": "/api/nots/graph/index/53",
	"trading": "/api/nots/graph/index/61",
}

func restCases(u *fakeUpstream) []restCase {
	const v1 = "/api/v1"
	cases := []restCase{
		get("summary", v1+"/market/summary", 200, dataIs(123456.5, "Total Turnover Rs:")),
		get("status", v1+"/market/status", 200, both(dataIs("CLOSE", "isOpen"), dataIs(7, "id"))),
		get("indices", v1+"/market/index", 200, dataIs(2000.5, "NEPSE Index", "currentValue")),
		get("sub-indices", v1+"/market/sub-indices", 200, dataIs(1500.2, "Banking SubIndex", "currentValue")),
		get("live", v1+"/market/live", 200, both(lenIs(3), dataIs("LIVE1", 0, "symbol"))),
		get("supply-demand", v1+"/market/supply-demand", 200, both(dataIs("SUP", "supplyList", 0, "symbol"), dataIs("DEM", "demandList", 0, "symbol"))),
		get("top-gainers", v1+"/market/top-gainers", 200, dataIs("GAIN", 0, "symbol")),
		get("top-losers", v1+"/market/top-losers", 200, dataIs("LOSE", 0, "symbol")),
		get("top-turnover", v1+"/market/top-turnover", 200, dataIs("TURN", 0, "symbol")),
		get("top-transactions", v1+"/market/top-transactions", 200, dataIs("TXN", 0, "symbol")),
		get("top-trades", v1+"/market/top-trades", 200, dataIs("TRD", 0, "symbol")),
		get("turnover-overview", v1+"/market/turnover-overview", 200, both(
			dataIs(110, "scripsDetails", "GAIN", "ltp"),
			dataIs(100, "scripsDetails", "GAIN", "previousClose"),
			dataIs("Hydro Power", "scripsDetails", "GAIN", "sector"),
			dataIs("HydroPower Index", "sectorsDetails", "Hydro Power", "turnover", "index"),
			dataIs("Banking SubIndex", "sectorsDetails", "Commercial Banks", "turnover", "index"),
		)),

		get("securities list", v1+"/securities", 200, both(lenIs(3), metaIs(1, "page"), metaIs(50, "limit"), metaIs(3, "total"))),
		get("securities filter", v1+"/securities?q=nabil", 200, both(lenIs(1), dataIs("NABIL", 0, "symbol"), metaIs(1, "total"))),
		get("securities paging", v1+"/securities?limit=1&page=2", 200, both(lenIs(1), dataIs("PRMT", 0, "symbol"), metaIs(3, "total"))),
		get("details", v1+"/securities/NABIL", 200, dataIs("details", "marker")),
		get("details lower case", v1+"/securities/nabil", 200, dataIs("details", "marker")),
		get("validate", v1+"/securities/nabil/validate", 200, both(dataIs(true, "valid"), dataIs("NABIL", "symbol"), dataIs("Nabil Bank Limited", "name"))),
		get("validate unknown", v1+"/securities/NABL/validate", 404, both(errorCode("NOT_FOUND"), func(t *testing.T, b map[string]any) {
			if s, _ := field(b, "error", "details", "suggestions", 0).(string); s != "NABIL" {
				t.Errorf("suggestions = %v", field(b, "error", "details"))
			}
		})),
		get("market depth", v1+"/securities/NABIL/market-depth", 200, dataIs("depth", "marketDepth", "marker")),
		get("price volume", v1+"/securities/NABIL/price-volume", 200, both(dataIs("NABIL", "symbol"), dataIs(990, "previousClose"))),
		get("price volume other", v1+"/securities/GAIN/price-volume", 200, dataIs(100, "previousClose")),
		get("price volume absent", v1+"/securities/PRMT/price-volume", 404, errorCode("NOT_FOUND")),
		get("price history", v1+"/securities/NABIL/price-history?start_date=2026-01-01&end_date=2026-02-01", 200, dataIs("history", 0, "marker")),
		get("price history default range", v1+"/securities/NABIL/price-history", 200, dataIs("history", 0, "marker")),
		get("graph", v1+"/securities/NABIL/graph", 200, dataIs("daily-graph", 0, "marker")),
		get("sectors", v1+"/sectors", 200, both(lenIs(3), dataIs("Commercial Banks", 0), dataIs("Hydro Power", 1), dataIs("Promoter Share", 2))),
		get("sector members", v1+"/sectors/commercial-banks/securities", 200, both(dataIs("Commercial Banks", "sector"), dataIs("NABIL", "securities", 0, "symbol"))),
		get("sector members promoter", v1+"/sectors/Promoter%20Share/securities", 200, dataIs("PRMT", "securities", 0, "symbol")),

		get("floorsheet", v1+"/trading/floorsheet", 200, both(dataIs("ALL-1", 0, "contractId"), metaIs(0, "page"), metaIs(500, "page_size"), metaIs(3, "total_pages"), metaIs(1250, "total_elements"))),
		get("floorsheet page", v1+"/trading/floorsheet?page=1", 200, metaIs(1, "page")),
		get("floorsheet symbol", v1+"/trading/floorsheet/NABIL?business_date=2026-11-05", 200, both(dataIs("NABIL-1", 0, "contractId"), metaIs(1, "total_pages"), metaIs(3, "total_elements"))),

		get("index graph default", v1+"/market/graphs/indices/nepse", 200, dataIs("/api/nots/graph/index/58", 0, "endpoint")),

		get("health live", "/health/live", 200, dataIs(nil)), // not enveloped; checked below
		get("not found", v1+"/nope", 404, errorCode("NOT_FOUND")),
		get("bad symbol", v1+"/securities/bad$sym", 400, errorCode("INVALID_ARGUMENT")),
		get("unknown symbol", v1+"/securities/UNKNWN/graph", 404, errorCode("NOT_FOUND")),
		get("bad index graph", v1+"/market/graphs/indices/nope", 400, errorCode("INVALID_ARGUMENT")),
		get("bad sector graph", v1+"/market/graphs/sectors/nope", 400, errorCode("INVALID_ARGUMENT")),
		get("page not integer", v1+"/securities?page=abc", 400, errorCode("INVALID_ARGUMENT")),
		get("limit too large", v1+"/securities?limit=501", 400, errorCode("INVALID_ARGUMENT")),
		get("negative floor page", v1+"/trading/floorsheet?page=-1", 400, errorCode("INVALID_ARGUMENT")),
		get("floor page over limit", v1+"/trading/floorsheet?page=1000000", 400, errorCode("INVALID_ARGUMENT")),
		get("date without symbol", v1+"/trading/floorsheet?business_date=2026-11-05", 400, errorCode("INVALID_ARGUMENT")),
		get("bad date", v1+"/securities/NABIL/price-history?start_date=2026-13-45", 400, errorCode("INVALID_ARGUMENT")),
		get("reversed range", v1+"/securities/NABIL/price-history?start_date=2026-03-01&end_date=2026-02-01", 400, errorCode("INVALID_ARGUMENT")),
		get("unknown sector", v1+"/sectors/unknown/securities", 404, errorCode("NOT_FOUND")),
		{name: "post not allowed", method: http.MethodPost, url: v1 + "/market/summary", status: 405, check: errorCode("METHOD_NOT_ALLOWED")},
	}
	for name, path := range indexGraphs {
		cases = append(cases, get("index graph "+name, v1+"/market/graphs/indices/"+name, 200, dataIs(path, 0, "endpoint")))
	}
	for slug, path := range sectorGraphs {
		cases = append(cases, get("sector graph "+slug, v1+"/market/graphs/sectors/"+slug, 200, dataIs(path, 0, "endpoint")))
	}
	return cases
}

func TestRESTRoutesReachTheirIntendedUpstreamEndpoints(t *testing.T) {
	u := newFakeUpstream(t)
	handler := newRESTHandler(t, newApp(t, u))

	for _, c := range restCases(u) {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(c.method, c.url, nil))
			if rec.Code != c.status {
				t.Fatalf("%s %s: status %d, want %d: %s", c.method, c.url, rec.Code, c.status, rec.Body)
			}
			if c.name == "health live" {
				return
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not JSON: %v: %s", err, rec.Body)
			}
			c.check(t, body)
			if c.status == 405 && rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("Allow = %q", rec.Header().Get("Allow"))
			}
		})
	}

	t.Run("upstream request details", func(t *testing.T) {
		if got := u.lastURI("GET /api/nots/market/history/security/131"); !strings.Contains(got, "?&size=500&startDate=") || !strings.Contains(got, "&endDate=") {
			t.Errorf("price history request = %q", got)
		}
		if got := u.lastURI("POST /api/nots/nepse-data/floorsheet"); !strings.Contains(got, "?&size=500&sort=contractId,desc&page=2") {
			t.Errorf("symbol filtering must page through the supported all-market endpoint; last request = %q", got)
		}
		if got := u.lastURI("GET /api/nots/nepse-data/marketdepth/131/"); got != "/api/nots/nepse-data/marketdepth/131/" {
			t.Errorf("market depth request = %q", got)
		}
	})

	t.Run("price history default range is the last 365 days", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/securities/NABIL/price-history", nil))
		uri := u.lastURI("GET /api/nots/market/history/security/131")
		end := time.Now().In(time.FixedZone("npt", 5*3600+45*60))
		if !strings.Contains(uri, "&endDate="+end.Format(time.DateOnly)) &&
			!strings.Contains(uri, "&endDate="+end.AddDate(0, 0, -1).Format(time.DateOnly)) &&
			!strings.Contains(uri, "&endDate="+end.AddDate(0, 0, 1).Format(time.DateOnly)) {
			t.Errorf("default end date is not today (Nepal time): %q", uri)
		}
	})

	t.Run("health and docs", func(t *testing.T) {
		for _, c := range []struct {
			path   string
			status int
			want   string
		}{
			{"/", 200, `"status":"ok"`},
			{"/health/live", 200, `"status":"ok"`},
			{"/health/ready", 200, `"status":"ok"`},
			{"/health/dependencies", 200, `"nepse":"ok"`},
			{"/openapi.json", 200, `"openapi": "3.0.3"`},
			{"/docs", 302, ""},
		} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.want) {
				t.Errorf("GET %s = %d %q, want %d containing %q", c.path, rec.Code, rec.Body.String(), c.status, c.want)
			}
		}
	})

	// Every upstream endpoint represented by the REST route contract must be reachable from at least one REST route. The MCP package checks its own map.
	if !t.Failed() {
		u.assertAllEndpointsUsed()
	}
}
