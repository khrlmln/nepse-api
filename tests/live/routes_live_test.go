package live_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nepse-api/src/rest"
	"nepse-api/src/shared/market"
	"nepse-api/src/shared/trading"
)

// TestNEPSELiveEveryRESTRoute calls every business route against the real NEPSE. The offline suite (tests/routes) proves each route is wired to the intended endpoint; this proves those endpoints still answer, and that the protected POST payloads (scrip, graph and floorsheet modes) are accepted by NEPSE. Outside trading hours a route may legitimately return little data, but it must still succeed.
func TestNEPSELiveEveryRESTRoute(t *testing.T) {
	restConfig, _, app, logger := liveSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 170*time.Second)
	defer cancel()

	server, err := rest.New(restConfig, app, logger)
	if err != nil {
		t.Fatalf("create REST server: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	// Determine whether the market-dependent order-depth endpoint can be meaningfully probed. Symbol-specific floorsheets are served by filtering the supported all-market session pages in the application.
	marketStatus, err := app.Market.Status(ctx)
	if err != nil {
		t.Fatalf("fetch market status before live route checks: %v", err)
	}
	marketOpen, marketStatusKnown := parseMarketOpen(marketStatus["isOpen"])
	if marketStatusKnown {
		t.Logf("NEPSE market open: %t", marketOpen)
	} else {
		t.Logf("NEPSE market status is not a recognized open/closed value: %v", marketStatus["isOpen"])
	}

	// Prefer an actively traded symbol; fall back to the first listed one for endpoints that are valid outside trading hours too.
	symbol := ""
	if top, err := app.Market.Top(ctx, market.TopTurnover); err == nil {
		for _, row := range top {
			if m, ok := row.(map[string]any); ok {
				if s, ok := m["symbol"].(string); ok && s != "" {
					symbol = s
					break
				}
			}
		}
	}
	if symbol == "" {
		list, err := app.Client.SecurityList(ctx)
		if err != nil || len(list) == 0 {
			t.Fatalf("no symbol available: %v", err)
		}
		symbol, _ = list[0]["symbol"].(string)
	}
	sectors, err := app.Securities.Sectors(ctx)
	if err != nil || len(sectors) == 0 {
		t.Fatalf("no sectors available: %v", err)
	}
	t.Logf("using symbol %s and sector %q", symbol, sectors[0])

	// A market-depth request should use a symbol that is present in the live market table, not a potentially stale top-turnover entry.
	marketDepthSymbol := ""
	marketDepthSkip := ""
	if marketStatusKnown && !marketOpen {
		marketDepthSkip = "NEPSE reports the market closed; order depth is only meaningful during trading."
	} else if liveRows, liveErr := app.Market.Live(ctx); liveErr == nil {
		marketDepthSymbol = firstSymbol(liveRows)
		if marketDepthSymbol == "" {
			marketDepthSkip = "NEPSE returned no live-market symbol to use for an order-depth request."
		}
	} else {
		marketDepthSkip = "could not discover a currently traded symbol for the market-depth request: " + liveErr.Error()
	}
	if marketDepthSymbol == "" {
		marketDepthSymbol = symbol
	}

	// The all-market endpoint returns the latest available trading session, which can differ from today's calendar date when the market is closed. Select the symbol and business date from the same row so the application validates the symbol-filter request against that exact session.
	floorPage, floorErr := app.Trading.Floorsheet(ctx, trading.FloorsheetRequest{})
	if floorErr != nil {
		t.Fatalf("discover a symbol/date from the latest all-market floorsheet page: %v", floorErr)
	}
	floorTrade := firstFloorTrade(floorPage.Rows)
	floorSymbol := floorTrade.Symbol
	floorSheetSkip := ""
	if floorSymbol == "" || floorTrade.BusinessDate == "" {
		floorSheetSkip = "NEPSE did not return a floorsheet row with both a symbol and business date; there is no safe symbol/date pair to query."
		floorSymbol = symbol
	} else {
		t.Logf("using floorsheet symbol %s for business date %s", floorSymbol, floorTrade.BusinessDate)
	}

	sym := url.PathEscape(symbol)
	routes := []struct {
		path       string
		allowed    []int // default: 200 only
		skipReason string
	}{
		{path: "/api/v1/market/summary"}, {path: "/api/v1/market/status"}, {path: "/api/v1/market/index"},
		{path: "/api/v1/market/sub-indices"}, {path: "/api/v1/market/live"}, {path: "/api/v1/market/supply-demand"},
		{path: "/api/v1/market/turnover-overview"},
		{path: "/api/v1/market/top-gainers"}, {path: "/api/v1/market/top-losers"}, {path: "/api/v1/market/top-turnover"},
		{path: "/api/v1/market/top-transactions"}, {path: "/api/v1/market/top-trades"},
		{path: "/api/v1/securities?limit=5"}, {path: "/api/v1/securities/" + sym}, {path: "/api/v1/securities/" + sym + "/validate"},
		{path: "/api/v1/securities/" + url.PathEscape(marketDepthSymbol) + "/market-depth", skipReason: marketDepthSkip},
		{path: "/api/v1/securities/" + sym + "/price-volume", allowed: []int{200, 404}}, // 404: no trade today
		{path: "/api/v1/securities/" + sym + "/price-history"}, {path: "/api/v1/securities/" + sym + "/graph"},
		{path: "/api/v1/sectors"}, {path: "/api/v1/sectors/" + url.PathEscape(sectors[0]) + "/securities"},
		{path: "/api/v1/trading/floorsheet?page=0"},
		{path: floorSheetRoute(floorSymbol, floorTrade.BusinessDate), skipReason: floorSheetSkip},
	}
	for name := range indexGraphNames {
		routes = append(routes, struct {
			path       string
			allowed    []int
			skipReason string
		}{path: "/api/v1/market/graphs/indices/" + name})
	}
	for slug := range sectorGraphNames {
		routes = append(routes, struct {
			path       string
			allowed    []int
			skipReason string
		}{path: "/api/v1/market/graphs/sectors/" + slug})
	}

	for _, r := range routes {
		t.Run(r.path, func(t *testing.T) {
			if r.skipReason != "" {
				t.Skip(r.skipReason)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+r.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := liveHTTPClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			var body map[string]any
			decodeErr := json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()
			ok := resp.StatusCode == http.StatusOK
			for _, status := range r.allowed {
				ok = ok || resp.StatusCode == status
			}
			if !ok || decodeErr != nil {
				t.Fatalf("status=%d decode_error=%v body=%v", resp.StatusCode, decodeErr, body)
			}
		})
	}

	// Every sector the turnover overview reports should join its sub-index. A null here means a name in the sector-to-sub-index table no longer matches NEPSE's sub-index names (see sectorIndex in src/shared/market/composite.go).
	overview, err := app.Market.TurnoverOverview(ctx)
	if err != nil {
		t.Fatalf("turnover overview: %v", err)
	}
	if details, ok := overview.(map[string]any)["sectorsDetails"].(map[string]map[string]any); ok {
		for sector, d := range details {
			if d["turnover"] == nil {
				t.Logf("WARNING: sector %q has no matching NEPSE sub-index in the turnover overview", sector)
			}
		}
	}
}

var indexGraphNames = map[string]struct{}{"nepse": {}, "sensitive": {}, "float": {}, "sensitive-float": {}}

var sectorGraphNames = map[string]struct{}{
	"banking": {}, "development-bank": {}, "finance": {}, "hotels-tourism": {}, "hydropower": {}, "investment": {},
	"life-insurance": {}, "manufacturing": {}, "microfinance": {}, "mutual-fund": {}, "non-life-insurance": {},
	"others": {}, "trading": {},
}

// parseMarketOpen returns whether NEPSE reports the market as open and whether the value was recognized. Unknown formats are not treated as closed.
func parseMarketOpen(value any) (open, known bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "open", "true", "1", "yes":
			return true, true
		case "close", "closed", "false", "0", "no":
			return false, true
		}
	}
	return false, false
}

