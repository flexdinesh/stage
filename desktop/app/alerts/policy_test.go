package alerts

import (
	"slices"
	"testing"
	"time"
)

func ids(items []Item) []int64 {
	out := []int64{}
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestPlanCatchUp(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	ago := func(m int) time.Time { return now.Add(-time.Duration(m) * time.Minute) }
	items := []Item{
		{ID: 1, Tag: "task-1", CreatedAt: ago(20)},            // too old
		{ID: 2, Tag: "task-2", CreatedAt: ago(10)},            // replaced by 7
		{ID: 3, Tag: "task-3", CreatedAt: ago(9), Read: true}, // read
		{ID: 4, Tag: "", CreatedAt: ago(8)},                   // no group
		{ID: 5, Tag: "thread-a", CreatedAt: ago(7)},
		{ID: 6, Tag: "task-6", CreatedAt: ago(6)},
		{ID: 7, Tag: "task-2", CreatedAt: ago(5)},
		{ID: 8, Tag: "task-8", CreatedAt: ago(4)},
		{ID: 9, Tag: "task-9", CreatedAt: ago(15)}, // exactly the window
		{ID: 10, Tag: "task-10"},                   // unparsed time
	}
	show, summ := PlanCatchUp(items, now, 15*time.Minute, 3)
	if got := ids(show); !slices.Equal(got, []int64{7, 8, 9}) {
		t.Errorf("show = %v, want [7 8 9]", got)
	}
	if got := ids(summ); !slices.Equal(got, []int64{5, 6}) {
		t.Errorf("summarized = %v, want [5 6]", got)
	}
	show, summ = PlanCatchUp(items[:3], now, 15*time.Minute, 3)
	if got := ids(show); !slices.Equal(got, []int64{2}) || len(summ) != 0 {
		t.Errorf("few: show %v summarized %v", got, ids(summ))
	}
	show, summ = PlanCatchUp(nil, now, 15*time.Minute, 3)
	if len(show) != 0 || len(summ) != 0 {
		t.Errorf("empty: %v %v", show, summ)
	}
}

func TestTarget(t *testing.T) {
	known := map[string]string{
		"task-7":           "/sessions/thr%201#answer",
		"task-8":           "https://evil.example/x",
		"thread-a b":       "/sessions/a%20b/../../api/x",
		"notification-5":   "javascript:alert(1)",
		"notification-6":   "/tasks/6",
		"task-9":           "/tasks/9#steal",
		"thread-%2E%2E":    "//evil.example/",
		"summary":          "/?inbox",
		"test":             "/settings",
		"task-10":          "/tasks/10",
		"thread-x":         "/sessions/x#answer",
		"thread-../../etc": "/sessions/..",
	}
	cases := map[string]string{
		"task-7":           "/tasks/7",
		"task-8":           "/tasks/8",
		"thread-a b":       "/?inbox",
		"notification-5":   "/",
		"notification-6":   "/tasks/6",
		"task-9":           "/tasks/9",
		"thread-%2E%2E":    "/?inbox",
		"summary":          "/?inbox",
		"test":             "/settings",
		"task-10":          "/tasks/10",
		"thread-x":         "/?inbox",
		"thread-../../etc": "/?inbox",
		"task-0":           "/",
		"task-abc":         "/",
		"thread-":          "/?inbox",
		"thread-..":        "/?inbox",
		"unknown":          "/",
		"":                 "/",
		"thread-y":         "/?inbox",
	}
	for id, want := range cases {
		if got := Target(id, known); got != want {
			t.Errorf("Target(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 11, 16, 2, 48, 47, 123000000, time.UTC)
	for _, s := range []string{
		"2026-11-16T02:48:47.123Z",
		"2026-11-16T02:48:47.123+00:00",
		"2026-11-16 02:48:47.123+00",
		"2026-11-16 02:48:47.123",
		"2026-11-16T02:48:47.123",
	} {
		if got := parseTime(s); !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v", s, got)
		}
	}
	if got := parseTime("2026-11-16T02:48:47Z"); !got.Equal(want.Truncate(time.Second)) {
		t.Errorf("no fraction = %v", got)
	}
	if !parseTime("yesterday").IsZero() {
		t.Error("garbage parsed")
	}
}
