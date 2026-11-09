package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nepse-api/src/rest/middleware"
	apierrors "nepse-api/src/shared/errors"
)

func TestNoInputWritesTheSharedResponseEnvelope(t *testing.T) {
	handler := middleware.Handle("/summary", noInput(func(context.Context) (map[string]string, error) {
		return map[string]string{"market": "open"}, nil
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/summary", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"data":{"market":"open"}`) {
		t.Fatalf("unexpected response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestByParamPassesPathValueAndMapsUseCaseErrors(t *testing.T) {
	var received string
	handler := middleware.Handle("/securities/{symbol}", byParam("symbol", func(_ context.Context, value string) (string, error) {
		received = value
		if value == "MISSING" {
			return "", apierrors.NotFound("symbol not found")
		}
		return value, nil
	}))

	request := httptest.NewRequest(http.MethodGet, "/securities/NABIL", nil)
	request.SetPathValue("symbol", "NABIL")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if received != "NABIL" || response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"data":"NABIL"`) {
		t.Fatalf("path value was not forwarded: received=%q status=%d body=%s", received, response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/securities/MISSING", nil)
	request.SetPathValue("symbol", "MISSING")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("use-case error was not mapped: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestIntParamHandlesMissingValidAndInvalidValues(t *testing.T) {
	for _, test := range []struct {
		name    string
		query   string
		want    int
		wantErr bool
	}{
		{name: "missing defaults to zero"},
		{name: "valid integer", query: "?page=%2042%20", want: 42},
		{name: "invalid integer", query: "?page=abc", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/securities"+test.query, nil)
			got, err := intParam(request, "page")
			if test.wantErr {
				if apierrors.Normalize(err).Code != apierrors.CodeInvalidArgument {
					t.Fatalf("error = %v, want INVALID_ARGUMENT", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("intParam = %d, %v; want %d, nil", got, err, test.want)
			}
		})
	}
}
