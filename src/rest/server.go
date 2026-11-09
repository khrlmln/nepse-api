// Package rest is the REST transport: routing, middleware, OpenAPI/Swagger UI and the HTTP server lifecycle.
package rest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/swaggest/swgui/v5emb"

	"nepse-api/src/rest/middleware"
	"nepse-api/src/shared/application"
	"nepse-api/src/shared/config"
	"nepse-api/src/shared/httpmw"
)

// Server is the REST HTTP server.
type Server struct {
	cfg     config.REST
	log     *slog.Logger
	handler http.Handler
	http    *http.Server
	drain   *httpmw.Drain
}

// New builds the router, middleware chain and HTTP server.
func New(cfg config.REST, app *application.App, log *slog.Logger) (*Server, error) {
	drain := &httpmw.Drain{}
	table := routes(app, cfg, drain)
	spec, err := buildOpenAPI(table)
	if err != nil {
		return nil, fmt.Errorf("generate OpenAPI document: %w", err)
	}
	handler := withMiddleware(newMux(table, spec, cfg.DocsEnabled), cfg, log)
	return &Server{
		cfg: cfg, log: log, handler: handler, drain: drain,
		http: &http.Server{
			Addr:              cfg.Addr(),
			Handler:           handler,
			ReadHeaderTimeout: cfg.ReadTimeout,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			MaxHeaderBytes:    16 << 10,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		},
	}, nil
}

// Handler exposes the full handler (router + middleware) for embedding or tests.
func (s *Server) Handler() http.Handler { return s.handler }

// newMux maps every route-table entry, the OpenAPI document and the Swagger UI.
func newMux(table []route, spec []byte, docsEnabled bool) *http.ServeMux {
	mux := http.NewServeMux()
	for _, r := range table {
		mux.Handle(r.Pattern, middleware.Handle(r.Pattern, r.Handler))
	}
	if docsEnabled {
		mux.Handle("/openapi.json", middleware.Handle("/openapi.json", func(w http.ResponseWriter, _ *http.Request) error {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(spec)
			return nil
		}))
		mux.Handle("/docs/", httpmw.Route("/docs/", v5emb.New("NEPSE API", "/openapi.json", "/docs/")))
		// Temporary (not permanent) so browsers never cache a redirect that may change.
		mux.Handle("/docs", http.RedirectHandler("/docs/", http.StatusFound))
	}
	mux.HandleFunc("/", middleware.NotFound)
	return mux
}

// withMiddleware wraps the mux, outermost first: IDs, logging, panic recovery, security headers, CORS, rate limit, concurrency limit, request deadline.
func withMiddleware(mux http.Handler, cfg config.REST, log *slog.Logger) http.Handler {
	limiter := httpmw.NewRateLimiter(cfg.RateLimitPerMinute)
	return httpmw.Chain(mux,
		httpmw.RequestID(),
		httpmw.AccessLog(log, cfg.TrustedProxies, cfg.SlowRequestThreshold, httpmw.LocalProbes),
		httpmw.Recover(log, httpmw.WriteError),
		middleware.SecurityHeaders("/docs"),
		middleware.CORS(cfg.AllowedOrigins),
		httpmw.RateLimit(limiter, cfg.TrustedProxies, httpmw.LocalProbes, httpmw.WriteError),
		httpmw.ConcurrencyLimit(cfg.MaxConcurrent, httpmw.LocalProbes, httpmw.WriteError),
		httpmw.Timeout(cfg.RequestTimeout),
	)
}

// Run serves until ctx is canceled, then drains and shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	return httpmw.Serve(ctx, s.log, "rest server", s.http, httpmw.ServeOptions{
		ShutdownTimeout: s.cfg.ShutdownTimeout, DrainDelay: s.cfg.DrainDelay, Drain: s.drain,
	})
}
