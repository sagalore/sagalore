package syncdata

import (
	"testing"
	"time"
)

func TestFormatOsloWhen(t *testing.T) {
	// 2026-10-22 17:00 UTC = 19:00 CEST (UTC+2 still in effect until last Sunday of October)
	got := FormatOsloWhen("2026-10-22T17:00:00Z")
	want := "22 October 2026 19:00"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// Unix seconds for the same instant.
	got = FormatOsloWhen("1792688400")
	if got != want {
		t.Fatalf("unix got %q want %q", got, want)
	}
	if FormatOsloWhen("") != "" {
		t.Fatal("empty")
	}
}

func TestPeriodBounds(t *testing.T) {
	now := time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)
	start, end, unbounded, err := PeriodBounds("all", now)
	if err != nil || !unbounded || !start.IsZero() || !end.IsZero() {
		t.Fatalf("all %+v %+v %v %v", start, end, unbounded, err)
	}
	if _, _, _, err := PeriodBounds("year", now); err == nil {
		t.Fatal("expected invalid period")
	}
	start, end, unbounded, err = PeriodBounds("today", now)
	if err != nil || unbounded || start.Day() != 29 || start.Month() != time.October || start.Hour() != 0 {
		t.Fatalf("today %v %v %v", start, end, err)
	}
	if !end.Equal(start.AddDate(0, 0, 1)) || now.Before(start) || !now.Before(end) {
		t.Fatalf("today window %v %v", start, end)
	}
	start, end, _, err = PeriodBounds("week", now)
	if err != nil || start.Weekday() != time.Monday || !end.Equal(start.AddDate(0, 0, 7)) {
		t.Fatalf("week %v %v %v", start, end, err)
	}
	if now.Before(start) || !now.Before(end) {
		t.Fatalf("now outside week %v %v", start, end)
	}
	start, end, _, err = PeriodBounds("month", now)
	if err != nil || start.Day() != 1 || start.Month() != time.October || !end.Equal(start.AddDate(0, 1, 0)) {
		t.Fatalf("month %v %v %v", start, end, err)
	}
}
