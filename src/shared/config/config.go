// Package config loads typed, validated configuration from the environment. Each service has its own loader; invalid or unsafe settings fail startup with one error listing every problem.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata" // keep Asia/Kathmandu available in standalone builds

	"nepse-api/src/shared/nepse"
	"nepse-api/src/shared/netutil"
)

// Common holds settings shared by both services.
type Common struct {
	ServiceName          string
	LogLevel             string
	LogFormat            string
	SlowRequestThreshold time.Duration
	ShutdownTimeout      time.Duration
	DrainDelay           time.Duration
	TrustedProxies       []netip.Prefix
	NEPSE                nepse.Config
}

// REST holds the REST service settings.
type REST struct {
	Common
	Host               string
	Port               int
	AllowedOrigins     []string
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	RequestTimeout     time.Duration
	MaxConcurrent      int
	RateLimitPerMinute int
	DocsEnabled        bool
}

// MCP holds the MCP service settings.
type MCP struct {
	Common
	Host               string
	Port               int
	AllowedOrigins     []string
	RateLimitPerMinute int
	ToolTimeout        time.Duration
	MaxConcurrent      int
}

func (c REST) Addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }
func (c MCP) Addr() string  { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// loader reads variables and accumulates every validation error.
type loader struct {
	errs []error
	get  func(string) string
}

func (l *loader) fail(key, format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf("%s: %s", key, fmt.Sprintf(format, args...)))
}

func (l *loader) str(key, def string) string {
	if v := strings.TrimSpace(l.get(key)); v != "" {
		return v
	}
	return def
}

