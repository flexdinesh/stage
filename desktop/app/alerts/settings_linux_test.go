package alerts

import (
	"errors"
	"slices"
	"testing"
)

func TestLinuxSettingsCommand(t *testing.T) {
	path := func(bins ...string) func(string) (string, error) {
		return func(b string) (string, error) {
			if slices.Contains(bins, b) {
				return "/usr/bin/" + b, nil
			}
			return "", errors.New("not found")
		}
	}
	gcc := []string{"gnome-control-center", "notifications"}
	cases := []struct {
		desktop string
		look    func(string) (string, error)
		want    []string
	}{
		{"GNOME", path("gnome-control-center"), gcc},
		{"ubuntu:GNOME", path("gnome-control-center"), gcc},
		{"Unity", path("gnome-control-center"), gcc},
		{"gnome", path(), nil},
		{"KDE", path("systemsettings", "systemsettings5"), []string{"systemsettings", "kcm_notifications"}},
		{"KDE", path("systemsettings5"), []string{"systemsettings5", "kcm_notifications"}},
		{"kde", path(), nil},
		{"X-Cinnamon", path("gnome-control-center", "systemsettings"), nil},
		{"", path("gnome-control-center"), nil},
		{"Hyprland:KDE", path("systemsettings"), []string{"systemsettings", "kcm_notifications"}},
	}
	for _, c := range cases {
		if got := linuxSettingsCommand(c.desktop, c.look); !slices.Equal(got, c.want) {
			t.Errorf("linuxSettingsCommand(%q) = %v, want %v", c.desktop, got, c.want)
		}
	}
}
