package codexsetup

import "testing"

func TestWindowLabel(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	for _, c := range []struct {
		mins *int64
		want string
	}{
		{nil, "Primary limit"}, {n(0), "Primary limit"}, {n(300), "5h limit"}, {n(10080), "Weekly limit"},
		{n(2880), "2d limit"}, {n(60), "1h limit"}, {n(90), "90m limit"},
	} {
		if got := windowLabel(c.mins, "Primary limit"); got != c.want {
			t.Errorf("windowLabel(%v) = %q, want %q", c.mins, got, c.want)
		}
	}
}