func (l *loader) boolean(key string, def bool) bool {
	v := strings.TrimSpace(l.get(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.fail(key, "%q is not a boolean", v)
		return def
	}
	return b
}

func (l *loader) integer(key string, def, lo, hi int) int {
	v := strings.TrimSpace(l.get(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.fail(key, "%q is not an integer", v)
		return def
	}
	if n < lo || n > hi {
		l.fail(key, "%d is outside the allowed range %d..%d", n, lo, hi)
		return def
	}
	return n
}

func (l *loader) int64(key string, def, lo int64) int64 {
	v := strings.TrimSpace(l.get(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < lo {
		l.fail(key, "%q must be an integer >= %d", v, lo)
		return def
	}
	return n
}

func (l *loader) duration(key string, def time.Duration, allowZero bool) time.Duration {
	v := strings.TrimSpace(l.get(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.fail(key, "%q is not a valid duration (e.g. 5s, 1m)", v)
		return def
	}
	if d < 0 || (d == 0 && !allowZero) {
		l.fail(key, "%q must be positive", v)
		return def
	}
	return d
}

func (l *loader) list(key string) []string {
	var out []string
	for _, part := range strings.Split(l.get(key), ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (l *loader) host(key, def string) string {
	h := l.str(key, def)
	if !validListenerHost(h) {
		l.fail(key, "%q is not a valid host name or IP address (do not include a port)", h)
		return def
	}
	return h
}

// validListenerHost accepts an IP literal or an ASCII DNS hostname. Listener
// ports are configured separately, so host:port values and URL syntax are invalid.
func validListenerHost(host string) bool {
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if strings.ContainsAny(host, ":/\\[]% 	\r\n") || len(host) > 254 {
		return false
	}
	name := strings.TrimSuffix(host, ".")
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func (l *loader) origins(key string) []string {
	out := l.list(key)
	for _, o := range out {
		if o == "*" {
			l.fail(key, "a wildcard origin is not allowed; list explicit origins")
			continue
		}
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
			l.fail(key, "%q is not an origin like https://app.example.com", o)
		}
	}
	return out
}

func (l *loader) common(defaultService string) Common {
	c := Common{
		ServiceName:          l.str("SERVICE_NAME", defaultService),
		LogLevel:             strings.ToLower(l.str("LOG_LEVEL", "info")),
		LogFormat:            strings.ToLower(l.str("LOG_FORMAT", "json")),
		SlowRequestThreshold: l.duration("LOG_SLOW_REQUEST_THRESHOLD", time.Second, false),
		ShutdownTimeout:      l.duration("SHUTDOWN_TIMEOUT", 15*time.Second, false),
		DrainDelay:           l.duration("SHUTDOWN_DRAIN_DELAY", 0, true),
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		l.fail("LOG_LEVEL", "%q must be debug, info, warn or error", c.LogLevel)
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		l.fail("LOG_FORMAT", "%q must be json or text", c.LogFormat)
	}
	proxies, err := netutil.ParseTrustedProxies(l.get("TRUSTED_PROXIES"))
	if err != nil {
		l.fail("TRUSTED_PROXIES", "%v", err)
	}
	c.TrustedProxies = proxies

	n := nepse.Config{
		BaseURL:            l.str("NEPSE_BASE_URL", "https://www.nepalstock.com"),
		Timeout:            l.duration("NEPSE_TIMEOUT", nepse.DefaultTimeout, false),
		MaxResponseBytes:   l.int64("NEPSE_MAX_RESPONSE_BYTES", nepse.DefaultMaxResponseBytes, 1),
		MaxFloorPages:      l.integer("NEPSE_MAX_FLOOR_PAGES", nepse.DefaultMaxFloorPages, 1, 10000),
		MaxFloorRows:       l.integer("NEPSE_MAX_FLOOR_ROWS", nepse.DefaultMaxFloorRows, nepse.FloorSheetPageSize, 5000000),
		CacheTTL:           l.duration("NEPSE_CACHE_TTL", 5*time.Second, true),
		MaxStale:           l.duration("NEPSE_MAX_STALE", nepse.DefaultMaxStale, true),
		CABundlePath:       l.str("NEPSE_CA_BUNDLE", ""),
		InsecureSkipVerify: l.boolean("NEPSE_INSECURE_SKIP_VERIFY", false),
		AllowInsecureHTTP:  l.boolean("NEPSE_ALLOW_INSECURE_HTTP", false),
		UserAgent:          l.str("NEPSE_USER_AGENT", ""),
	}
	if err := nepse.ValidateBaseURL(n.BaseURL, n.AllowInsecureHTTP); err != nil {
		l.fail("NEPSE_BASE_URL", "%v", err)
	}
	if n.CABundlePath != "" {
		if _, err := os.Stat(n.CABundlePath); err != nil {
			l.fail("NEPSE_CA_BUNDLE", "cannot read %q", n.CABundlePath)
		}
	}
	c.NEPSE = n
	return c
}

func port(l *loader, key string, def int) int { return l.integer(key, def, 1, 65535) }

// LoadREST loads the REST service configuration from the process environment.
func LoadREST() (REST, error) { return loadREST(os.Getenv) }

func loadREST(get func(string) string) (REST, error) {
	l := &loader{get: get}
	c := REST{
		Common:             l.common("nepse-rest"),
		Host:               l.host("REST_HOST", "127.0.0.1"),
		Port:               port(l, "REST_PORT", 8000),
		AllowedOrigins:     l.origins("REST_ALLOWED_ORIGINS"),
		ReadTimeout:        l.duration("REST_READ_TIMEOUT", 5*time.Second, false),
		WriteTimeout:       l.duration("REST_WRITE_TIMEOUT", 30*time.Second, false),
		IdleTimeout:        l.duration("REST_IDLE_TIMEOUT", 60*time.Second, false),
		MaxConcurrent:      l.integer("REST_MAX_CONCURRENT", 64, 1, 100000),
		RateLimitPerMinute: l.integer("REST_RATE_LIMIT_PER_MINUTE", 120, 0, 1000000),
		DocsEnabled:        l.boolean("REST_DOCS_ENABLED", true),
	}
	c.RequestTimeout = l.duration("REST_REQUEST_TIMEOUT", max(c.WriteTimeout-5*time.Second, time.Second), false)
	if c.RequestTimeout >= c.WriteTimeout {
		l.fail("REST_REQUEST_TIMEOUT", "must be shorter than REST_WRITE_TIMEOUT (%s) so errors can still be written", c.WriteTimeout)
	}
	return c, errors.Join(l.errs...)
}

// LoadMCP loads the MCP service configuration from the process environment.
func LoadMCP() (MCP, error) { return loadMCP(os.Getenv) }

func loadMCP(get func(string) string) (MCP, error) {
	l := &loader{get: get}
	c := MCP{
		Common:             l.common("nepse-mcp"),
		Host:               l.host("MCP_HOST", "127.0.0.1"),
		Port:               port(l, "MCP_PORT", 9000),
		AllowedOrigins:     l.origins("MCP_ALLOWED_ORIGINS"),
		RateLimitPerMinute: l.integer("MCP_RATE_LIMIT_PER_MINUTE", 120, 0, 1000000),
		ToolTimeout:        l.duration("MCP_TOOL_TIMEOUT", 30*time.Second, false),
		MaxConcurrent:      l.integer("MCP_MAX_CONCURRENT", 32, 1, 100000),
	}
	return c, errors.Join(l.errs...)
}
