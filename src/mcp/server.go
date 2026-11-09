// Package mcpserver is the MCP transport: it exposes shared use cases as Model Context Protocol tools over stateless Streamable HTTP, with optional origin restrictions, rate and concurrency limits, and request logging.
package mcpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"nepse-api/src/mcp/tools"
	"nepse-api/src/shared/application"
	"nepse-api/src/shared/config"
	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/httpmw"
	"nepse-api/src/shared/version"
)

const (
	maxRequestBytes = 1 << 20 // MCP requests are small JSON-RPC messages
	instructions    = "Read-only market data from the Nepal Stock Exchange (NEPSE) via an unofficial integration. " +
		"Check get_market_status first: live tables and market depth are meaningful only while the market is open " +
		"(Sunday-Thursday, about 11:00-15:00 Nepal time). Use list_securities or validate_symbol to resolve symbols. " +
		"For technical analysis use get_price_history (daily OHLC-style rows) and the index graphs. " +
		"List results are paginated; floorsheet pages hold 500 trades. Data is unofficial and not investment advice."
)

// Server is the MCP HTTP server.
type Server struct {
	cfg     config.MCP
	log     *slog.Logger
	handler http.Handler
	http    *http.Server
	drain   *httpmw.Drain
}

// New builds the MCP server, registers the tools and assembles the HTTP stack.
func New(cfg config.MCP, app *application.App, log *slog.Logger) (*Server, error) {
	drain := &httpmw.Drain{}
	handler := withMiddleware(newMux(cfg, app, drain, newSDKHandler(cfg, app, log)), cfg, log)
	return &Server{
		cfg: cfg, log: log, handler: handler, drain: drain,
		http: &http.Server{
			Addr:              cfg.Addr(),
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      cfg.ToolTimeout + 10*time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    16 << 10,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		},
	}, nil
}

// Handler exposes the full handler (stack + routes) for embedding or tests.
func (s *Server) Handler() http.Handler { return s.handler }

// newSDKHandler builds the MCP protocol handler (stateless, JSON responses).
func newSDKHandler(cfg config.MCP, app *application.App, log *slog.Logger) http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: cfg.ServiceName, Title: "NEPSE market data", Version: version.Version},
		&mcp.ServerOptions{Instructions: instructions})
	tools.Register(srv, tools.Deps{App: app, Log: log, Timeout: cfg.ToolTimeout})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless:           true,
		JSONResponse:        true,
		MaxRequestBodyBytes: maxRequestBytes,
		Logger:              log,
	})
}

// newMux routes /mcp and the local health probes. Caller authentication is intentionally not part of this internal service; network access is restricted by the loopback listener and deployment reverse proxy/firewall.
func newMux(cfg config.MCP, app *application.App, drain *httpmw.Drain, sdk http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	// /mcp requests beyond the configured concurrency limit are rejected, not queued.
	limited := httpmw.Chain(sdk, httpmw.ConcurrencyLimit(cfg.MaxConcurrent, nil, httpmw.WriteError))
	mux.Handle("/mcp", httpmw.Route("/mcp", limited))

	// live and ready are local-only: an upstream outage must not restart or unready the process.
	health := httpmw.Health{Ping: app.Client.Ping, Drain: drain}
	for route, h := range map[string]func(http.ResponseWriter, *http.Request) error{
		"/health/live": health.Live(), "/health/ready": health.Ready(), "/health/dependencies": health.Dependencies(),
	} {
		mux.Handle(route, httpmw.Route(route, httpmw.HandleErr(h)))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpmw.WriteError(w, r, apierrors.NotFound("The requested resource does not exist."))
	})
	return mux
}

// withMiddleware wraps the mux, outermost first: IDs, logging, panic recovery, CORS/origin policy, rate limit.
func withMiddleware(mux http.Handler, cfg config.MCP, log *slog.Logger) http.Handler {
	limiter := httpmw.NewRateLimiter(cfg.RateLimitPerMinute)
	return httpmw.Chain(mux,
		httpmw.RequestID(),
		httpmw.AccessLog(log, cfg.TrustedProxies, cfg.SlowRequestThreshold, httpmw.LocalProbes),
		httpmw.Recover(log, httpmw.WriteError),
		cors(cfg.AllowedOrigins),
		httpmw.RateLimit(limiter, cfg.TrustedProxies, httpmw.LocalProbes, httpmw.WriteError),
	)
}

// cors admits browser clients from the allow-listed origins only. Requests with no Origin header (desktop apps, CLIs, servers) are unaffected; any other origin is rejected outright, because this unauthenticated service must never be driven from a foreign web page. Preflight (OPTIONS) requests from allowed origins are answered before the MCP handler and carry no application data.
func cors(allowed []string) httpmw.Middleware {
	return httpmw.CORS(httpmw.CORSPolicy{
		Origins:        allowed,
		Methods:        []string{"GET", "POST", "DELETE", "OPTIONS"},
		Headers:        []string{"Content-Type", "Accept", "Mcp-Protocol-Version", "Mcp-Session-Id", "Last-Event-ID", "X-Request-ID"},
		Expose:         []string{"Mcp-Session-Id", "Retry-After", "X-Request-ID"},
		RejectUnlisted: true,
	})
}

// Run serves until ctx is canceled, then drains and shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	return httpmw.Serve(ctx, s.log, "mcp server", s.http, httpmw.ServeOptions{
		ShutdownTimeout: s.cfg.ShutdownTimeout, DrainDelay: s.cfg.DrainDelay, Drain: s.drain,
	})
}
