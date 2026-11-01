package syncdata

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var osloLoc *time.Location

func init() {
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		loc = time.FixedZone("CET", 3600)
	}
	osloLoc = loc
}

var englishMonths = [...]string{
	"", "January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

// FormatOsloWhen renders a completion time as "dd Month yyyy hh:mm" in Europe/Oslo.
// Accepts RFC3339 (API sync) or a unix-seconds string/int from the addon.
func FormatOsloWhen(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var t time.Time
	if unix, err := strconv.ParseInt(raw, 10, 64); err == nil && unix > 1_000_000_000 {
		t = time.Unix(unix, 0)
	} else if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		t = parsed
	} else if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		t = parsed
	} else {
		return ""
	}
	local := t.In(osloLoc)
	month := englishMonths[int(local.Month())]
	return fmt.Sprintf("%02d %s %d %02d:%02d", local.Day(), month, local.Year(), local.Hour(), local.Minute())
}
