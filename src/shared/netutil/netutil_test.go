package netutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies(" 10.0.0.0/8 , 192.168.1.5,fd00::/8, ,::1 ")
	if err != nil || len(got) != 4 {
		t.Fatalf("%v %v", got, err)
	}
	if got[1].String() != "192.168.1.5/32" {
		t.Fatalf("a bare IP must become a /32, got %s", got[1])
	}
	if empty, err := ParseTrustedProxies(""); err != nil || len(empty) != 0 {
		t.Fatalf("%v %v", empty, err)
	}
	for _, bad := range []string{"nope", "10.0.0.0/33", "1.2.3", "10.0.0.0/8,x"} {
		if _, err := ParseTrustedProxies(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestClientIPAndRemoteIP(t *testing.T) {
	trusted, _ := ParseTrustedProxies("10.0.0.0/8,fd00::/8")
	mk := func(remote, xff string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.9:1", "1.2.3.4", "203.0.113.9"},                       // untrusted peer: header ignored
		{"10.1.1.1:1", "", "10.1.1.1"},                                    // trusted peer, no header
		{"10.1.1.1:1", "198.51.100.7", "198.51.100.7"},                    // single hop
		{"10.1.1.1:1", "6.6.6.6, 198.51.100.7, 10.2.2.2", "198.51.100.7"}, // right-most untrusted hop
		{"10.1.1.1:1", "10.2.2.2, 10.3.3.3", "10.1.1.1"},                  // every hop trusted -> the peer
		{"10.1.1.1:1", "garbage", "10.1.1.1"},
		{"[fd00::1]:5", "2001:db8::7", "2001:db8::7"},
		{"nonsense", "", "nonsense"}, // unparsable RemoteAddr falls back to itself
	}
	for _, c := range cases {
		if got := ClientIP(mk(c.remote, c.xff), trusted); got != c.want {
			t.Errorf("remote=%s xff=%q -> %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
	if got := ClientIP(mk("10.1.1.1:1", "1.2.3.4"), nil); got != "10.1.1.1" {
		t.Fatalf("no trusted proxies must ignore XFF: %s", got)
	}
	if got := RemoteIP(mk("[::1]:80", "")); got != "::1" {
		t.Fatal(got)
	}
}
