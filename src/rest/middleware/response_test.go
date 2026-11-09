package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "nepse-api/src/shared/errors"
)

func TestHandleAllowsOnlyReadMethods(t *testing.T) {
	handler := Handle("/resource", func(w http.ResponseWriter, r *http.Request) error {
		OK(w, r, map[string]string{"value": "ok"}, nil)
		return nil
	})

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/resource", nil))
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d, want %d", method, response.Code, http.StatusOK)
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/resource", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if response.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("Allow header = %q", response.Header().Get("Allow"))
	}
	if !strings.Contains(response.Body.String(), `"code":"METHOD_NOT_ALLOWED"`) {
		t.Errorf("method error code missing: %s", response.Body.String())
	}
}

func TestHandleMapsErrorsAndOKWritesEnvelope(t *testing.T) {
	handler := Handle("/resource", func(http.ResponseWriter, *http.Request) error {
		return apierrors.NotFound("missing")
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("unexpected mapped error: status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	OK(response, httptest.NewRequest(http.MethodGet, "/resource", nil), []string{"NABIL"}, map[string]int{"total": 1})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"data":["NABIL"]`) || !strings.Contains(response.Body.String(), `"total":1`) {
		t.Fatalf("unexpected success envelope: status=%d body=%s", response.Code, response.Body.String())
	}

	failed := Handle("/resource", func(http.ResponseWriter, *http.Request) error {
		return apierrors.Internal(errors.New("private cause"))
	})
	response = httptest.NewRecorder()
	failed.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/resource", nil))
	if strings.Contains(response.Body.String(), "private cause") {
		t.Error("internal error leaked its cause to the client")
	}
}
