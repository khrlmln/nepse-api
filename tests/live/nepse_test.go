package live_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserver "nepse-api/src/mcp"
	"nepse-api/src/rest"
	"nepse-api/src/shared/application"
	"nepse-api/src/shared/config"
	securitysvc "nepse-api/src/shared/securities"
	"nepse-api/src/shared/trading"
)

// TestNEPSELiveServices verifies that both public transports can retrieve current, read-only market data through the real NEPSE client. It runs as part of `go test ./...` and requires outbound access to nepalstock.com.
var liveHTTPClient = &http.Client{Timeout: 20 * time.Second}

// liveSetup loads the configuration from the environment (like the binaries do) and builds the application against the real NEPSE.
func liveSetup(t *testing.T) (config.REST, config.MCP, *application.App, *slog.Logger) {
	t.Helper()
	restConfig, err := config.LoadREST()
	if err != nil {
		t.Fatalf("load REST configuration: %v", err)
	}
	mcpConfig, err := config.LoadMCP()
	if err != nil {
		t.Fatalf("load MCP configuration: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := application.New(restConfig.Common, logger)
	if err != nil {
		t.Fatalf("initialize NEPSE client: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Close(ctx); err != nil {
			t.Errorf("close NEPSE client: %v", err)
		}
	})
	return restConfig, mcpConfig, app, logger
}

func TestNEPSELiveServices(t *testing.T) {
	restConfig, mcpConfig, app, logger := liveSetup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	if err := app.Client.Ping(ctx); err != nil {
		t.Fatalf("NEPSE authentication/market-status request failed: %v", err)
	}
	securities, err := app.Client.SecurityList(ctx)
	if err != nil {
		t.Fatalf("fetch current NEPSE security list: %v", err)
	}
	if len(securities) == 0 {
		t.Fatal("NEPSE returned an empty security list")
	}
	if _, err := app.Market.Summary(ctx); err != nil {
		t.Fatalf("fetch current NEPSE market summary: %v", err)
	}
	// Select a symbol from the live list instead of relying on a hard-coded ticker.
	symbol := ""
	for _, security := range securities {
		if candidate, ok := security["symbol"].(string); ok && candidate != "" {
			symbol = candidate
			break
		}
	}
	if symbol == "" {
		t.Fatal("NEPSE security list contains no usable symbol")
	}
	if _, err := app.Securities.PriceVolumeHistory(ctx, securitysvc.HistoryRequest{Symbol: symbol}); err != nil {
		t.Fatalf("fetch current price history for %s: %v", symbol, err)
	}
	if page, err := app.Trading.Floorsheet(ctx, trading.FloorsheetRequest{}); err != nil || page.Rows == nil {
		t.Fatalf("fetch current floorsheet: rows=%d error=%v", len(page.Rows), err)
	}

	// Exercise the actual REST route table against current upstream data.
	restService, err := rest.New(restConfig, app, logger)
	if err != nil {
		t.Fatalf("create REST server: %v", err)
	}
	restHTTP := httptest.NewServer(restService.Handler())
	t.Cleanup(restHTTP.Close)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, restHTTP.URL+"/api/v1/market/summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := liveHTTPClient.Do(request)
	if err != nil {
		t.Fatalf("REST market summary request: %v", err)
	}
	var restBody struct {
		Data map[string]any `json:"data"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&restBody)
	_ = response.Body.Close()
	if decodeErr != nil || response.StatusCode != http.StatusOK || len(restBody.Data) == 0 {
		t.Fatalf("REST market summary: status=%d data=%v decode_error=%v", response.StatusCode, restBody.Data, decodeErr)
	}

	request, err = http.NewRequestWithContext(ctx, http.MethodGet, restHTTP.URL+"/api/v1/securities?limit=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = liveHTTPClient.Do(request)
	if err != nil {
		t.Fatalf("REST security-list request: %v", err)
	}
	var securitiesBody struct {
		Data []map[string]any `json:"data"`
	}
	decodeErr = json.NewDecoder(response.Body).Decode(&securitiesBody)
	_ = response.Body.Close()
	if decodeErr != nil || response.StatusCode != http.StatusOK || len(securitiesBody.Data) == 0 {
		t.Fatalf("REST security list: status=%d items=%d decode_error=%v", response.StatusCode, len(securitiesBody.Data), decodeErr)
	}

	// Technical-analysis history is selected from the live security list.
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, restHTTP.URL+"/api/v1/securities/"+url.PathEscape(symbol)+"/price-history", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = liveHTTPClient.Do(request)
	if err != nil {
		t.Fatalf("REST price-history request: %v", err)
	}
	var historyBody struct {
		Data json.RawMessage `json:"data"`
	}
	decodeErr = json.NewDecoder(response.Body).Decode(&historyBody)
	_ = response.Body.Close()
	if decodeErr != nil || response.StatusCode != http.StatusOK || len(historyBody.Data) == 0 || string(historyBody.Data) == "null" {
		t.Fatalf("REST price history for %s: status=%d data=%s decode_error=%v", symbol, response.StatusCode, historyBody.Data, decodeErr)
	}

	// A closed market may legitimately return an empty floorsheet page.
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, restHTTP.URL+"/api/v1/trading/floorsheet?page=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = liveHTTPClient.Do(request)
	if err != nil {
		t.Fatalf("REST floorsheet request: %v", err)
	}
	var floorsheetBody struct {
		Data json.RawMessage `json:"data"`
	}
	decodeErr = json.NewDecoder(response.Body).Decode(&floorsheetBody)
	_ = response.Body.Close()
	if decodeErr != nil || response.StatusCode != http.StatusOK || len(floorsheetBody.Data) == 0 || string(floorsheetBody.Data) == "null" {
		t.Fatalf("REST floorsheet: status=%d data=%s decode_error=%v", response.StatusCode, floorsheetBody.Data, decodeErr)
	}

	// Exercise the MCP Streamable HTTP transport without caller credentials.
	mcpService, err := mcpserver.New(mcpConfig, app, logger)
	if err != nil {
		t.Fatalf("create MCP server: %v", err)
	}
	mcpHTTP := httptest.NewServer(mcpService.Handler())
	t.Cleanup(mcpHTTP.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "nepse-live-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: mcpHTTP.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect to MCP without caller authentication: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list MCP tools: %v", err)
	}
	foundSummaryTool := false
	for _, tool := range tools.Tools {
		if tool.Name == "get_market_summary" {
			foundSummaryTool = true
			break
		}
	}
	if !foundSummaryTool {
		t.Fatal("MCP tool list does not contain get_market_summary")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_market_summary", Arguments: map[string]any{}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("MCP market summary tool: result=%+v error=%v", result, err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "list_securities", Arguments: map[string]any{}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("MCP live security-list tool: result=%+v error=%v", result, err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_price_history", Arguments: map[string]any{"symbol": symbol}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("MCP price-history tool for %s: result=%+v error=%v", symbol, result, err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_floorsheet", Arguments: map[string]any{}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("MCP floorsheet tool: result=%+v error=%v", result, err)
	}
}
