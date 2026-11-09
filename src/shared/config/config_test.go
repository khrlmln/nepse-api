package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsAreValidAndSafe(t *testing.T) {
	r, err := loadREST(env(nil))
	if err != nil || r.Host != "127.0.0.1" || r.Port != 8000 || r.RequestTimeout >= r.WriteTimeout || !r.DocsEnabled {
		t.Fatalf("%+v %v", r, err)
	}
	m, err := loadMCP(env(nil))
	if err != nil || m.Host != "127.0.0.1" || m.Port != 9000 {
		t.Fatalf("%+v %v", m, err)
	}
	if r.NEPSE.MaxFloorPages != 100 || r.NEPSE.MaxFloorRows != 50000 {
		t.Fatalf("%+v", r.NEPSE)
	}
}

func TestInvalidValuesAreAllReported(t *testing.T) {
	_, err := loadREST(env(map[string]string{
		"REST_PORT": "70000", "REST_WRITE_TIMEOUT": "abc", "NEPSE_BASE_URL": "http://www.nepalstock.com",
		"REST_ALLOWED_ORIGINS": "*", "LOG_LEVEL": "loud", "NEPSE_MAX_FLOOR_PAGES": "0", "REST_RATE_LIMIT_PER_MINUTE": "-1",
		"TRUSTED_PROXIES": "not-an-ip", "REST_REQUEST_TIMEOUT": "40s",
	}))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"REST_PORT", "REST_WRITE_TIMEOUT", "NEPSE_BASE_URL", "REST_ALLOWED_ORIGINS", "LOG_LEVEL",
		"NEPSE_MAX_FLOOR_PAGES", "REST_RATE_LIMIT_PER_MINUTE", "TRUSTED_PROXIES"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s:\n%v", want, err)
		}
	}
}

func TestMCPDoesNotRequireCallerAuthentication(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "0.0.0.0", "::"} {
		m, err := loadMCP(env(map[string]string{"MCP_HOST": host}))
		if err != nil {
			t.Errorf("MCP_HOST=%q must not require an application token: %v", host, err)
		}
		if m.Host != host {
			t.Errorf("MCP_HOST=%q was changed to %q", host, m.Host)
		}
	}
}

func TestPlainHTTPUpstreamNeedsExplicitOptIn(t *testing.T) {
	if _, err := loadREST(env(map[string]string{"NEPSE_BASE_URL": "http://127.0.0.1:1"})); err == nil {
		t.Fatal("http upstream must be rejected by default")
	}
	if _, err := loadREST(env(map[string]string{"NEPSE_BASE_URL": "http://127.0.0.1:1", "NEPSE_ALLOW_INSECURE_HTTP": "true"})); err != nil {
		t.Fatal(err)
	}
}

func TestMCPConcurrencyAndBaseURLRules(t *testing.T) {
	m, err := loadMCP(env(nil))
	if err != nil || m.MaxConcurrent != 32 {
		t.Fatalf("%+v %v", m, err)
	}
	for _, bad := range []string{"0", "-1", "abc", "100001"} {
		if _, err := loadMCP(env(map[string]string{"MCP_MAX_CONCURRENT": bad})); err == nil {
			t.Errorf("MCP_MAX_CONCURRENT=%s accepted", bad)
		}
	}
	for _, u := range []string{"https://www.nepalstock.com/?x=1", "https://www.nepalstock.com/#f", "https://www.nepalstock.com?", "https://www.nepalstock.com/path"} {
		if _, err := loadREST(env(map[string]string{"NEPSE_BASE_URL": u})); err == nil {
			t.Errorf("NEPSE_BASE_URL=%s accepted", u)
		}
	}
}

func TestBaseURLErrorNeverEchoesCredentials(t *testing.T) {
	_, err := loadREST(env(map[string]string{"NEPSE_BASE_URL": "https://admin:s3cretPASS@www.nepalstock.com/?q=1"}))
	if err == nil {
		t.Fatal("credentials in the base URL accepted")
	}
	if strings.Contains(err.Error(), "s3cretPASS") || strings.Contains(err.Error(), "admin") {
		t.Fatalf("credentials echoed: %v", err)
	}
	if !strings.Contains(err.Error(), "https://host") {
		t.Fatalf("error does not show the required form: %v", err)
	}
}

