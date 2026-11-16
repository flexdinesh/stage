package alerts

import (
	"os"
	"os/exec"
	"strings"
)

// OpenSettings starts the desktop's notification settings
// (linuxSettingsCommand for XDG_CURRENT_DESKTOP); false when the desktop
// has none Stage knows on PATH. bundleID and open are unused on Linux.
func OpenSettings(bundleID string, open func(string) error) (bool, error) {
	argv := linuxSettingsCommand(os.Getenv("XDG_CURRENT_DESKTOP"), exec.LookPath)
	if argv == nil {
		return false, nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return false, err
	}
	go func() { _ = cmd.Wait() }()
	return true, nil
}

// linuxSettingsCommand is the notification settings command for an
// XDG_CURRENT_DESKTOP value (colon-separated, any case): GNOME, Unity and
// Ubuntu gnome-control-center notifications; KDE systemsettings
// kcm_notifications (systemsettings5 on Plasma 5). Only a command on PATH
// counts; nil when none.
func linuxSettingsCommand(xdgDesktop string, lookPath func(string) (string, error)) []string {
	have := func(bin string) bool {
		_, err := lookPath(bin)
		return err == nil
	}
	for _, d := range strings.Split(xdgDesktop, ":") {
		switch strings.ToLower(strings.TrimSpace(d)) {
		case "gnome", "unity", "ubuntu":
			if have("gnome-control-center") {
				return []string{"gnome-control-center", "notifications"}
			}
		case "kde":
			for _, bin := range []string{"systemsettings", "systemsettings5"} {
				if have(bin) {
					return []string{bin, "kcm_notifications"}
				}
			}
		}
	}
	return nil
}
