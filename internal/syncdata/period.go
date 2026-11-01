package syncdata

import (
	"fmt"
	"time"
)

// PeriodBounds is the half-open window [start, end) for a leaderboard period.
// Bounds are calendar edges in Europe/Oslo. all is unbounded. today, week, and
// month include the current Oslo day, Monday-based week, and calendar month.
func PeriodBounds(period string, now time.Time) (start, end time.Time, unbounded bool, err error) {
	switch period {
	case "all":
		return time.Time{}, time.Time{}, true, nil
	case "today", "week", "month":
	default:
		return time.Time{}, time.Time{}, false, fmt.Errorf("period")
	}
	local := now.In(osloLoc)
	switch period {
	case "today":
		start = startOfDay(local)
		end = start.AddDate(0, 0, 1)
	case "week":
		start = startOfDay(local)
		wd := int(start.Weekday())
		if wd == 0 {
			wd = 7
		}
		start = start.AddDate(0, 0, -(wd - 1))
		end = start.AddDate(0, 0, 7)
	case "month":
		y, m, _ := local.Date()
		start = time.Date(y, m, 1, 0, 0, 0, 0, osloLoc)
		end = start.AddDate(0, 1, 0)
	}
	return start, end, false, nil
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