func TestEveryLoaderBranchRejectsBadInput(t *testing.T) {
	bad := map[string]string{
		"NEPSE_INSECURE_SKIP_VERIFY": "maybe", "NEPSE_ALLOW_INSECURE_HTTP": "2", "REST_DOCS_ENABLED": "yes please",
		"NEPSE_MAX_RESPONSE_BYTES": "-5", "NEPSE_TIMEOUT": "0s", "NEPSE_CACHE_TTL": "-1s", "NEPSE_MAX_FLOOR_ROWS": "10",
		"REST_HOST": "bad host", "LOG_FORMAT": "yaml", "SHUTDOWN_TIMEOUT": "soon", "SHUTDOWN_DRAIN_DELAY": "-3s",
		"LOG_SLOW_REQUEST_THRESHOLD": "0", "NEPSE_CA_BUNDLE": "/definitely/not/here.pem", "REST_MAX_CONCURRENT": "0",
	}
	for key, value := range bad {
		if _, err := loadREST(env(map[string]string{key: value})); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%q: expected an error naming the variable, got %v", key, value, err)
		}
	}
	for _, good := range []map[string]string{
		{"NEPSE_CACHE_TTL": "0s"}, {"SHUTDOWN_DRAIN_DELAY": "0s"}, {"SHUTDOWN_DRAIN_DELAY": "5s"}, {"REST_DOCS_ENABLED": "false"},
		{"REST_ALLOWED_ORIGINS": "https://a.example.com, http://localhost:3000"}, {"LOG_LEVEL": "DEBUG", "LOG_FORMAT": "TEXT"},
	} {
		if _, err := loadREST(env(good)); err != nil {
			t.Errorf("%v rejected: %v", good, err)
		}
	}
}

func TestListenerHostValidationRejectsPortsAndMalformedNames(t *testing.T) {
	for _, host := range []string{"localhost:8000", "127.0.0.1:8000", "https://localhost", "[::1]", "bad host", "bad/name", "bad..name", "-bad.example", "bad-.example", "name_with_underscore"} {
		if _, err := loadREST(env(map[string]string{"REST_HOST": host})); err == nil {
			t.Errorf("REST_HOST=%q accepted; host and port must be separate", host)
		}
	}
	for _, host := range []string{"localhost", "localhost.", "api.internal", "127.0.0.1", "::1", "fe80::1%lo0"} {
		if _, err := loadMCP(env(map[string]string{"MCP_HOST": host})); err != nil {
			t.Errorf("MCP_HOST=%q rejected: %v", host, err)
		}
	}
}

func TestOriginRules(t *testing.T) {
	for _, bad := range []string{"*", "example.com", "https://example.com/path", "https://example.com?x=1", "https://example.com#section", "https://user@example.com", "ftp://example.com", "javascript://example.com", "https://", "://x"} {
		if _, err := loadREST(env(map[string]string{"REST_ALLOWED_ORIGINS": bad})); err == nil {
			t.Errorf("origin %q accepted", bad)
		}
	}
}

func TestAddrAndDerivedTimeouts(t *testing.T) {
	r, err := loadREST(env(map[string]string{"REST_HOST": "0.0.0.0", "REST_PORT": "8081", "REST_WRITE_TIMEOUT": "20s"}))
	if err != nil || r.Addr() != "0.0.0.0:8081" || r.RequestTimeout != 15*time.Second {
		t.Fatalf("%v %s %v", err, r.Addr(), r.RequestTimeout)
	}
	small, err := loadREST(env(map[string]string{"REST_WRITE_TIMEOUT": "2s"}))
	if err != nil || small.RequestTimeout != time.Second {
		t.Fatalf("tiny write timeouts keep a 1s floor: %v %v", small.RequestTimeout, err)
	}
	m, _ := loadMCP(env(map[string]string{"MCP_HOST": "::1", "MCP_PORT": "9100"}))
	if m.Addr() != "[::1]:9100" {
		t.Fatalf("IPv6 addresses must be bracketed: %s", m.Addr())
	}
	if _, err := loadREST(env(map[string]string{"REST_WRITE_TIMEOUT": "10s", "REST_REQUEST_TIMEOUT": "10s"})); err == nil {
		t.Fatal("request timeout must be shorter than the write timeout")
	}
}
