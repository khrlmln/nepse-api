package nepse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

const (
	// floorEmptyTries bounds how often a mid-sheet page that NEPSE answers with `[]` is requested again (NEPSE does this spuriously).
	floorEmptyTries = 3
	// Per-symbol floorsheet is blocked upstream. Cache the full-session pages briefly because filtering one symbol requires walking every page.
	floorPageCacheTTL = 30 * time.Second
	floorScanWorkers  = 3
)

type floorEnvelope struct {
	Floorsheets *struct {
		Content       []json.RawMessage `json:"content"`
		TotalPages    json.RawMessage   `json:"totalPages"`
		TotalElements json.RawMessage   `json:"totalElements"`
	} `json:"floorsheets"`
}

// parseCount reads a non-negative integer that NEPSE may send as a number or a quoted number. present is false for an absent/null field; ok is false when a present value is not such an integer.
func parseCount(raw json.RawMessage) (n int64, present, ok bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0, false, true
	}
	n, err := strconv.ParseInt(strings.Trim(text, `"`), 10, 64)
	return n, true, err == nil && n >= 0
}

// FloorSheet returns one upstream page (zero-based, 500 rows) of the day's floorsheet. NEPSE blocks its per-symbol floorsheet endpoint, so symbol queries are served by filtering the supported all-market session pages locally. A symbol query can only address the latest available session; older historical symbol-specific floorsheets are not exposed by NEPSE's public endpoint.
func (c *Client) FloorSheet(ctx context.Context, symbol string, businessDate time.Time, page int) (FloorSheetPage, error) {
	if err := c.checkFloorPage(page); err != nil {
		return FloorSheetPage{}, err
	}
	if symbol == "" {
		return c.marketFloorSheetPage(ctx, page)
	}
	if _, err := c.SecurityID(ctx, symbol); err != nil {
		return FloorSheetPage{}, err
	}
	return c.floorSheetByFilteringSession(ctx, symbol, businessDate, page)
}

func (c *Client) maxFloorPageCount() int {
	rows := c.cfg.MaxFloorRows / FloorSheetPageSize
	if c.cfg.MaxFloorRows%FloorSheetPageSize != 0 {
		rows++
	}
	return min(c.cfg.MaxFloorPages, rows)
}

func (c *Client) checkFloorPage(page int) error {
	maxPages := c.maxFloorPageCount()
	if page < 0 || page >= maxPages {
		return apierrors.InvalidArgument(fmt.Sprintf("page must be between 0 and %d.", maxPages-1))
	}
	return nil
}

// marketFloorSheetPage fetches and caches one supported all-market floorsheet page. The cache coalesces concurrent requests for the same page and prevents a symbol-filter request from downloading the same session repeatedly.
func (c *Client) marketFloorSheetPage(ctx context.Context, page int) (FloorSheetPage, error) {
	path := c.marketFloorSheetPath(page)
	body, _, stale, err := c.cache.Do(ctx, "POST "+path, floorPageCacheTTL, func(loadCtx context.Context) ([]byte, error) {
		for try := 1; ; try++ {
			response, err := c.postRaw(loadCtx, path, modeFloor)
			if err != nil {
				return nil, err
			}
			if isEmptySheet(response) {
				if page == 0 || try >= floorEmptyTries {
					return response, nil
				}
				continue
			}
			if _, err := parseFloorEnvelope(response, page); err != nil {
				return nil, err
			}
			return response, nil
		}
	})
	if err != nil {
		return FloorSheetPage{}, err
	}
	if stale {
		c.log().Log(ctx, slog.LevelWarn, "serving a stale NEPSE floorsheet page", "operation", "stale_cache", "path", route(path))
	}
	if isEmptySheet(body) {
		return emptyFloorPage(page, page == 0), nil
	}
	return parseFloorEnvelope(body, page)
}

func (c *Client) marketFloorSheetPath(page int) string {
	path := fmt.Sprintf("%s?&size=%d&sort=contractId,desc", c.endpoint(EpFloorSheet), FloorSheetPageSize)
	if page > 0 {
		path += "&page=" + strconv.Itoa(page)
	}
	return path
}

