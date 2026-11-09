package netutil

import (
	"net/http/httptest"
	"testing"
)

func TestIPv4MappedIPv6AddressesMatchIPv4Prefixes(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[::ffff:10.0.0.7]:5555"
	r.Header.Set("X-Forwarded-For", "198.51.100.4, ::ffff:10.0.0.3")
	if got := ClientIP(r, trusted); got != "198.51.100.4" {
		t.Fatalf("ClientIP = %q, want 198.51.100.4 (mapped proxy addresses are trusted)", got)
	}

	r.Header.Set("X-Forwarded-For", "::ffff:203.0.113.9")
	if got := ClientIP(r, trusted); got != "203.0.113.9" {
		t.Fatalf("ClientIP = %q, want the unmapped 203.0.113.9", got)
	}
}
