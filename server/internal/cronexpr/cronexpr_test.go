package cronexpr

import (
	"testing"
	"time"
)

func TestNextInZone(t *testing.T) {
	spec, err := Parse("0 2 * * *", "Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-11-11 10:00 UTC = 15:30 IST: next 02:00 IST (Nov 12) is 2026-11-11 20:30 UTC.
	got := spec.Next(time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 11, 11, 20, 30, 0, 0, time.UTC); !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("next = %v, want %v (UTC)", got, want)
	}
}

func TestNextAcrossDST(t *testing.T) {
	spec, err := Parse("0 9 * * *", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 09:00 EST on Mar 7 is 14:00 UTC; after the Mar 8 spring-forward 09:00 EDT is 13:00 UTC.
	first := spec.Next(time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 3, 7, 14, 0, 0, 0, time.UTC); !first.Equal(want) {
		t.Fatalf("first = %v, want %v", first, want)
	}
	second := spec.Next(first)
	if want := time.Date(2026, 3, 8, 13, 0, 0, 0, time.UTC); !second.Equal(want) {
		t.Fatalf("second = %v, want %v", second, want)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct{ expr, tz string }{
		{"@every 5m", "UTC"},
		{"CRON_TZ=UTC 0 2 * * *", "UTC"},
		{"TZ=UTC 0 2 * * *", "UTC"},
		{"61 * * * *", "UTC"},
		{"0 2 * *", "UTC"},
		{"", "UTC"},
		{"0 0 30 2 *", "UTC"}, // never fires
		{"0 2 * * *", ""},
		{"0 2 * * *", "Local"},
		{"0 2 * * *", "Mars/Olympus"},
	}
	for _, c := range cases {
		if _, err := Parse(c.expr, c.tz); err == nil {
			t.Errorf("Parse(%q, %q) = nil error", c.expr, c.tz)
		}
	}
	for _, expr := range []string{"@daily", "*/5 * * * *", "30 9 * * 1-5"} {
		if _, err := Parse(expr, "UTC"); err != nil {
			t.Errorf("Parse(%q) = %v", expr, err)
		}
	}
}
