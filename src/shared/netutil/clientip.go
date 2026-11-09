// Package netutil holds small network helpers shared by both services.
package netutil

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrustedProxies parses a comma-separated list of CIDRs/IPs.
func ParseTrustedProxies(csv string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(part); err == nil {
			out = append(out, prefix)
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q", part)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// RemoteIP is the IP of the direct peer.
func RemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ClientIP returns the client address. X-Forwarded-For is honoured only when the direct peer is a trusted proxy, and then the right-most untrusted hop is used.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	remote := RemoteIP(r)
	peer, err := netip.ParseAddr(remote)
	if err != nil || !contains(trusted, peer) {
		return remote
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			continue
		}
		if !contains(trusted, hop) {
			return hop.Unmap().String()
		}
	}
	return remote
}

// contains reports whether addr is inside any prefix. An IPv4-mapped IPv6 address (::ffff:10.0.0.1) is treated as the IPv4 address it carries, so an IPv4 prefix matches it however a dual-stack listener or proxy spelled it.
func contains(prefixes []netip.Prefix, addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
