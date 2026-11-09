package rest

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nepse-api/src/shared/application"
	"nepse-api/src/shared/config"
	"nepse-api/src/shared/httpmw"
	"nepse-api/src/shared/market"
	"nepse-api/src/shared/securities"
	"nepse-api/src/shared/trading"
)

func testRouteTable() []route {
	app := &application.App{
		Market:     market.New(nil),
		Securities: securities.New(nil),
		Trading:    trading.New(nil),
	}
	cfg := config.REST{Common: config.Common{ServiceName: "rest-test"}, DocsEnabled: true}
	return routes(app, cfg, &httpmw.Drain{})
}

func TestRouteTableAndOpenAPIDocumentStayInSync(t *testing.T) {
	table := testRouteTable()
	if len(table) == 0 {
		t.Fatal("route table is empty")
	}

	patterns := make(map[string]struct{}, len(table))
	operationIDs := make(map[string]struct{}, len(table))
	for _, r := range table {
		if r.Pattern == "" || r.Op.ID == "" || r.Op.Summary == "" || r.Handler == nil {
			t.Errorf("incomplete route definition: %+v", r)
		}
		if _, exists := patterns[r.Pattern]; exists {
			t.Errorf("duplicate route pattern %q", r.Pattern)
		}
		patterns[r.Pattern] = struct{}{}
		if _, exists := operationIDs[r.Op.ID]; exists {
			t.Errorf("duplicate OpenAPI operation ID %q", r.Op.ID)
		}
		operationIDs[r.Op.ID] = struct{}{}
	}

	spec, err := buildOpenAPI(table)
	if err != nil {
		t.Fatalf("build OpenAPI document: %v", err)
	}
	var document struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(spec, &document); err != nil {
		t.Fatalf("decode generated OpenAPI document: %v", err)
	}
	if document.OpenAPI != "3.0.3" {
		t.Errorf("OpenAPI version = %q, want 3.0.3", document.OpenAPI)
	}
	if len(document.Paths) != len(table) {
		t.Errorf("OpenAPI has %d paths for %d route definitions", len(document.Paths), len(table))
	}
	for _, r := range table {
		path := r.Pattern
		if path == "/{$}" {
			path = "/"
		}
		get, ok := document.Paths[path]["get"]
		if !ok {
			t.Errorf("OpenAPI is missing GET %s", path)
			continue
		}
		var operation struct {
			ID        string                     `json:"operationId"`
			Responses map[string]json.RawMessage `json:"responses"`
		}
		if err := json.Unmarshal(get, &operation); err != nil {
			t.Errorf("decode operation for %s: %v", path, err)
		} else if operation.ID != r.Op.ID {
			t.Errorf("OpenAPI operation for %s = %q, want %q", path, operation.ID, r.Op.ID)
		}
		for _, status := range []string{"200", "405", "499", "500"} {
			if _, ok := operation.Responses[status]; !ok {
				t.Errorf("OpenAPI operation %s is missing response %s", r.Op.ID, status)
			}
		}
		switch r.Op.ID {
		case "getLiveness", "getServiceStatus":
			if _, ok := operation.Responses["503"]; ok {
				t.Errorf("OpenAPI operation %s documents an impossible 503 response", r.Op.ID)
			}
		case "getReadiness":
			if _, ok := operation.Responses["503"]; !ok {
				t.Errorf("OpenAPI operation %s is missing its drain response", r.Op.ID)
			}
		case "getDependencies":
			for _, status := range []string{"429", "502", "503", "504"} {
				if _, ok := operation.Responses[status]; !ok {
					t.Errorf("dependency operation is missing response %s", status)
				}
			}
		}
	}

	var schemas struct {
		Components struct {
			Schemas struct {
				ErrorResponse struct {
					Properties struct {
						Error struct {
							Properties struct {
								Code struct {
									Enum []string `json:"enum"`
								} `json:"code"`
							} `json:"properties"`
						} `json:"error"`
					} `json:"properties"`
				} `json:"ErrorResponse"`
			} `json:"schemas"`
		} `json:"components"`
	}

	if err := json.Unmarshal(spec, &schemas); err != nil {
		t.Fatal(err)
	}
	foundUnavailable := false
	for _, code := range schemas.Components.Schemas.ErrorResponse.Properties.Error.Properties.Code.Enum {
		if code == "UNAVAILABLE" {
			foundUnavailable = true
		}
	}
	if !foundUnavailable {
		t.Error("OpenAPI ErrorResponse enum is missing UNAVAILABLE")
	}
}

func TestMuxServesOpenAPIAndSwaggerRedirect(t *testing.T) {
	table := testRouteTable()
	spec, err := buildOpenAPI(table)
	if err != nil {
		t.Fatal(err)
	}
	mux := newMux(table, spec, true)

	specResponse := httptest.NewRecorder()
	mux.ServeHTTP(specResponse, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if specResponse.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d, want %d", specResponse.Code, http.StatusOK)
	}
	if got := specResponse.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("OpenAPI content type = %q", got)
	}
	if specResponse.Body.Len() == 0 {
		t.Fatal("OpenAPI response is empty")
	}

	docsResponse := httptest.NewRecorder()
	mux.ServeHTTP(docsResponse, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if docsResponse.Code != http.StatusFound {
		t.Errorf("/docs status = %d, want %d", docsResponse.Code, http.StatusFound)
	}
	if got := docsResponse.Header().Get("Location"); got != "/docs/" {
		t.Errorf("/docs redirect location = %q, want /docs/", got)
	}
}

func TestNewBuildsTheRESTHandlerStack(t *testing.T) {
	app := &application.App{
		Market:     market.New(nil),
		Securities: securities.New(nil),
		Trading:    trading.New(nil),
	}
	cfg := config.REST{
		Common:         config.Common{ServiceName: "rest-test", SlowRequestThreshold: time.Second},
		Host:           "127.0.0.1",
		Port:           8000,
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    60 * time.Second,
		RequestTimeout: 25 * time.Second,
		MaxConcurrent:  8,
		DocsEnabled:    true,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := New(cfg, app, logger)
	if err != nil {
		t.Fatalf("create REST server: %v", err)
	}

	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Error("REST middleware did not add a request ID")
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("REST middleware did not add security headers")
	}
}