func (c *Client) floorSheetByFilteringSession(ctx context.Context, symbol string, businessDate time.Time, page int) (FloorSheetPage, error) {
	first, err := c.marketFloorSheetPage(ctx, 0)
	if err != nil {
		return FloorSheetPage{}, err
	}
	if len(first.Rows) == 0 {
		if first.TotalPages > 0 || first.TotalElements > 0 {
			return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet page 0 is empty but pagination reports data"), "NEPSE returned inconsistent floorsheet pagination metadata.")
		}
		if !businessDate.IsZero() {
			return FloorSheetPage{}, apierrors.Unavailable("NEPSE returned no latest-session floorsheet rows, so the requested symbol-specific business date cannot be verified.")
		}
		return emptyFloorPage(page, true), nil
	}

	totalPages := first.TotalPages
	if totalPages < 0 {
		// A symbol filter must never silently return a partial session. Without totalPages, only an explicit totalElements equal to this short page proves that page 0 contains the complete session.
		if first.TotalElements < 0 || first.TotalElements != int64(len(first.Rows)) || len(first.Rows) >= FloorSheetPageSize {
			return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet omitted totalPages without a matching totalElements count"), "NEPSE did not report enough pagination information to verify a complete floorsheet session.")
		}
		totalPages = 1
	}
	if totalPages == 0 {
		if len(first.Rows) != 0 {
			return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet reported zero totalPages with non-empty content"), "NEPSE returned inconsistent floorsheet pagination metadata.")
		}
		return emptyFloorPage(page, true), nil
	}
	if first.TotalElements >= 0 {
		expectedPages := first.TotalElements / FloorSheetPageSize
		if first.TotalElements%FloorSheetPageSize != 0 {
			expectedPages++
		}
		if expectedPages != int64(totalPages) {
			return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet reports %d rows across %d pages; expected %d pages", first.TotalElements, totalPages, expectedPages), "NEPSE returned inconsistent floorsheet pagination metadata.")
		}
	}
	latestDate := firstBusinessDate([]FloorSheetPage{first})
	if latestDate == "" {
		return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet page 0 rows did not include businessDate"), "NEPSE did not include a usable business date, so the latest session cannot be verified.")
	}
	if !businessDate.IsZero() && businessDate.Format(time.DateOnly) != latestDate {
		return FloorSheetPage{}, apierrors.Unavailable("NEPSE only exposes the latest session's floorsheet; historical symbol-specific floorsheets are unavailable.")
	}
	maxPages := c.maxFloorPageCount()
	if totalPages > maxPages || (first.TotalElements >= 0 && first.TotalElements > int64(c.cfg.MaxFloorRows)) {
		return FloorSheetPage{}, apierrors.Unavailable("The latest NEPSE floorsheet exceeds this service's configured symbol-filter limit.")
	}

	pages := make([]FloorSheetPage, totalPages)
	pages[0] = first
	if err := c.loadRemainingFloorPages(ctx, pages); err != nil {
		return FloorSheetPage{}, err
	}

	filtered := make([]json.RawMessage, 0)
	seenContracts := make(map[string]struct{})
	if first.TotalElements > 0 {
		seenContracts = make(map[string]struct{}, int(first.TotalElements))
	}
	wanted := strings.ToUpper(strings.TrimSpace(symbol))
	for pageNo, floorPage := range pages {
		if pageNo > 0 && len(floorPage.Rows) == 0 {
			return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet page %d was empty before the reported final page", pageNo), "NEPSE returned an incomplete floorsheet session. Retry the request.").AsRetryable(time.Second)
		}
		if floorPage.TotalPages >= 0 && floorPage.TotalPages != totalPages {
			return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet page %d reported %d pages; page 0 reported %d", pageNo, floorPage.TotalPages, totalPages), "NEPSE floorsheet pagination changed while the session was being read. Retry the request.").AsRetryable(time.Second)
		}
		for _, row := range floorPage.Rows {
			rowSymbol, rowDate, contractID := floorRowFields(row)
			if rowDate == "" {
				return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet page %d contains a row without a valid businessDate", pageNo), "NEPSE returned a floorsheet row without a usable business date.")
			}
			if rowDate != latestDate {
				return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet page %d mixed session dates %q and %q", pageNo, latestDate, rowDate), "NEPSE floorsheet data changed while the session was being read. Retry the request.").AsRetryable(time.Second)
			}
			if rowSymbol == "" {
				return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet page %d contains a row without a stock symbol", pageNo), "NEPSE returned a floorsheet row without a usable security symbol.")
			}
			if contractID == "" {
				return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("all-market floorsheet page %d contains a row without a contractId", pageNo), "NEPSE returned a floorsheet row without a usable contract identifier.")
			}
			if _, duplicate := seenContracts[contractID]; duplicate {
				continue // a live session can shift page boundaries while being scanned
			}
			seenContracts[contractID] = struct{}{}
			if strings.EqualFold(rowSymbol, wanted) {
				filtered = append(filtered, row)
			}
		}
	}

	totalElements := int64(len(filtered))
	totalFilteredPages := (len(filtered) + FloorSheetPageSize - 1) / FloorSheetPageSize
	start := page * FloorSheetPageSize
	if start >= len(filtered) {
		return FloorSheetPage{Rows: []json.RawMessage{}, Page: page, TotalPages: totalFilteredPages, TotalElements: totalElements}, nil
	}
	end := min(start+FloorSheetPageSize, len(filtered))
	return FloorSheetPage{Rows: filtered[start:end], Page: page, TotalPages: totalFilteredPages, TotalElements: totalElements}, nil
}

