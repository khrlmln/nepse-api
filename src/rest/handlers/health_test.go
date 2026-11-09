package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nepse-api/src/rest/middleware"
	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/httpmw"
)

func TestHealthProbesSeparateProcessReadinessFromNEPSE(t *testing.T) {
	health := Health{
		Drain: &httpmw.Drain{},
		Ping: func(context.Context) error {
			t.Fatal("liveness/readiness must not contact NEPSE")
			return nil
		},
	}

	for _, test := range []struct {
		name    string
		handler middleware.HandlerFunc
	}{
		{name: "live", handler: health.Live()},
		{name: "ready", handler: health.Ready()},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			middleware.Handle("/health/"+test.name, test.handler).ServeHTTP(
				response, httptest.NewRequest(http.MethodGet, "/health/"+test.name, nil),
			)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"status":"ok"`) {
				t.Errorf("unexpected health response: %s", response.Body.String())
			}
		})
	}
}

func TestDependencyProbeUsesBoundedContextAndReportsUpstreamStatus(t *testing.T) {
	called := false
	health := Health{
		Drain: &httpmw.Drain{},
		Ping: func(ctx context.Context) error {
			called = true
			if _, ok := ctx.Deadline(); !ok {
				t.Error("dependency check context has no deadline")
			}
			return nil
		},
	}
	response := httptest.NewRecorder()
	middleware.Handle("/health/dependencies", health.Dependencies()).ServeHTTP(
		response, httptest.NewRequest(http.MethodGet, "/health/dependencies", nil),
	)
	if !called {
		t.Fatal("dependency probe did not call Ping")
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"nepse":"ok"`) {
		t.Fatalf("unexpected dependency response: status=%d body=%s", response.Code, response.Body.String())
	}

	health.Ping = func(context.Context) error {
		return apierrors.UpstreamUnavailable(errors.New("upstream unavailable"))
	}
	response = httptest.NewRecorder()
	middleware.Handle("/health/dependencies", health.Dependencies()).ServeHTTP(
		response, httptest.NewRequest(http.MethodGet, "/health/dependencies", nil),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("failed dependency status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestRootIncludesDocumentationLinksOnlyWhenEnabled(t *testing.T) {
	for _, test := range []struct {
		name        string
		docsEnabled bool
		wantDocs    bool
	}{
		{name: "enabled", docsEnabled: true, wantDocs: true},
		{name: "disabled", docsEnabled: false, wantDocs: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			Root{Service: "rest-test", DocsEnabled: test.docsEnabled}.Handle()(
				response, httptest.NewRequest(http.MethodGet, "/", nil),
			)
			body := response.Body.String()
			if response.Code != http.StatusOK || !strings.Contains(body, `"service":"rest-test"`) {
				t.Fatalf("unexpected root response: status=%d body=%s", response.Code, body)
			}
			if strings.Contains(body, `"docs"`) != test.wantDocs {
				t.Errorf("docs link presence = %v, want %v", strings.Contains(body, `"docs"`), test.wantDocs)
			}
		})
	}
}

func TestSecuritiesHandlerRejectsMalformedPaginationBeforeCallingService(t *testing.T) {
	response := httptest.NewRecorder()
	handler := middleware.Handle("/api/v1/securities", (Securities{}).List())
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/securities?page=not-a-number", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestTradingHandlerRejectsMalformedPageBeforeCallingService(t *testing.T) {
	response := httptest.NewRecorder()
	handler := middleware.Handle("/api/v1/trading/floorsheet", (Trading{}).Floorsheet())
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/trading/floorsheet?page=bad", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}
