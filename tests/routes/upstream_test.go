package routes_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeUpstream is a minimal NEPSE look-alike. Every endpoint answers with data that identifies the endpoint (a marker symbol or the request path itself), so a test can tell which upstream endpoint a REST route or MCP tool really called. The expected paths below are written out literally, independently of the embedded API_ENDPOINTS.json, so a wrong endpoint mapping cannot hide.
type fakeUpstream struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	hits       map[string]int // "METHOD path" -> count
	requestURI map[string][]string
	unexpected []string
	unauth     []string
}

const (
	salt1, salt2, salt3, salt4, salt5 = 12, 24, 36, 48, 60
)

// jsonRoutes are static bodies keyed by "METHOD path".
var jsonRoutes = map[string]string{
	"GET /api/nots/nepse-data/market-open":  `{"isOpen":"CLOSE","asOf":"2026-11-05T15:00:00","id":7}`,
	"GET /api/nots/market-summary/":         `[{"detail":"Total Turnover Rs:","value":123456.5},{"detail":"Total Traded Shares","value":1000}]`,
	"GET /api/nots/nepse-data/supplydemand": `{"supplyList":[{"symbol":"SUP"}],"demandList":[{"symbol":"DEM"}]}`,
	"GET /api/nots/top-ten/top-gainer":      `[{"symbol":"GAIN","ltp":110,"pointChange":10,"percentageChange":10}]`,
	"GET /api/nots/top-ten/top-loser":       `[{"symbol":"LOSE","ltp":90,"pointChange":-10,"percentageChange":-10}]`,
	"GET /api/nots/top-ten/trade":           `[{"symbol":"TRD","shareTraded":500}]`,
	"GET /api/nots/top-ten/transaction":     `[{"symbol":"TXN","totalTrades":7}]`,
	"GET /api/nots/top-ten/turnover":        `[{"symbol":"TURN","turnover":9000}]`,
	"GET /api/nots/nepse-index":             `[{"index":"NEPSE Index","currentValue":2000.5},{"index":"Sensitive Index","currentValue":400.1}]`,
	"GET /api/nots":                         `[{"index":"Banking SubIndex","currentValue":1500.2},{"index":"HydroPower Index","currentValue":900.7}]`,
	"GET /api/nots/company/list": `[` +
		`{"symbol":"NABIL","securityName":"Nabil Bank Limited","sectorName":"Commercial Banks","instrumentType":"Equity"},` +
		`{"symbol":"GAIN","securityName":"Gain Hydro","sectorName":"Hydro Power","instrumentType":"Equity"}]`,
	"GET /api/nots/security": `[` +
		`{"id":131,"symbol":"NABIL","securityName":"Nabil Bank Limited"},` +
		`{"id":2,"symbol":"PRMT","securityName":"Promoter Holdings"},` +
		`{"id":3,"symbol":"GAIN","securityName":"Gain Hydro"}]`,
	"GET /api/nots/securityDailyTradeStat/58": `[{"symbol":"NABIL","lastTradedPrice":1000,"previousClose":990},{"symbol":"GAIN","lastTradedPrice":110,"previousClose":100}]`,
	"GET /api/nots/lives-market":              `[{"symbol":"LIVE1","lastTradedPrice":1},{"symbol":"LIVE2","lastTradedPrice":2},{"symbol":"LIVE3","lastTradedPrice":3}]`,

	"POST /api/nots/security/131":               `{"security":{"symbol":"NABIL"},"marker":"details"}`,
	"POST /api/nots/market/graphdata/daily/131": `[{"marker":"daily-graph"}]`,
	"GET /api/nots/nepse-data/marketdepth/131/": `{"marketDepth":{"marker":"depth"}}`,
	"GET /api/nots/market/history/security/131": `{"content":[{"businessDate":"2026-11-05","closePrice":1000,"marker":"history"}],"totalPages":1}`,
	"POST /api/nots/nepse-data/floorsheet":      `{"floorsheets":{"content":[{"contractId":"ALL-1","stockSymbol":"GAIN","businessDate":"2026-11-05"},{"contractId":"NABIL-1","stockSymbol":"NABIL","businessDate":"2026-11-05"}],"totalPages":3,"totalElements":1250}}`,
}

