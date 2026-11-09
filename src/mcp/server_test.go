package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"nepse-api/src/shared/application"
	"nepse-api/src/shared/config"
	"nepse-api/src/shared/market"
	"nepse-api/src/shared/securities"
	"nepse-api/src/shared/trading"
)

func TestCORSPolicyAllowsOnlyConfiguredOrigins(t *testing.T) {
	calls := 0
	handler := cors([]string{"https://internal.example"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))

	allowed := httptest.NewRecorder()
	allowedRequest := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	allowedRequest.Header.Set("Origin", "https://internal.example")
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusNoContent || allowed.Header().Get("Access-Control-Allow-Origin") != "https://internal.example" {
		t.Fatalf("allowed origin response: status=%d headers=%v", allowed.Code, allowed.Header())
	}

	preflight := httptest.NewRecorder()
	preflightRequest := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	preflightRequest.Header.Set("Origin", "https://internal.example")
	handler.ServeHTTP(preflight, preflightRequest)
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Fatalf("allowed preflight response: status=%d headers=%v", preflight.Code, preflight.Header())
	}

	denied := httptest.NewRecorder()
	deniedRequest := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	deniedRequest.Header.Set("Origin", "https://untrusted.example")
	handler.ServeHTTP(denied, deniedRequest)
	if denied.Code != http.StatusForbidden {
		t.Errorf("untrusted origin status = %d, want %d", denied.Code, http.StatusForbidden)
	}

	withoutOrigin := httptest.NewRecorder()
	handler.ServeHTTP(withoutOrigin, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if withoutOrigin.Code != http.StatusNoContent {
		t.Errorf("request without Origin status = %d", withoutOrigin.Code)
	}
	if calls != 2 {
		t.Errorf("downstream handler called %d times, want 2", calls)
	}
}

func TestMCPServerRegistersToolsWithoutCallerAuthentication(t *testing.T) {
	app := &application.App{
		Market:     market.New(nil),
		Securities: securities.New(nil),
		Trading:    trading.New(nil),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := New(config.MCP{
		Common:        config.Common{ServiceName: "mcp-test"},
		ToolTimeout:   5 * time.Second,
		MaxConcurrent: 8,
	}, app, logger)
	if err != nil {
		t.Fatalf("create MCP server: %v", err)
	}

	httpServer := httptest.NewServer(service.Handler())
	t.Cleanup(httpServer.Close)

	probeClient := &http.Client{Timeout: 2 * time.Second}
	probe, err := probeClient.Get(httpServer.URL + "/health/live")
	if err != nil {
		t.Fatalf("call liveness endpoint: %v", err)
	}
	probe.Body.Close()
	if probe.StatusCode != http.StatusOK {
		t.Fatalf("liveness status = %d, want %d", probe.StatusCode, http.StatusOK)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-server-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect without caller credentials: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools without caller credentials: %v", err)
	}
	found := map[string]bool{}
	for _, tool := range tools.Tools {
		found[tool.Name] = true
	}
	want := []string{
		"get_market_summary", "get_market_status", "get_nepse_indices", "get_sub_indices",
		"get_live_market", "get_top_gainers", "get_top_losers", "get_top_turnover",
		"get_top_transactions", "get_top_traded_shares", "get_supply_demand",
		"get_index_graph", "get_sector_graph", "get_turnover_overview",
		"list_securities", "validate_symbol", "get_security_details", "get_market_depth",
		"get_security_price_volume", "get_price_history", "get_security_graph",
		"list_sectors", "get_sector_securities", "get_floorsheet",
	}
	if len(found) != len(want) {
		t.Errorf("registered %d MCP tools, want %d", len(found), len(want))
	}
	for _, name := range want {
		if !found[name] {
			t.Errorf("MCP tool list is missing %q", name)
		}
	}
}
