//go:build !darwin && !linux

package alerts

// OpenSettings is unavailable on this system: false.
func OpenSettings(bundleID string, open func(string) error) (bool, error) {
	return false, nil
}
