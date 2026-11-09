package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	apierrors "nepse-api/src/shared/errors"
)

func TestNewLoggerLevelsFormatsAndServiceAttribute(t *testing.T) {
	var buf bytes.Buffer
	log, err := NewLogger(&buf, "WARN", "JSON", "svc")
	if err != nil {
		t.Fatal(err)
	}
	log.Info("hidden")
	log.Warn("shown", "k", "v")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("level filtering failed: %q", buf.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil || m["service"] != "svc" || m["msg"] != "shown" || m["k"] != "v" {
		t.Fatalf("%v %v", m, err)
	}
	buf.Reset()
	text, _ := NewLogger(&buf, "debug", "text", "svc")
	text.Debug("hello")
	if !strings.Contains(buf.String(), "msg=hello") || !strings.Contains(buf.String(), "service=svc") {
		t.Fatalf("%q", buf.String())
	}
	for _, level := range []string{"", "info", "warning", "error"} {
		if _, err := NewLogger(&buf, level, "json", "s"); err != nil {
			t.Errorf("level %q rejected: %v", level, err)
		}
	}
	if _, err := NewLogger(&buf, "loud", "json", "s"); err == nil {
		t.Fatal("bad level accepted")
	}
	if _, err := NewLogger(&buf, "info", "xml", "s"); err == nil {
		t.Fatal("bad format accepted")
	}
}

func TestSensitiveAttributesAreRedactedByKey(t *testing.T) {
	var buf bytes.Buffer
	log, _ := NewLogger(&buf, "info", "json", "s")
	log.Info("x", "Authorization", "Bearer abc", "access_token", "t", "salt1", 5, "password", "p", "api_key", "k", "cookie", "c", "safe", "visible")
	out := buf.String()
	for _, leaked := range []string{"Bearer abc", `"t"`, `"p"`, `"k"`, `"c"`} {
		if strings.Contains(out, leaked) {
			t.Errorf("leaked %s in %s", leaked, out)
		}
	}
	if strings.Count(out, "[REDACTED]") != 6 || !strings.Contains(out, "visible") {
		t.Fatalf("redaction wrong: %s", out)
	}
}

func TestRequestIDHelpers(t *testing.T) {
	if RequestID(context.Background()) != "" {
		t.Fatal("empty context must have no id")
	}
	if RequestID(WithRequestID(context.Background(), "abc")) != "abc" {
		t.Fatal("id lost")
	}
	a, b := NewRequestID(), NewRequestID()
	if a == b || !strings.HasPrefix(a, "req_") || len(a) != len("req_")+24 || !ValidRequestID(a) {
		t.Fatalf("%q %q", a, b)
	}
	for _, good := range []string{"a", "A-Z_0.9", strings.Repeat("x", 64)} {
		if !ValidRequestID(good) {
			t.Errorf("%q rejected", good)
		}
	}
	for _, bad := range []string{"", strings.Repeat("x", 65), "a b", "a\nb", "a/b", "ü", "a\x00b", `a"b`} {
		if ValidRequestID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSanitize(t *testing.T) {
	if got := Sanitize("line1\nline2\r\ttab\x00nul"); strings.ContainsAny(got, "\n\r\t\x00") || got != "line1 line2  tab nul" {
		t.Fatalf("control characters not neutralised: %q", got)
	}
	got := Sanitize(strings.Repeat("é", 300)) // 2 bytes per rune
	if !utf8.ValidString(got) || len(got) > maxLogValueLen+len("…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncation broke UTF-8 or the bound: valid=%v len=%d", utf8.ValidString(got), len(got))
	}
	if !utf8.ValidString(Sanitize("bad\xffbytes")) {
		t.Fatal("invalid UTF-8 survived")
	}
	if Sanitize("plain") != "plain" {
		t.Fatal("plain text changed")
	}
}

func TestRedactQuery(t *testing.T) {
	if RedactQuery("") != "" {
		t.Fatal("empty")
	}
	got := RedactQuery("page=2&token=abc&Api_Key=zzz&q=nabil")
	if strings.Contains(got, "abc") || strings.Contains(got, "zzz") || !strings.Contains(got, "page=2") || !strings.Contains(got, "q=nabil") {
		t.Fatalf("%s", got)
	}
	if RedactQuery("%zz=%") != "[REDACTED]" {
		t.Fatal("unparseable queries must not be logged verbatim")
	}
}

func TestErrorAttrsKinds(t *testing.T) {
	attr := func(e *apierrors.ApiError) map[string]any {
		m := map[string]any{}
		a := ErrorAttrs(e)
		for i := 0; i+1 < len(a); i += 2 {
			m[a[i].(string)] = a[i+1]
		}
		return m
	}
	if m := attr(apierrors.NotFound("x")); m["error_kind"] != "client" || m["error_code"] != "NOT_FOUND" || m["error_cause"] != nil {
		t.Fatalf("%v", m)
	}
	if m := attr(apierrors.UpstreamUnavailable(errors.New("dial tcp\nfailed"))); m["error_kind"] != "upstream" || m["error_cause"] != "dial tcp failed" {
		t.Fatalf("%v", m)
	}
	if m := attr(apierrors.Internal(errors.New("bug"))); m["error_kind"] != "internal" {
		t.Fatalf("%v", m)
	}
}
