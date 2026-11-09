package rest

import (
	"encoding/json"
	"sort"
	"strings"
)

const apiVersion = "1.0.0"

func obj(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

var anyObject = obj("type", "object", "additionalProperties", true)

func envelope(data map[string]any, meta string) map[string]any {
	props := obj("data", data)
	if meta != "" {
		props["meta"] = obj("$ref", "#/components/schemas/"+meta)
	}
	return obj("type", "object", "required", []string{"data"}, "properties", props)
}

func dataSchema(k responseKind) map[string]any {
	switch k {
	case kindObject:
		return anyObject
	case kindArray, kindPaged, kindFloorsheet:
		return obj("type", "array", "items", anyObject)
	case kindStrings:
		return obj("type", "array", "items", obj("type", "string"))
	default: // kindAny
		return obj("description", "An array or an object, as defined by NEPSE.", "oneOf", []any{obj("type", "array", "items", anyObject), anyObject})
	}
}

func responseSchema(k responseKind) map[string]any {
	switch k {
	case kindPaged:
		return envelope(dataSchema(k), "Pagination")
	case kindFloorsheet:
		return envelope(dataSchema(k), "FloorsheetPage")
	case kindHealth:
		return obj("$ref", "#/components/schemas/HealthStatus")
	default:
		return envelope(dataSchema(k), "")
	}
}

func parameter(p param) map[string]any {
	typ := p.Type
	if typ == "" {
		typ = "string"
	}
	schema := obj("type", typ)
	if len(p.Enum) > 0 {
		schema["enum"] = p.Enum
	}
	return obj("name", p.Name, "in", p.In, "required", p.Required || p.In == "path", "description", p.Description, "schema", schema)
}

func errorRef(desc string) map[string]any {
	return obj("description", desc, "content", obj("application/json", obj("schema", obj("$ref", "#/components/schemas/ErrorResponse"))))
}

// buildOpenAPI generates the OpenAPI 3.0 document from the route table.
func buildOpenAPI(rs []route) ([]byte, error) {
	paths := map[string]any{}
	tagSet := map[string]struct{}{}
	for _, r := range rs {
		op := r.Op
		tagSet[op.Tag] = struct{}{}
		params := make([]any, 0, len(op.Params))
		hasPath := false
		for _, p := range op.Params {
			params = append(params, parameter(p))
			hasPath = hasPath || p.In == "path"
		}
		responses := obj("200", obj("description", "Success.", "content", obj("application/json", obj("schema", responseSchema(op.Response)))),
			"405", errorRef("Method not allowed (METHOD_NOT_ALLOWED)."),
			"500", errorRef("Unexpected internal server error (INTERNAL)."),
			"499", errorRef("The client canceled the request (CANCELED)."))
		if op.Tag != "Health" {
			if len(op.Params) > 0 {
				responses["400"] = errorRef("Invalid input (INVALID_ARGUMENT).")
			}
			if hasPath {
				responses["404"] = errorRef("Unknown resource (NOT_FOUND).")
			}
			responses["429"] = errorRef("Rate limited (RATE_LIMITED); see Retry-After.")
			responses["502"] = errorRef("NEPSE authentication failed or returned an unexpected response (UPSTREAM_AUTH_FAILED, UPSTREAM_INVALID_RESPONSE).")
			responses["503"] = errorRef("NEPSE unavailable or the requested floorsheet session cannot be served (UPSTREAM_UNAVAILABLE, UNAVAILABLE).")
			responses["504"] = errorRef("Deadline exceeded (DEADLINE_EXCEEDED).")
		} else {
			switch op.ID {
			case "getReadiness":
				responses["503"] = errorRef("Service is not ready (UNAVAILABLE).")
			case "getDependencies":
				responses["429"] = errorRef("Rate limited (RATE_LIMITED); see Retry-After.")
				responses["502"] = errorRef("NEPSE authentication failed or returned an unexpected response (UPSTREAM_AUTH_FAILED, UPSTREAM_INVALID_RESPONSE).")
				responses["503"] = errorRef("NEPSE unavailable (UPSTREAM_UNAVAILABLE).")
				responses["504"] = errorRef("Dependency check deadline exceeded (DEADLINE_EXCEEDED).")
			}
		}
		operation := obj("operationId", op.ID, "tags", []string{op.Tag}, "summary", op.Summary, "description", op.Description, "responses", responses)
		if len(params) > 0 {
			operation["parameters"] = params
		}
		paths[strings.TrimSuffix(r.Pattern, "{$}")] = obj("get", operation)
	}
	tags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	tagObjs := make([]any, len(tags))
	for i, t := range tags {
		tagObjs[i] = obj("name", t)
	}

	doc := obj(
		"openapi", "3.0.3",
		"info", obj("title", "NEPSE API", "version", apiVersion,
			"description", "Read-only market data from the Nepal Stock Exchange (NEPSE) through an unofficial integration. "),
		"tags", tagObjs,
		"paths", paths,
		"components", obj("schemas", obj(
			"ErrorResponse", obj("type", "object", "required", []string{"error"}, "properties", obj("error", obj(
				"type", "object", "required", []string{"code", "message"},
				"properties", obj(
					"code", obj("type", "string", "enum", []string{"INVALID_ARGUMENT", "METHOD_NOT_ALLOWED", "FORBIDDEN", "NOT_FOUND", "RATE_LIMITED",
						"UPSTREAM_AUTH_FAILED", "UPSTREAM_INVALID_RESPONSE", "UPSTREAM_UNAVAILABLE", "DEADLINE_EXCEEDED", "CANCELED", "UNAVAILABLE", "INTERNAL"}),
					"message", obj("type", "string"),
					"request_id", obj("type", "string"),
					"details", obj("description", "Optional safe details, e.g. symbol suggestions."),
				)))),
			"Pagination", obj("type", "object", "required", []string{"page", "limit", "total"},
				"properties", obj("page", obj("type", "integer"), "limit", obj("type", "integer"), "total", obj("type", "integer"))),
			"FloorsheetPage", obj("type", "object", "required", []string{"page", "page_size"},
				"properties", obj("page", obj("type", "integer"), "page_size", obj("type", "integer"),
					"total_pages", obj("type", "integer"), "total_elements", obj("type", "integer"))),
			"HealthStatus", obj("type", "object", "required", []string{"status"},
				"properties", obj("status", obj("type", "string"), "checks", obj("type", "object", "additionalProperties", obj("type", "string")))),
		)),
	)
	return json.MarshalIndent(doc, "", "  ")
}
