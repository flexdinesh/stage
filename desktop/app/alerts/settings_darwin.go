package alerts

import (
	"net/url"
	"strconv"
	"strings"
	"syscall"
)

// OpenSettings opens the app's pane of System Settings, Notifications, with
// open (the shell's OpenExternal): macOS 13+ the per-app pane for
// bundleID, macOS 12 the Notifications preference pane.
func OpenSettings(bundleID string, open func(string) error) (bool, error) {
	if err := open(settingsURL(macOSMajor(), bundleID)); err != nil {
		return false, err
	}
	return true, nil
}

// settingsURL is the Notifications settings URL for a macOS major version.
func settingsURL(major int, bundleID string) string {
	if major >= 13 && bundleID != "" {
		return "x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=" + url.QueryEscape(bundleID)
	}
	return "x-apple.systempreferences:com.apple.preference.notifications"
}

// macOSMajor is the macOS major version (kern.osproductversion), 0 when
// unknown.
func macOSMajor() int {
	v, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return 0
	}
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}
	return n
}
