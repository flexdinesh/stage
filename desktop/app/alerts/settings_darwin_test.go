package alerts

import "testing"

func TestSettingsURL(t *testing.T) {
	cases := []struct {
		major  int
		bundle string
		want   string
	}{
		{14, "io.github.flexdinesh.stage.desktop", "x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=io.github.flexdinesh.stage.desktop"},
		{13, "io.github.flexdinesh.stage-dev.desktop", "x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=io.github.flexdinesh.stage-dev.desktop"},
		{12, "io.github.flexdinesh.stage.desktop", "x-apple.systempreferences:com.apple.preference.notifications"},
		{0, "io.github.flexdinesh.stage.desktop", "x-apple.systempreferences:com.apple.preference.notifications"},
		{14, "", "x-apple.systempreferences:com.apple.preference.notifications"},
	}
	for _, c := range cases {
		if got := settingsURL(c.major, c.bundle); got != c.want {
			t.Errorf("settingsURL(%d, %q) = %q, want %q", c.major, c.bundle, got, c.want)
		}
	}
}
