package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	apierrors "nepse-api/src/shared/errors"
	"nepse-api/src/shared/observability"
	"nepse-api/src/shared/validation"
)

func testDeps(t *testing.T, timeout time.Duration) (Deps, *bytes.Buffer) {
	var buf bytes.Buffer
	log, _ := observability.NewLogger(&buf, "debug", "json", "t")
	return Deps{Log: log, Timeout: timeout}, &buf
}

func errBody(t *testing.T, res *mcp.CallToolResult) (code, requestID string) {
	t.Helper()
	if res == nil || !res.IsError || len(res.Content) != 1 {
		t.Fatalf("expected a tool error result, got %+v", res)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	if res.StructuredContent == nil {
		t.Fatal("errors must also be available as structured content")
	}
	return body.Error.Code, body.Error.RequestID
}

func TestInvokeSuccessReturnsTheResultAndLogsOnce(t *testing.T) {
	d, logs := testDeps(t, time.Second)
	res, out, err := d.invoke(observability.WithRequestID(context.Background(), "rid-1"), "my_tool", func(context.Context) (Result, error) {
		return Result{Data: map[string]int{"a": 1}, Meta: "m"}, nil
	})
	if err != nil || res != nil {
		t.Fatalf("%v %v", res, err)
	}
	if r := out.(Result); r.Meta != "m" {
		t.Fatalf("%+v", out)
	}
	if n := strings.Count(logs.String(), `"msg":"mcp tool completed"`); n != 1 || !strings.Contains(logs.String(), `"request_id":"rid-1"`) || !strings.Contains(logs.String(), `"tool":"my_tool"`) {
		t.Fatalf("%s", logs.String())
	}
}

func TestInvokeConvertsEveryFailureKindToAStructuredToolError(t *testing.T) {
	cases := []struct {
		name  string
		fn    func(context.Context) (Result, error)
		code  string
		level string
	}{
		{"validation", func(context.Context) (Result, error) { return Result{}, apierrors.InvalidArgument("bad") }, "INVALID_ARGUMENT", "INFO"},
		{"not found", func(context.Context) (Result, error) { return Result{}, apierrors.NotFound("x") }, "NOT_FOUND", "INFO"},
		{"upstream", func(context.Context) (Result, error) { return Result{}, apierrors.UpstreamUnavailable(errors.New("x")) }, "UPSTREAM_UNAVAILABLE", "WARN"},
		{"internal", func(context.Context) (Result, error) { return Result{}, errors.New("secret detail") }, "INTERNAL", "ERROR"},
		{"panic", func(context.Context) (Result, error) { panic("boom secret") }, "INTERNAL", "ERROR"},
		{"timeout", func(ctx context.Context) (Result, error) { <-ctx.Done(); return Result{}, ctx.Err() }, "DEADLINE_EXCEEDED", "WARN"},
	}
	for _, c := range cases {
		d, logs := testDeps(t, 50*time.Millisecond)
		res, out, err := d.invoke(observability.WithRequestID(context.Background(), "rid-2"), "t", c.fn)
		if err != nil || out != nil {
			t.Errorf("%s: failures must be tool results, not protocol errors: %v %v", c.name, out, err)
			continue
		}
		code, rid := errBody(t, res)
		if code != c.code || rid != "rid-2" {
			t.Errorf("%s: code=%s rid=%s", c.name, code, rid)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if strings.Contains(text, "secret") || strings.Contains(text, "goroutine") {
			t.Errorf("%s: internals leaked to the client: %s", c.name, text)
		}
		if !strings.Contains(logs.String(), `"level":"`+c.level+`"`) || strings.Count(logs.String(), `"msg":"mcp tool failed"`) != 1 {
			t.Errorf("%s: expected exactly one %s log:\n%s", c.name, c.level, logs.String())
		}
		if c.name == "panic" && (strings.Contains(logs.String(), "boom secret") || !strings.Contains(logs.String(), `"panic_type":"string"`)) {
			t.Errorf("panic logs must include type but not payload:\n%s", logs.String())
		}
	}
}

func TestInvokeGeneratesAnIDWhenNoneWasPropagated(t *testing.T) {
	d, _ := testDeps(t, time.Second)
	res, _, _ := d.invoke(context.Background(), "t", func(context.Context) (Result, error) { return Result{}, apierrors.NotFound("x") })
	if _, rid := errBody(t, res); !strings.HasPrefix(rid, "req_") {
		t.Fatalf("%q", rid)
	}
}

func TestWithInboundRequestID(t *testing.T) {
	mk := func(id string) *mcp.CallToolRequest {
		h := map[string][]string{}
		if id != "" {
			h["X-Request-Id"] = []string{id}
		}
		return &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: h}}
	}
	if got := observability.RequestID(withInboundRequestID(context.Background(), mk("corr-1"))); got != "corr-1" {
		t.Fatalf("%q", got)
	}
	existing := observability.WithRequestID(context.Background(), "ctx-id") // an id already on the context wins
	if got := observability.RequestID(withInboundRequestID(existing, mk("corr-1"))); got != "ctx-id" {
		t.Fatalf("%q", got)
	}
	for _, bad := range []*mcp.CallToolRequest{mk(""), mk("bad id with spaces"), mk(strings.Repeat("a", 100)), {Extra: nil}, nil} {
		if got := observability.RequestID(withInboundRequestID(context.Background(), bad)); got != "" {
			t.Errorf("%+v -> %q", bad, got)
		}
	}
}

func TestPageAny(t *testing.T) {
	items := []any{1, 2, 3, 4, 5}
	data, meta, err := pageAny(items, PageInput{Page: 2, Limit: 2})
	if m, ok := meta.(validation.PageMeta); err != nil || len(data.([]any)) != 2 || !ok || m.Total != 5 || m.Page != 2 || m.Limit != 2 {
		t.Fatalf("%v %v %v", data, meta, err)
	}
	if data, meta, err := pageAny(map[string]any{"a": 1}, PageInput{Limit: 99999}); err != nil || meta != nil || data == nil {
		t.Fatalf("non-arrays pass through unpaginated: %v %v %v", data, meta, err)
	}
	if _, _, err := pageAny(items, PageInput{Limit: 99999}); apierrors.Normalize(err).Code != apierrors.CodeInvalidArgument {
		t.Fatalf("%v", err)
	}
	if _, _, err := pageAny(items, PageInput{Page: -1}); err == nil {
		t.Fatal("negative page accepted")
	}
}