func firstSymbol(rows []any) string {
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"symbol", "stockSymbol"} {
			if symbol, ok := row[key].(string); ok && strings.TrimSpace(symbol) != "" {
				return strings.TrimSpace(symbol)
			}
		}
	}
	return ""
}

type floorTrade struct {
	Symbol       string
	BusinessDate string
}

// firstFloorTrade selects a real row and carries its business date forward. The symbol-scoped application route filters the latest all-market session; supplying the row's date verifies that the request targets that session.
func firstFloorTrade(rows []json.RawMessage) floorTrade {
	for _, raw := range rows {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(raw, &row); err != nil {
			continue
		}
		var symbol string
		for _, key := range []string{"stockSymbol", "symbol"} {
			if value, ok := row[key]; ok && json.Unmarshal(value, &symbol) == nil {
				symbol = strings.TrimSpace(symbol)
				if symbol != "" {
					break
				}
			}
		}
		var rawDate string
		if symbol == "" {
			continue
		}
		for _, key := range []string{"businessDate", "business_date"} {
			if value, ok := row[key]; ok && json.Unmarshal(value, &rawDate) == nil {
				if date := normalizeBusinessDate(rawDate); date != "" {
					return floorTrade{Symbol: symbol, BusinessDate: date}
				}
			}
		}
	}
	return floorTrade{}
}

