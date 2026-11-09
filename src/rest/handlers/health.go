package handlers

import (
	"net/http"

	"nepse-api/src/rest/middleware"
	"nepse-api/src/shared/httpmw"
)

// Health and Status are the shared probe implementation; the MCP service serves the very same endpoints from it.
type (
	Health = httpmw.Health
	Status = httpmw.HealthStatus
)

// Root serves GET / as a minimal service-status document.
type Root struct {
	Service     string
	DocsEnabled bool
}

type rootBody struct {
	Service string `json:"service"`
	Status  string `json:"status"`
	Docs    string `json:"docs,omitempty"`
	OpenAPI string `json:"openapi,omitempty"`
}

func (h Root) Handle() middleware.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		body := rootBody{Service: h.Service, Status: "ok"}
		if h.DocsEnabled {
			body.Docs, body.OpenAPI = "/docs/", "/openapi.json"
		}
		httpmw.WriteJSON(w, r, http.StatusOK, body)
		return nil
	}
}
