package rest

import "nepse-api/src/rest/handlers"

// healthRoutes are the unversioned status and probe endpoints.
func healthRoutes(health handlers.Health, root handlers.Root) []route {
	return []route{
		{"/{$}", operation{ID: "getServiceStatus", Tag: "Health", Summary: "Service status",
			Description: "Minimal status document for humans and uptime checks; makes no upstream call.", Response: kindHealth}, root.Handle()},
		{"/health/live", operation{ID: "getLiveness", Tag: "Health", Summary: "Liveness",
			Description: "The process is running and serving requests. Makes no upstream call.", Response: kindHealth}, health.Live()},
		{"/health/ready", operation{ID: "getReadiness", Tag: "Health", Summary: "Readiness",
			Description: "The process finished starting (assets loaded) and is not shutting down; 503 UNAVAILABLE while draining. Makes no upstream call, so NEPSE downtime never makes the service unready.", Response: kindHealth}, health.Ready()},
		{"/health/dependencies", operation{ID: "getDependencies", Tag: "Health", Summary: "Dependency check",
			Description: "Actively checks, uncached and within 5 seconds, that NEPSE is reachable and its upstream authentication flow works. Rate limited; use for upstream diagnostics, not process liveness.", Response: kindHealth}, health.Dependencies()},
	}
}