// graphPaths are the POST graph endpoints; each answers [{"endpoint": <path>}].
var graphPaths = []string{
	"/api/nots/graph/index/58", "/api/nots/graph/index/57", "/api/nots/graph/index/62", "/api/nots/graph/index/63",
	"/api/nots/graph/index/51", "/api/nots/graph/index/55", "/api/nots/graph/index/60", "/api/nots/graph/index/52",
	"/api/nots/graph/index/54", "/api/nots/graph/index/67", "/api/nots/graph/index/65", "/api/nots/graph/index/56",
	"/api/nots/graph/index/64", "/api/nots/graph/index/66", "/api/nots/graph/index/59", "/api/nots/graph/index/53",
	"/api/nots/graph/index/61",
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	u := &fakeUpstream{t: t, hits: map[string]int{}, requestURI: map[string][]string{}}
	u.srv = httptest.NewServer(http.HandlerFunc(u.handle))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *fakeUpstream) handle(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	u.mu.Lock()
	u.hits[key]++
	u.requestURI[key] = append(u.requestURI[key], r.URL.RequestURI())
	u.mu.Unlock()

	if r.URL.Path == "/api/authenticate/prove" {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"serverTime":   1,
			"accessToken":  strings.Repeat("A", 196) + "0001",
			"refreshToken": strings.Repeat("B", 196) + "0001",
			"salt1":        salt1, "salt2": salt2, "salt3": salt3, "salt4": salt4, "salt5": salt5,
		})
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Salter ") {
		u.mu.Lock()
		u.unauth = append(u.unauth, key)
		u.mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			ID *int `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == nil {
			u.t.Errorf("POST %s: body is not {\"id\": <int>}: %v", r.URL.Path, err)
		}
	}
	for _, p := range graphPaths {
		if key == "POST "+p {
			_ = json.NewEncoder(w).Encode([]map[string]string{{"endpoint": p}})
			return
		}
	}
	if key == "POST /api/nots/nepse-data/floorsheet" {
		page := r.URL.Query().Get("page")
		switch page {
		case "1":
			_, _ = w.Write([]byte(`{"floorsheets":{"content":[{"contractId":"NABIL-2","stockSymbol":"NABIL","businessDate":"2026-11-05"}],"totalPages":3,"totalElements":1250}}`))
		case "2":
			_, _ = w.Write([]byte(`{"floorsheets":{"content":[{"contractId":"NABIL-3","stockSymbol":"NABIL","businessDate":"2026-11-05"}],"totalPages":3,"totalElements":1250}}`))
		default:
			_, _ = w.Write([]byte(jsonRoutes[key]))
		}
		return
	}
	if body, ok := jsonRoutes[key]; ok {
		_, _ = w.Write([]byte(body))
		return
	}
	u.mu.Lock()
	u.unexpected = append(u.unexpected, key)
	u.mu.Unlock()
	w.WriteHeader(http.StatusNotFound)
}

// lastURI returns the most recent request URI recorded for "METHOD path".
func (u *fakeUpstream) lastURI(key string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	uris := u.requestURI[key]
	if len(uris) == 0 {
		return ""
	}
	return uris[len(uris)-1]
}

// assertAllEndpointsUsed fails if the application asked for an endpoint the fake does not know, sent an unauthenticated data request, or never used one of the fake's endpoints (an endpoint nothing is mapped to).
func (u *fakeUpstream) assertAllEndpointsUsed() {
	u.t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.unexpected) > 0 {
		u.t.Errorf("application called unknown upstream endpoints: %v", u.unexpected)
	}
	if len(u.unauth) > 0 {
		u.t.Errorf("data requests without the Salter authorization header: %v", u.unauth)
	}
	var unused []string
	for key := range jsonRoutes {
		if u.hits[key] == 0 {
			unused = append(unused, key)
		}
	}
	for _, p := range graphPaths {
		if u.hits["POST "+p] == 0 {
			unused = append(unused, "POST "+p)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		u.t.Errorf("upstream endpoints no REST route or MCP tool ever reached:\n  %s", strings.Join(unused, "\n  "))
	}
}

// field walks nested maps/slices: field(v, "data", 0, "symbol").
func field(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			s, ok := v.([]any)
			if !ok || k >= len(s) {
				return nil
			}
			v = s[k]
		}
	}
	return v
}

func show(v any) string { return fmt.Sprintf("%v", v) }
