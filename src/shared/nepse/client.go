// Package nepse is the shared upstream client: NEPSE authentication (css.wasm token transformation), protected payload construction, bounded HTTP access and typed accessors used by the market, securities and trading use cases. Files: client.go (construction), transport.go (HTTP, retries, error mapping), auth.go (token lifecycle), payload.go (protected payload ids), data.go (generic and per-security accessors), securities.go (security list and symbol lookup), floorsheet.go, cache.go, endpoints.go, shape.go, wasm.go.
package nepse

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"nepse-api/src/shared/nepse/assets"
)

// Defaults applied for zero Config values; the config package reuses them so the numbers live in exactly one place.
const (
	DefaultTimeout          = 30 * time.Second
	DefaultMaxResponseBytes = 8 << 20
	DefaultMaxFloorPages    = 100
	DefaultMaxFloorRows     = 50000
	// DefaultMaxStale is how long past its TTL a cached response may still be served when NEPSE cannot be reached; 0 disables serving stale data.
	DefaultMaxStale = time.Minute
)

// Config configures the client. Zero values are replaced by safe defaults.
type Config struct {
	BaseURL            string
	Timeout            time.Duration
	MaxResponseBytes   int64
	MaxFloorPages      int
	MaxFloorRows       int
	CacheTTL           time.Duration
	MaxStale           time.Duration // stale-on-error window past the TTL (see DefaultMaxStale); 0 = never serve stale
	CABundlePath       string
	InsecureSkipVerify bool // testing only
	AllowInsecureHTTP  bool // testing only (plain-HTTP fake upstream)
	UserAgent          string
	Location           *time.Location
	Logger             *slog.Logger
}

// Client talks to NEPSE. It is safe for concurrent use.
type Client struct {
	cfg     Config
	http    *http.Client
	assets  assetsData
	referer string
	token   *tokenManager
	cache   *responseCache

	payloadSpecMu    sync.Mutex
	payloadSeedIndex int
	payloadSpecKey   string
	payloadSpecReady bool

	securityMu   sync.Mutex
	securityIDs  map[string]int
	securityMiss map[string]time.Time
}

// ValidateBaseURL checks the NEPSE base URL. Only scheme://host[:port] is valid: no credentials, path, query or fragment, because request URLs are built by appending a path to it. Plain http needs allowHTTP (testing only). Errors never echo the value, which could contain credentials.
func ValidateBaseURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "?#") {
		return errors.New("must have the form https://host[:port] (no credentials, path, query or fragment)")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && allowHTTP) {
		return errors.New("must use https (plain http is for testing only and needs NEPSE_ALLOW_INSECURE_HTTP=true)")
	}
	return nil
}

func (cfg Config) withDefaults() Config {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if cfg.MaxFloorPages <= 0 {
		cfg.MaxFloorPages = DefaultMaxFloorPages
	}
	if cfg.MaxFloorRows <= 0 {
		cfg.MaxFloorRows = DefaultMaxFloorRows
	}
	if cfg.CacheTTL < 0 {
		cfg.CacheTTL = 0
	}
	if cfg.Location == nil {
		cfg.Location = nepalLocation()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return cfg
}

// NewClient validates cfg, loads the embedded assets and compiles css.wasm.
func NewClient(cfg Config) (*Client, error) {
	if err := ValidateBaseURL(cfg.BaseURL, cfg.AllowInsecureHTTP); err != nil {
		return nil, fmt.Errorf("NEPSE base URL: %w", err)
	}
	cfg = cfg.withDefaults()
	base, _ := url.Parse(cfg.BaseURL) // validated above

	data, err := loadAssets()
	if err != nil {
		return nil, err
	}
	transport, err := newTransport(cfg, base.Hostname())
	if err != nil {
		return nil, err
	}
	parser, err := newWASMTokenParser(context.Background(), assets.CSSWASM())
	if err != nil {
		return nil, err
	}
	c := &Client{
		cfg:          cfg,
		http:         &http.Client{Transport: transport, Timeout: cfg.Timeout},
		assets:       data,
		referer:      base.Host,
		cache:        newResponseCache(),
		securityIDs:  make(map[string]int),
		securityMiss: make(map[string]time.Time),
	}
	c.cache.maxStale = cfg.MaxStale
	c.token = &tokenManager{parser: parser, client: c}
	return c, nil
}

// newTransport builds the TLS-verifying HTTP transport (TLS 1.2+, optional extra CA bundle).
func newTransport(cfg Config, serverName string) (*http.Transport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("unexpected default HTTP transport type %T", http.DefaultTransport)
	}
	tr := base.Clone()
	var roots *x509.CertPool
	if cfg.CABundlePath != "" {
		pem, err := os.ReadFile(cfg.CABundlePath)
		if err != nil {
			return nil, fmt.Errorf("read NEPSE CA bundle: %w", err)
		}
		if roots, err = x509.SystemCertPool(); err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("NEPSE CA bundle contains no certificates")
		}
	}
	tr.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.InsecureSkipVerify, // #nosec G402 -- explicit, operator-controlled, testing-only switch
		ServerName:         serverName,
		RootCAs:            roots,
	}
	tr.ForceAttemptHTTP2 = true
	return tr, nil
}

func nepalLocation() *time.Location {
	if loc, err := time.LoadLocation("Asia/Kathmandu"); err == nil {
		return loc
	}
	return time.FixedZone("Asia/Kathmandu", 5*60*60+45*60)
}

// Close releases the WASM runtime and idle connections.
func (c *Client) Close(ctx context.Context) error {
	c.http.CloseIdleConnections()
	return c.token.parser.Close(ctx)
}

// Today is the current time in the exchange's time zone.
func (c *Client) Today() time.Time { return time.Now().In(c.cfg.Location) }

func (c *Client) log() *slog.Logger { return c.cfg.Logger }

func (c *Client) endpoint(ep Endpoint) string { return c.assets.endpoints[string(ep)] }
