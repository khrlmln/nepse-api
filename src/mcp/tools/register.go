// Package tools registers the MCP tools. Each tool is a thin adapter over a shared use case: it declares a typed input, calls one use case and returns the result in the same {"data","meta"} shape as the REST API.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"nepse-api/src/shared/application"
	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/observability"
)

// Deps are the collaborators every tool needs.
type Deps struct {
	App     *application.App
	Log     *slog.Logger
	Timeout time.Duration
}

// Register adds every tool to the server.
func Register(s *mcp.Server, d Deps) {
	registerMarket(s, d)
	registerSecurities(s, d)
	registerTrading(s, d)
}

// Result is what a use-case adapter returns on success.
type Result struct {
	Data any `json:"data"`
	Meta any `json:"meta,omitempty"`
}

type errorBody struct {
	Code      apierrors.Code `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Details   any            `json:"details,omitempty"`
}

func ptr[T any](v T) *T { return &v }

// add registers a read-only tool whose behaviour is run(ctx, input).
func add[In any](s *mcp.Server, d Deps, name, title, description string, run func(context.Context, In) (Result, error)) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: description,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		return d.invoke(withInboundRequestID(ctx, req), name, func(ctx context.Context) (Result, error) { return run(ctx, in) })
	})
}

// withInboundRequestID restores the HTTP request's correlation ID. The SDK runs tool handlers detached from the HTTP request context, but exposes the inbound headers, on which the HTTP middleware stamped the canonical X-Request-ID.
func withInboundRequestID(ctx context.Context, req *mcp.CallToolRequest) context.Context {
	if observability.RequestID(ctx) != "" || req == nil || req.Extra == nil {
		return ctx
	}
	if id := req.Extra.Header.Get("X-Request-ID"); observability.ValidRequestID(id) {
		return observability.WithRequestID(ctx, id)
	}
	return ctx
}

// invoke runs one tool call with a deadline and panic protection, logs exactly one event for it, and converts failures into a structured tool error result (so the model sees a stable code and can recover) instead of a protocol error.
func (d Deps) invoke(ctx context.Context, tool string, fn func(context.Context) (Result, error)) (*mcp.CallToolResult, any, error) {
	start := time.Now()
	rid := observability.RequestID(ctx)
	if rid == "" { // the SDK did not carry the HTTP request context through
		rid = observability.NewRequestID()
		ctx = observability.WithRequestID(ctx, rid)
	}
	ctx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()

	var (
		result Result
		err    error
	)
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				stack := string(debug.Stack())
				if len(stack) > 4096 {
					stack = stack[:4096]
				}
				d.Log.Error("panic recovered in tool", "request_id", rid, "tool", tool, "panic_type", fmt.Sprintf("%T", rec), "stack", stack)
				err = apierrors.Internal(fmt.Errorf("panic in MCP tool (%T)", rec))
			}
		}()
		result, err = fn(ctx)
	}()

	attrs := []any{"request_id", rid, "tool", tool, "duration_ms", float64(time.Since(start).Microseconds()) / 1000}
	if err != nil {
		apiErr := apierrors.Normalize(err)
		level := slog.LevelWarn
		if apiErr.Status() < 500 {
			level = slog.LevelInfo
		} else if apiErr.Code == apierrors.CodeInternal {
			level = slog.LevelError
		}
		d.Log.Log(ctx, level, "mcp tool failed", append(append(attrs, "outcome", "error"), observability.ErrorAttrs(apiErr)...)...)
		return errorResult(apiErr, rid), nil, nil
	}
	d.Log.Log(ctx, slog.LevelInfo, "mcp tool completed", append(attrs, "outcome", "ok")...)
	return nil, result, nil
}

// errorResult is the tool-level failure: IsError with a JSON body carrying the same code/message/request_id/details as the REST error.
func errorResult(e *apierrors.ApiError, requestID string) *mcp.CallToolResult {
	body := map[string]any{"error": errorBody{Code: e.Code, Message: e.Message, RequestID: requestID, Details: e.Details}}
	text, err := json.Marshal(body)
	if err != nil {
		text = []byte(`{"error":{"code":"INTERNAL","message":"An internal error occurred."}}`)
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}, StructuredContent: body}
}
