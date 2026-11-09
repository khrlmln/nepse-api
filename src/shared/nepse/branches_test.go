package nepse

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

func TestCABundleHandling(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("[]")) }))
	defer srv.Close()
	dir := t.TempDir()
	pemPath := filepath.Join(dir, "ca.pem")
	_ = os.WriteFile(pemPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600)
	c, err := NewClient(Config{BaseURL: srv.URL, CABundlePath: pemPath, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	_, err = c.Get(context.Background(), EpSummary) // login then fails only because this server is no NEPSE
	if err != nil && strings.Contains(err.Error(), "certificate") {
		t.Fatalf("a bundle containing the CA must satisfy verification: %v", err)
	}
	bad := filepath.Join(dir, "bad.pem")
	_ = os.WriteFile(bad, []byte("not a certificate"), 0o600)
	for name, path := range map[string]string{"missing": filepath.Join(dir, "missing.pem"), "garbage": bad} {
		if c, err := NewClient(Config{BaseURL: "https://www.nepalstock.com", CABundlePath: path}); err == nil {
			_ = c.Close(context.Background())
			t.Errorf("%s bundle accepted", name)
		}
	}
	if _, err := NewClient(Config{BaseURL: "http://insecure.example"}); err == nil {
		t.Fatal("plain http accepted without the opt-in")
	}
}

func TestHistoryUnwrapsContentAndPassesOtherShapesThrough(t *testing.T) {
	f := newFake(t)
	c := f.client(nil)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 10)
	f.histBody.Store(`{"content":[{"businessDate":"2026-01-02"}],"totalPages":1}`)
	v, err := c.PriceVolumeHistory(context.Background(), "NABIL", start, end)
	if rows, ok := v.([]any); err != nil || !ok || len(rows) != 1 {
		t.Fatalf("content must be unwrapped: %v %v", v, err)
	}
	f.histBody.Store(`[{"a":1},{"a":2}]`)
	c.cache = newResponseCache()
	if v, err := c.PriceVolumeHistory(context.Background(), "NABIL", start, end); err != nil || len(v.([]any)) != 2 {
		t.Fatalf("plain arrays pass through: %v %v", v, err)
	}
	f.histBody.Store(`"nope"`)
	c.cache = newResponseCache()
	if _, err := c.PriceVolumeHistory(context.Background(), "NABIL", start, end); apierrors.Normalize(err).Code != apierrors.CodeUpstreamInvalidResp {
		t.Fatalf("%v", err)
	}
	if _, err := c.PriceVolumeHistory(context.Background(), "NOPE", start, start); apierrors.Normalize(err).Code != apierrors.CodeNotFound {
		t.Fatal("unknown symbols are NOT_FOUND")
	}
	for _, dates := range [][2]time.Time{{{}, end}, {start, {}}, {{}, {}}} {
		if _, err := c.PriceVolumeHistory(context.Background(), "NABIL", dates[0], dates[1]); apierrors.Normalize(err).Code != apierrors.CodeInvalidArgument {
			t.Errorf("%v: both dates are required", dates)
		}
	}
}

func TestSymbolScopedAccessorsReportUnknownSymbols(t *testing.T) {
	f := newFake(t)
	c := f.client(nil)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"details": func() error { _, err := c.CompanyDetails(ctx, "NOPE"); return err },
		"graph":   func() error { _, err := c.DailyScripGraph(ctx, "NOPE"); return err },
		"depth":   func() error { _, err := c.MarketDepth(ctx, "NOPE"); return err },
		"floor":   func() error { _, err := c.FloorSheet(ctx, "NOPE", time.Time{}, 0); return err },
	} {
		if apierrors.Normalize(call()).Code != apierrors.CodeNotFound {
			t.Errorf("%s: unknown symbols must be NOT_FOUND", name)
		}
	}
	if _, err := c.MarketDepth(ctx, "NABIL"); err != nil {
		t.Fatal(err)
	}
}

func TestFloorsheetSymbolUsesLatestSessionFallback(t *testing.T) {
	f := newFake(t)
	f.pages = 1
	c := f.client(nil)
	if _, err := c.FloorSheet(context.Background(), "NABIL", time.Time{}, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.seen.Load("/api/nots/nepse-data/floorsheet?&size=500&sort=contractId,desc"); !ok {
		t.Fatal("symbol floorsheet should be filtered from the supported all-market endpoint")
	}
	var companyEndpointCalled bool
	f.seen.Range(func(key, _ any) bool {
		companyEndpointCalled = companyEndpointCalled || strings.Contains(key.(string), "/api/nots/security/floorsheet/")
		return true
	})
	if companyEndpointCalled {
		t.Fatal("blocked per-symbol endpoint must not be called")
	}
}

func TestCacheWaiterHonoursItsOwnContext(t *testing.T) {
	c := newResponseCache()
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _, _, _ = c.Do(context.Background(), "k", time.Minute, func(context.Context) ([]byte, error) {
			close(started)
			<-release
			return []byte("v"), nil
		})
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, _, err := c.Do(ctx, "k", time.Minute, func(context.Context) ([]byte, error) { return nil, nil }); err == nil || ctx.Err() == nil {
		t.Fatalf("a waiter must give up when its own context ends: %v", err)
	}
	close(release)
}

func TestCacheIdleEntriesAreEvictedFirstWhenFull(t *testing.T) {
	c := newResponseCache()
	c.maxEntries = 3
	load := func(key string) {
		if _, _, _, err := c.Do(context.Background(), key, time.Hour, func(context.Context) ([]byte, error) { return []byte(key), nil }); err != nil {
			t.Fatal(err)
		}
	}
	load("old1")
	load("old2")
	load("fresh")
	c.mu.Lock()
	c.entries["old1"].lastUse = time.Now().Add(-time.Hour)
	c.entries["old2"].lastUse = time.Now().Add(-time.Hour)
	c.mu.Unlock()
	load("new") // full: idle entries go first, the recently used one survives
	c.mu.Lock()
	_, hasFresh := c.entries["fresh"]
	_, hasNew := c.entries["new"]
	n := len(c.entries)
	c.mu.Unlock()
	if !hasFresh || !hasNew || n > 3 {
		t.Fatalf("fresh=%v new=%v entries=%d", hasFresh, hasNew, n)
	}
}

func TestConcurrentCoalescingRunsOneLoad(t *testing.T) {
	c := newResponseCache()
	var mu sync.Mutex
	loads := 0
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _, _, err := c.Do(context.Background(), "same", time.Minute, func(context.Context) ([]byte, error) {
				mu.Lock()
				loads++
				mu.Unlock()
				<-gate
				return []byte("shared"), nil
			})
			if err != nil || string(v) != "shared" {
				t.Errorf("%q %v", v, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	if loads != 1 {
		t.Fatalf("%d loads for 20 concurrent callers", loads)
	}
}
