package transfer

import (
	"fmt"
	"time"
)

// truncateUTC returns t at midnight UTC.
func truncateUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// DaysInRange returns partition names (YYYYMMDD) for each UTC day in
// [from, to), excluding today (UTC) and later — only sealed partitions
// are eligible for transfer. A zero `to` means "today". `now` is
// injected for testability; callers pass time.Now().
func DaysInRange(from, to, now time.Time) ([]string, error) {
	today := truncateUTC(now)
	from = truncateUTC(from)
	if to.IsZero() {
		to = today
	} else {
		to = truncateUTC(to)
	}
	if from.After(to) {
		return nil, fmt.Errorf("invalid range: from %s is after to %s", from.Format("20060102"), to.Format("20060102"))
	}
	bound := to
	if bound.After(today) {
		bound = today
	}
	var days []string
	for d := from; d.Before(bound); d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format("20060102"))
	}
	return days, nil
}