func normalizeBusinessDate(value string) string {
	value = strings.TrimSpace(value)
	layouts := []string{
		time.DateOnly,
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format(time.DateOnly)
		}
	}
	return ""
}

func floorSheetRoute(symbol, businessDate string) string {
	path := "/api/v1/trading/floorsheet/" + url.PathEscape(symbol)
	if businessDate == "" {
		return path
	}
	return path + "?business_date=" + url.QueryEscape(businessDate)
}

func TestParseMarketOpen(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  bool
		known bool
	}{
		{name: "open string", value: "OPEN", want: true, known: true},
		{name: "closed string", value: "CLOSE", want: false, known: true},
		{name: "bool open", value: true, want: true, known: true},
		{name: "bool closed", value: false, want: false, known: true},
		{name: "unknown", value: "trading", want: false, known: false},
		{name: "missing", value: nil, want: false, known: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := parseMarketOpen(tt.value)
			if got != tt.want || known != tt.known {
				t.Fatalf("parseMarketOpen(%v) = (%t, %t), want (%t, %t)", tt.value, got, known, tt.want, tt.known)
			}
		})
	}
}

func TestFirstFloorTradePreservesBusinessDate(t *testing.T) {
	rows := []json.RawMessage{
		json.RawMessage(`{"contractId":0,"stockSymbol":"STALE","businessDate":null}`),
		json.RawMessage(`{"contractId":1,"stockSymbol":"GCIL","businessDate":"2026-11-05"}`),
		json.RawMessage(`{"contractId":2,"stockSymbol":"NABIL","businessDate":"2026-11-04"}`),
	}
	want := floorTrade{Symbol: "GCIL", BusinessDate: "2026-11-05"}
	if got := firstFloorTrade(rows); got != want {
		t.Fatalf("firstFloorTrade() = %+v, want %+v", got, want)
	}
	if got := firstFloorTrade(nil); got != (floorTrade{}) {
		t.Fatalf("firstFloorTrade(nil) = %+v, want empty", got)
	}
}

func TestNormalizeBusinessDate(t *testing.T) {
	tests := []struct{ input, want string }{
		{"2026-11-05", "2026-11-05"},
		{"2026-11-05T10:15:00+05:45", "2026-11-05"},
		{"2026-11-05 10:15:00", "2026-11-05"},
		{"2026-11-05T10:15:00.123", "2026-11-05"},
		{"2026-11-05-not-a-date", ""},
		{"not-a-date", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeBusinessDate(tt.input); got != tt.want {
				t.Fatalf("normalizeBusinessDate(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFloorSheetRouteIncludesBusinessDate(t *testing.T) {
	want := "/api/v1/trading/floorsheet/GCIL?business_date=2026-11-05"
	if got := floorSheetRoute("GCIL", "2026-11-05"); got != want {
		t.Fatalf("floorSheetRoute() = %q, want %q", got, want)
	}
}
