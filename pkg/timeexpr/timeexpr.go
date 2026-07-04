// Package timeexpr resolves relative-or-absolute time expressions, e.g.
// "now-7d/d", against an injected reference time. It is a thin wrapper over
// github.com/timberio/go-datemath (Elasticsearch/Grafana "datemath"),
// normalizing the input grammar and shielding callers from parser panics.
package timeexpr

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	datemath "github.com/timberio/go-datemath"
)

// rfc3339Anchor matches a leading RFC3339 timestamp. The underlying datemath
// grammar requires `<date>||<math>` for an explicit-date anchor, whereas our
// grammar attaches math directly (`<date>-1d`); we use this to insert the `||`.
var rfc3339Anchor = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)

// Parse resolves a time expression against now.
//
// Grammar: <anchor>[+-<duration>][/<rounding>]
//   - anchor   (required): "now" or an RFC3339 date
//   - duration (optional): <int><unit>, e.g. -7d, +12h
//   - rounding (optional): /<unit>, truncates the result down to that unit
//
// Units: y (year) M (month) w (week) d (day) h (hour) m (minute) s (second).
// Rounding always truncates down, the week starts Monday, and all math is
// performed in UTC.
func Parse(expr string, now time.Time) (_ time.Time, err error) {
	s := strings.TrimSpace(expr)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time expression")
	}

	// Rewrite `<date><math>` into the `<date>||<math>` form the grammar wants.
	// "now"-anchored and bare-RFC3339 (no math) expressions already parse as-is.
	if !strings.HasPrefix(s, "now") && !strings.Contains(s, "||") {
		if anchor := rfc3339Anchor.FindString(s); anchor != "" && len(anchor) < len(s) {
			s = anchor + "||" + s[len(anchor):]
		}
	}

	// The datemath lexer can panic on some malformed input; never let that
	// crash the caller (this runs inside an HTTP handler).
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("invalid time expression %q: %v", expr, r)
		}
	}()

	t, perr := datemath.ParseAndEvaluate(s,
		datemath.WithNow(now),
		datemath.WithStartOfWeek(time.Monday),
		datemath.WithLocation(time.UTC),
	)
	if perr != nil {
		return time.Time{}, fmt.Errorf("invalid time expression %q: %w", expr, perr)
	}
	return t, nil
}
