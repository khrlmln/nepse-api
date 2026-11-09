package routes_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserver "nepse-api/src/mcp"
	"nepse-api/src/shared/config"
)

func connectMCP(t *testing.T, u *fakeUpstream) *mcp.ClientSession {
	t.Helper()
	app := newApp(t, u)
	svc, err := mcpserver.New(config.MCP{
		Common:      config.Common{ServiceName: "mcp-routes-test", SlowRequestThreshold: time.Hour},
		ToolTimeout: 10 * time.Second, MaxConcurrent: 16,
	}, app, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("create MCP server: %v", err)
	}
	httpSrv := httptest.NewServer(svc.Handler())
	t.Cleanup(httpSrv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "routes-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpSrv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type toolCase struct {
	tool   string
	args   map[string]any
	isErr  string // expected error code, "" for success
	checks []func(*testing.T, map[string]any)
}

func call(tool string, args map[string]any, checks ...func(*testing.T, map[string]any)) toolCase {
	return toolCase{tool: tool, args: args, checks: checks}
}

func fails(tool string, args map[string]any, code string) toolCase {
	return toolCase{tool: tool, args: args, isErr: code, checks: []func(*testing.T, map[string]any){errorCode(code)}}
}

func mcpCases() []toolCase {
	cases := []toolCase{
		call("get_market_summary", nil, dataIs(123456.5, "Total Turnover Rs:")),
		call("get_market_status", map[string]any{}, both(dataIs("CLOSE", "isOpen"), dataIs(7, "id"))),
		call("get_nepse_indices", nil, dataIs(2000.5, "NEPSE Index", "currentValue")),
		call("get_sub_indices", nil, dataIs(1500.2, "Banking SubIndex", "currentValue")),
		call("get_live_market", nil, both(lenIs(3), metaIs(3, "total"), metaIs(1, "page"))),
		call("get_live_market", map[string]any{"limit": 2, "page": 2}, both(lenIs(1), dataIs("LIVE3", 0, "symbol"), metaIs(2, "page"), metaIs(2, "limit"), metaIs(3, "total"))),
		call("get_top_gainers", nil, dataIs("GAIN", 0, "symbol")),
		call("get_top_losers", nil, dataIs("LOSE", 0, "symbol")),
		call("get_top_turnover", nil, dataIs("TURN", 0, "symbol")),
		call("get_top_transactions", nil, dataIs("TXN", 0, "symbol")),
		call("get_top_traded_shares", nil, dataIs("TRD", 0, "symbol")),
		call("get_supply_demand", nil, dataIs("SUP", "supplyList", 0, "symbol")),
		call("get_index_graph", nil, dataIs("/api/nots/graph/index/58", 0, "endpoint")),
		call("get_index_graph", map[string]any{"name": "sensitive-float"}, dataIs("/api/nots/graph/index/63", 0, "endpoint")),
		call("get_sector_graph", map[string]any{"sector": "hydropower"}, dataIs("/api/nots/graph/index/54", 0, "endpoint")),
		call("get_turnover_overview", nil, both(dataIs(110, "scripsDetails", "GAIN", "ltp"), dataIs("HydroPower Index", "sectorsDetails", "Hydro Power", "turnover", "index"))),

		call("list_securities", nil, both(lenIs(3), metaIs(3, "total"))),
		call("list_securities", map[string]any{"query": "gain"}, both(lenIs(1), dataIs("GAIN", 0, "symbol"))),
		call("list_securities", map[string]any{"limit": 1, "page": 3}, both(lenIs(1), dataIs("GAIN", 0, "symbol"))),
		call("validate_symbol", map[string]any{"symbol": "nabil"}, both(dataIs(true, "valid"), dataIs("NABIL", "symbol"))),
		fails("validate_symbol", map[string]any{"symbol": "NABL"}, "NOT_FOUND"),
		fails("validate_symbol", map[string]any{"symbol": "bad$"}, "INVALID_ARGUMENT"),
		call("get_security_details", map[string]any{"symbol": "NABIL"}, dataIs("details", "marker")),
		call("get_market_depth", map[string]any{"symbol": "NABIL"}, dataIs("depth", "marketDepth", "marker")),
		call("get_security_price_volume", map[string]any{"symbol": "NABIL"}, dataIs(990, "previousClose")),
		fails("get_security_price_volume", map[string]any{"symbol": "PRMT"}, "NOT_FOUND"),
		call("get_price_history", map[string]any{"symbol": "NABIL", "start_date": "2026-01-01", "end_date": "2026-02-01"}, both(dataIs("history", 0, "marker"), metaIs(1, "total"))),
		fails("get_price_history", map[string]any{"symbol": "NABIL", "start_date": "2026-03-01", "end_date": "2026-02-01"}, "INVALID_ARGUMENT"),
		call("get_security_graph", map[string]any{"symbol": "NABIL"}, dataIs("daily-graph", 0, "marker")),
		call("list_sectors", nil, both(lenIs(3), dataIs("Promoter Share", 2))),
		call("get_sector_securities", map[string]any{"sector": "Hydro Power"}, both(dataIs("Hydro Power", "sector"), dataIs("GAIN", "securities", 0, "symbol"))),
		fails("get_sector_securities", map[string]any{"sector": "nonsense"}, "NOT_FOUND"),
		call("get_floorsheet", nil, both(dataIs("ALL-1", 0, "contractId"), metaIs(500, "page_size"), metaIs(3, "total_pages"))),
		call("get_floorsheet", map[string]any{"symbol": "NABIL", "business_date": "2026-11-05", "page": 0}, both(dataIs("NABIL-1", 0, "contractId"), metaIs(3, "total_elements"))),
		fails("get_floorsheet", map[string]any{"page": -1}, "INVALID_ARGUMENT"),
		fails("get_floorsheet", map[string]any{"business_date": "2026-11-05"}, "INVALID_ARGUMENT"),
	}
	for name := range indexGraphs {
		cases = append(cases, call("get_index_graph", map[string]any{"name": name}, dataIs(indexGraphs[name], 0, "endpoint")))
	}
	for slug := range sectorGraphs {
		cases = append(cases, call("get_sector_graph", map[string]any{"sector": slug}, dataIs(sectorGraphs[slug], 0, "endpoint")))
	}
	return cases
}

// wantTools is the exact public MCP surface.
var wantTools = []string{
	"get_floorsheet", "get_index_graph", "get_live_market", "get_market_depth", "get_market_status",
	"get_market_summary", "get_nepse_indices", "get_price_history", "get_sector_graph", "get_sector_securities",
	"get_security_details", "get_security_graph", "get_security_price_volume", "get_sub_indices", "get_supply_demand",
	"get_top_gainers", "get_top_losers", "get_top_traded_shares", "get_top_transactions", "get_top_turnover",
	"get_turnover_overview", "list_sectors", "list_securities", "validate_symbol",
}

func TestMCPToolsListMatchesThePublicSurfaceAndSchemas(t *testing.T) {
	session := connectMCP(t, newFakeUpstream(t))
	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	props := map[string]map[string]bool{}
	required := map[string][]string{}
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not annotated read-only", tool.Name)
		}
		if tool.Description == "" || tool.Title == "" {
			t.Errorf("%s lacks a title or description", tool.Name)
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: schema: %v", tool.Name, err)
		}
		props[tool.Name] = map[string]bool{}
		for p := range schema.Properties {
			props[tool.Name][p] = true
		}
		required[tool.Name] = schema.Required
	}
	sort.Strings(got)
	if len(got) != len(wantTools) {
		t.Fatalf("tools = %v\nwant    %v", got, wantTools)
	}
	for i := range got {
		if got[i] != wantTools[i] {
			t.Fatalf("tools = %v\nwant    %v", got, wantTools)
		}
	}
	// Embedded structs must be flattened into the advertised schema, and required
	// inputs must be required.
	for tool, want := range map[string][]string{
		"list_securities":   {"query", "page", "limit"},
		"get_live_market":   {"page", "limit"},
		"get_price_history": {"symbol", "start_date", "end_date", "page", "limit"},
		"get_floorsheet":    {"symbol", "business_date", "page"},
		"validate_symbol":   {"symbol"},
		"get_index_graph":   {"name"},
	} {
		for _, p := range want {
			if !props[tool][p] {
				t.Errorf("%s: input schema lacks %q (has %v)", tool, p, props[tool])
			}
		}
	}
	for tool, want := range map[string]string{"validate_symbol": "symbol", "get_price_history": "symbol", "get_sector_securities": "sector", "get_sector_graph": "sector"} {
		found := false
		for _, r := range required[tool] {
			found = found || r == want
		}
		if !found {
			t.Errorf("%s: %q is not required (required=%v)", tool, want, required[tool])
		}
	}
}

