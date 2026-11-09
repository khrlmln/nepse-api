// Package validation holds the input rules shared by the REST and MCP services, so both reject the same inputs with the same messages.
package validation

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	apierrors "nepse-api/src/shared/errors"
)

const (
	DefaultLimit = 50
	MaxLimit     = 500
	// MaxPage bounds the page number so (page-1)*limit can never overflow.
	MaxPage     = 100000
	maxQueryLen = 64
)

var symbolRE = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,19}$`)

// Symbol normalises (trim + upper-case) and validates a security symbol.
func Symbol(raw string) (string, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return "", apierrors.InvalidArgument("The symbol is required.")
	}
	if !symbolRE.MatchString(s) {
		return "", apierrors.InvalidArgument("The symbol may contain only letters, digits, '.', '_' and '-' (max 20 characters).")
	}
	return s, nil
}

// Date parses an optional YYYY-MM-DD date; empty input yields the zero time.
func Date(field, raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return time.Time{}, apierrors.InvalidArgument(fmt.Sprintf("%s must be a date formatted as YYYY-MM-DD.", field))
	}
	return t, nil
}

// DefaultHistoryDays is the default look-back of price history requests.
const DefaultHistoryDays = 365

// DateRange validates an optional start/end pair and applies the defaults (end = today, start = end - 365 days) BEFORE checking the order, so a start_date after the defaulted end_date is rejected instead of being sent upstream. today is the exchange's current date.
func DateRange(startRaw, endRaw string, today time.Time) (start, end time.Time, err error) {
	if start, err = Date("start_date", startRaw); err != nil {
		return
	}
	if end, err = Date("end_date", endRaw); err != nil {
		return
	}
	if end.IsZero() {
		end = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	}
	if start.IsZero() {
		start = end.AddDate(0, 0, -DefaultHistoryDays)
	}
	if start.After(end) {
		err = apierrors.InvalidArgument("start_date must not be after end_date.")
	}
	return
}

// Query validates a free-text search filter.
func Query(raw string) (string, error) {
	q := strings.TrimSpace(raw)
	if len(q) > maxQueryLen {
		return "", apierrors.InvalidArgument(fmt.Sprintf("The search query is limited to %d characters.", maxQueryLen))
	}
	return q, nil
}

// Page is a validated 1-based pagination request.
type Page struct{ Page, Limit int }

// NewPage applies defaults (page 1, limit 50) and hard bounds (limit <= 500).
func NewPage(page, limit int) (Page, error) {
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if page < 1 || page > MaxPage {
		return Page{}, apierrors.InvalidArgument(fmt.Sprintf("page must be between 1 and %d.", MaxPage))
	}
	if limit < 1 || limit > MaxLimit {
		return Page{}, apierrors.InvalidArgument(fmt.Sprintf("limit must be between 1 and %d.", MaxLimit))
	}
	return Page{Page: page, Limit: limit}, nil
}

// Paged is one page of a larger collection.
type Paged[T any] struct {
	Items []T
	Page  int
	Limit int
	Total int
}

// PageMeta is the pagination metadata both transports return next to a page.
type PageMeta struct {
	Page  int `json:"page"`
	Limit int `json:"limit"`
	Total int `json:"total"`
}

// Meta is the pagination metadata of this page.
func (p Paged[T]) Meta() PageMeta { return PageMeta{Page: p.Page, Limit: p.Limit, Total: p.Total} }

// Paginate slices items according to p. It is safe for any Page value: pages beyond the end yield an empty slice and the offset arithmetic cannot overflow.
func Paginate[T any](items []T, p Page) Paged[T] {
	page, limit := max(p.Page, 1), max(p.Limit, 1)
	start := len(items)
	if page-1 <= len(items)/limit { // then (page-1)*limit <= len(items): no overflow
		start = (page - 1) * limit
	}
	end := min(start+limit, len(items)) // start <= len(items) and limit is bounded by NewPage
	if end < start {                    // limit so large that start+limit overflowed
		end = len(items)
	}
	out := items[start:end]
	if out == nil {
		out = []T{} // encode as [] rather than null
	}
	return Paged[T]{Items: out, Page: page, Limit: limit, Total: len(items)}
}

// FloorPage validates a zero-based floorsheet page index.
func FloorPage(page int) (int, error) {
	if page < 0 {
		return 0, apierrors.InvalidArgument("page must be 0 or greater.")
	}
	return page, nil
}
