package rest

import (
	"strings"

	"nepse-api/src/rest/handlers"
	"nepse-api/src/rest/middleware"
	"nepse-api/src/shared/application"
	"nepse-api/src/shared/config"
	"nepse-api/src/shared/httpmw"
)

// responseKind says what the "data" member of a success body looks like; it is used only to document the endpoint in the OpenAPI document.
type responseKind int

const (
	kindObject     responseKind = iota // an object
	kindArray                          // an array of objects
	kindAny                            // an array or an object (upstream-defined)
	kindPaged                          // array + pagination meta
	kindFloorsheet                     // array + floorsheet meta
	kindStrings                        // array of strings
	kindHealth                         // health body (not enveloped)
)

type param struct {
	Name        string
	In          string // "path" or "query"
	Description string
	Required    bool
	Type        string // JSON schema type, default "string"
	Enum        []string
}

type operation struct {
	ID          string
	Tag         string
	Summary     string
	Description string
	Params      []param
	Response    responseKind
}

// route is the single source of truth for an endpoint: the router and the OpenAPI document are both generated from this table, so they cannot drift.
type route struct {
	Pattern string
	Op      operation
	Handler middleware.HandlerFunc
}

// apiV1 prefixes every business endpoint; health and status routes live at the root.
const apiV1 = "/api/v1"

const passthroughNote = " Item fields are defined by NEPSE and passed through unchanged; they are not guaranteed by this API."

// routes builds the complete route table. Each group lives in its own file (routes_market.go, routes_securities.go, routes_trading.go, routes_health.go).
func routes(app *application.App, cfg config.REST, drain *httpmw.Drain) []route {
	var table []route
	table = append(table, marketRoutes(handlers.Market{Svc: app.Market})...)
	table = append(table, securityRoutes(handlers.Securities{Svc: app.Securities})...)
	table = append(table, tradingRoutes(handlers.Trading{Svc: app.Trading})...)
	table = append(table, healthRoutes(
		handlers.Health{Ping: app.Client.Ping, Drain: drain},
		handlers.Root{Service: cfg.ServiceName, DocsEnabled: cfg.DocsEnabled})...)
	return table
}

// title upper-cases the first letter ("gainers" -> "Gainers") for operation IDs.
func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