func TestMCPToolsReachTheirIntendedUpstreamEndpoints(t *testing.T) {
	u := newFakeUpstream(t)
	session := connectMCP(t, u)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	for _, c := range mcpCases() {
		name := c.tool
		if len(c.args) > 0 {
			b, _ := json.Marshal(c.args)
			name += " " + string(b)
		}
		t.Run(name, func(t *testing.T) {
			res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
			if err != nil {
				t.Fatalf("protocol error: %v", err)
			}
			if res.IsError != (c.isErr != "") {
				t.Fatalf("IsError = %v, want error code %q: %+v", res.IsError, c.isErr, res.Content)
			}
			body, ok := res.StructuredContent.(map[string]any)
			if !ok {
				t.Fatalf("no structured content: %+v", res)
			}
			for _, check := range c.checks {
				check(t, body)
			}
		})
	}

	if !t.Failed() {
		u.assertAllEndpointsUsed()
	}
}

func TestRESTAndMCPReturnTheSameData(t *testing.T) {
	u := newFakeUpstream(t)
	app := newApp(t, u)
	handler := newRESTHandler(t, app)
	session := connectMCP(t, u)

	for _, c := range []struct {
		rest string
		tool string
		args map[string]any
	}{
		{"/api/v1/market/summary", "get_market_summary", nil},
		{"/api/v1/market/status", "get_market_status", nil},
		{"/api/v1/market/index", "get_nepse_indices", nil},
		{"/api/v1/market/sub-indices", "get_sub_indices", nil},
		{"/api/v1/market/top-gainers", "get_top_gainers", nil},
		{"/api/v1/market/top-losers", "get_top_losers", nil},
		{"/api/v1/market/top-turnover", "get_top_turnover", nil},
		{"/api/v1/market/top-transactions", "get_top_transactions", nil},
		{"/api/v1/market/top-trades", "get_top_traded_shares", nil},
		{"/api/v1/market/supply-demand", "get_supply_demand", nil},
		{"/api/v1/market/turnover-overview", "get_turnover_overview", nil},
		{"/api/v1/market/graphs/indices/float", "get_index_graph", map[string]any{"name": "float"}},
		{"/api/v1/market/graphs/sectors/finance", "get_sector_graph", map[string]any{"sector": "finance"}},
		{"/api/v1/securities/NABIL", "get_security_details", map[string]any{"symbol": "NABIL"}},
		{"/api/v1/securities/NABIL/market-depth", "get_market_depth", map[string]any{"symbol": "NABIL"}},
		{"/api/v1/securities/NABIL/price-volume", "get_security_price_volume", map[string]any{"symbol": "NABIL"}},
		{"/api/v1/securities/NABIL/graph", "get_security_graph", map[string]any{"symbol": "NABIL"}},
		{"/api/v1/sectors", "list_sectors", nil},
		{"/api/v1/trading/floorsheet?page=1", "get_floorsheet", map[string]any{"page": 1}},
		{"/api/v1/trading/floorsheet/NABIL", "get_floorsheet", map[string]any{"symbol": "NABIL"}},
		{"/api/v1/securities/NABIL/validate", "validate_symbol", map[string]any{"symbol": "NABIL"}},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", c.rest, nil))
		var restBody map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &restBody); err != nil || rec.Code != 200 {
			t.Fatalf("%s: %d %v %s", c.rest, rec.Code, err, rec.Body)
		}
		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", c.tool, err, res)
		}
		mcpBody := res.StructuredContent.(map[string]any)
		restData, _ := json.Marshal(restBody["data"])
		mcpData, _ := json.Marshal(mcpBody["data"])
		if string(restData) != string(mcpData) {
			t.Errorf("%s and %s differ:\n rest: %s\n mcp:  %s", c.rest, c.tool, restData, mcpData)
		}
		restMeta, _ := json.Marshal(restBody["meta"])
		mcpMeta, _ := json.Marshal(mcpBody["meta"])
		if string(restMeta) != string(mcpMeta) {
			t.Errorf("%s and %s meta differ:\n rest: %s\n mcp:  %s", c.rest, c.tool, restMeta, mcpMeta)
		}
	}
}