func (c *Client) loadRemainingFloorPages(ctx context.Context, pages []FloorSheetPage) error {
	if len(pages) <= 1 {
		return nil
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error

	workers := min(floorScanWorkers, len(pages)-1)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pageNo := range jobs {
				result, err := c.marketFloorSheetPage(workCtx, pageNo)
				if err != nil {
					once.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				pages[pageNo] = result
			}
		}()
	}

sendJobs:
	for pageNo := 1; pageNo < len(pages); pageNo++ {
		select {
		case jobs <- pageNo:
		case <-workCtx.Done():
			break sendJobs
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// floorRowFields extracts the security symbol and business date from one upstream row. The JSON is decoded once because a symbol lookup may scan tens of thousands of rows from the latest session.
func floorRowFields(raw json.RawMessage) (symbol, businessDate, contractID string) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return "", "", ""
	}
	for _, key := range []string{"stockSymbol", "symbol"} {
		if value, ok := row[key]; ok && json.Unmarshal(value, &symbol) == nil {
			if symbol = strings.ToUpper(strings.TrimSpace(symbol)); symbol != "" {
				break
			}
		}
	}
	for _, key := range []string{"businessDate", "business_date"} {
		var value string
		if json.Unmarshal(row[key], &value) == nil {
			if businessDate = normalizeFloorDate(value); businessDate != "" {
				break
			}
		}
	}
	if rawID, ok := row["contractId"]; ok {
		contractID = string(bytes.TrimSpace(rawID))
		if unquoted, err := strconv.Unquote(contractID); err == nil {
			contractID = unquoted
		}
		contractID = strings.TrimSpace(contractID)
		if contractID == "null" {
			contractID = ""
		}
	}
	return symbol, businessDate, contractID
}

func firstBusinessDate(pages []FloorSheetPage) string {
	for _, page := range pages {
		for _, raw := range page.Rows {
			if _, date, _ := floorRowFields(raw); date != "" {
				return date
			}
		}
	}
	return ""
}

func normalizeFloorDate(value string) string {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.DateOnly, time.RFC3339Nano, "2006-01-02 15:04:05.999", "2006-01-02 15:04:05", "2006-01-02T15:04:05.999", "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format(time.DateOnly)
		}
	}
	return ""
}

// isEmptySheet reports the "no data" shapes NEPSE uses: [], {} and null.
func isEmptySheet(b []byte) bool {
	switch string(bytes.TrimSpace(b)) {
	case "[]", "{}", "null":
		return true
	}
	return false
}

// emptyFloorPage is a page without rows. totalKnown says whether upstream proves that there is nothing at all rather than just an empty page.
func emptyFloorPage(page int, totalKnown bool) FloorSheetPage {
	p := FloorSheetPage{Rows: []json.RawMessage{}, Page: page, TotalPages: -1, TotalElements: -1}
	if totalKnown {
		p.TotalPages, p.TotalElements = 0, 0
	}
	return p
}

// parseFloorEnvelope decodes one floorsheet page and validates its paging fields.
func parseFloorEnvelope(body []byte, page int) (FloorSheetPage, error) {
	var env floorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("floorsheet envelope: %w", err), "")
	}
	if env.Floorsheets == nil || env.Floorsheets.Content == nil {
		return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("floorsheet envelope lacks floorsheets.content"), "")
	}
	out := FloorSheetPage{Rows: env.Floorsheets.Content, Page: page, TotalPages: -1, TotalElements: -1}
	if n, present, ok := parseCount(env.Floorsheets.TotalPages); present {
		if !ok || n > int64(^uint(0)>>1) {
			return FloorSheetPage{}, apierrors.UpstreamInvalidResponse(fmt.Errorf("invalid totalPages %s", env.Floorsheets.TotalPages), "")
		}
		out.TotalPages = int(n)
	}
	// totalElements is informational. NEPSE has returned malformed values for this field, so ignore unusable values and leave the sentinel -1 rather than rejecting otherwise valid page content. totalPages remains strict because symbol-session scans rely on it to avoid returning partial data.
	if n, present, ok := parseCount(env.Floorsheets.TotalElements); present && ok {
		out.TotalElements = n
	}
	return out, nil
}
