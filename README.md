# NEPSE API

**NEPSE API — a Go-based REST API and Model Context Protocol (MCP) server for accessing Nepal Stock Exchange market data.**

NEPSE API provides a unified interface to retrieve market information, security details, historical price data, trading activity and other publicly available NEPSE data. It is designed for internal applications, automation workflows and AI assistants using MCP-compatible clients such as Claude.

> This is an unofficial integration. Upstream endpoints and response formats can change. Data is not investment advice.

## Requirements

- Go version declared in [`go.mod`](go.mod) (Go 1.25 or newer).
- Outbound HTTPS access to `www.nepalstock.com`.
- A C compiler with CGO enabled for the race-enabled test command.

## Quick start

```sh
cp .env.example .env
make run-rest
```

In a second terminal, start MCP independently:

```sh
make run-mcp
```

Default listeners are loopback-only:

| Service | Address              | Purpose                      |
| ------- | -------------------- | ---------------------------- |
| REST    | `127.0.0.1:8000`     | HTTP/JSON API                |
| MCP     | `127.0.0.1:9000/mcp` | Streamable HTTP MCP endpoint |

`make build` creates `bin/nepse-rest` and `bin/nepse-mcp`. You can also run `make build-rest` or `make build-mcp` separately. The run commands start only their named service.

## Testing

Run the complete test suite with one project command:

```sh
make test
```

This runs the complete Go test suite exactly once. It includes package tests, binary lifecycle tests, and live read-only integration tests against NEPSE through the REST and MCP transports. It requires outbound network access and a working NEPSE upstream, plus a C compiler for the race detector. Tests do not use checked-in market-response snapshots.

## REST endpoints

- `GET /health/live`, `/health/ready`, `/health/dependencies`
- `GET /api/v1/market/*` — summary, status, indices, live market, top lists, graphs, supply/demand and turnover overview
- `GET /api/v1/securities*` — security list/search, details, validation, market depth, price/volume, price history and graphs
- `GET /api/v1/sectors*` — sector list and sector members
- `GET /api/v1/trading/floorsheet*` — paginated floorsheet data; per-symbol results are filtered from the latest all-market session because NEPSE blocks its per-symbol upstream endpoint

A symbol-specific floorsheet lookup is more expensive than an all-market page: it scans the latest session (typically tens of thousands of trades) and filters locally. Pages are cached briefly to reduce repeat downloads. `NEPSE_MAX_FLOOR_PAGES` and `NEPSE_MAX_FLOOR_ROWS` bound the scan; a session above either configured limit returns `503 UNAVAILABLE`. Historical per-symbol dates are not available from this upstream endpoint.

- `GET /openapi.json` and `/docs/` — API specification and Swagger UI when `REST_DOCS_ENABLED=true`

The route table in `src/rest/routes*.go` generates both the REST routes and OpenAPI operations.

## MCP tools

The MCP server exposes read-only tools for market summary/status, indices, live market, top lists, graphs, supply/demand, turnover overview, securities, sectors, price history, and floorsheet data. Symbol-specific floorsheets are filtered from the latest all-market session because NEPSE blocks its per-symbol upstream endpoint; historical symbol-specific data is unavailable.

## Highlights

### REST API

A production-oriented HTTP API exposing NEPSE market data through structured JSON responses.

Available functionality includes:

- Market summary, status and indices.
- Sub-indices and live market information.
- Top gainers, losers, turnover, transactions and traded shares.
- Supply and demand information.
- Security listing, searching and validation.
- Security details and market depth.
- Security price-volume information.
- Historical price data and security graphs.
- Sector listings and sector-specific securities.
- Index and sector graphs.
- Market floorsheet with pagination and symbol filtering.

An OpenAPI specification and Swagger documentation are included.

### MCP Server

A standalone MCP server exposing **24 tools** for AI assistants and MCP-compatible applications.

Tools cover:

- Market information and index analysis.
- Top market performers and trading activity.
- Security discovery and validation.
- Security details and historical prices.
- Market depth and price-volume information.
- Sector and index analysis.
- Floorsheet retrieval.

REST and MCP share the same underlying business services and NEPSE client, ensuring consistent data and behavior across both interfaces.

### NEPSE Integration

- Automated upstream authentication and token refresh.
- NEPSE-compatible request payload generation.
- Embedded runtime assets required for upstream communication.
- Bounded HTTP requests, retries and timeouts.
- Response validation and structured error handling.
- Caching and request coalescing.
- Symbol-specific floorsheet retrieval through paginated all-market data filtering when the upstream per-security endpoint is unavailable.

Symbol-specific floorsheet filtering supports the latest available trading session; arbitrary historical symbol-specific retrieval is not guaranteed.

## Production and Deployment

The application is designed to run as two independent native binaries:

- `nepse-rest`
- `nepse-mcp`

Production features include:

- Structured logging and sensitive-data redaction.
- Graceful shutdown.
- Request timeouts and concurrency limits.
- Configurable CORS and trusted-proxy handling.
- Health and readiness endpoints.
- Production build metadata.

**Security note:** Application-level caller authentication is intentionally not implemented. Deployments should restrict access through a VPN, firewall or reverse-proxy IP allowlist. Do not expose the application listeners directly to the public internet.

## Supported Platforms

Automated release builds target:

| Operating System | Architectures                        |
| ---------------- | ------------------------------------ |
| Linux            | AMD64, ARM64                         |
| Windows          | AMD64, ARM64                         |
| macOS            | AMD64 (Intel), ARM64 (Apple Silicon) |

Each platform package includes both REST and MCP binaries.

## Acknowledgements

This project relies on NEPSE's publicly accessible market-data services and the embedded runtime assets required to communicate with its upstream endpoints.
