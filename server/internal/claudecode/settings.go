package claudecode

import (
	"encoding/json"
)

// launchSettings is a launch's one --settings value: the hook settings
// file (it carries the image root's ReadRule), else JSON with the
// ReadRule for a launch with images, else none. Hooks opened for another
// image root are left out of a launch with images, which polling follows
// instead.
func (a *Adapter) launchSettings(images bool) string {
	if a.hooks != nil && (!images || a.hooks.ImageRoot() == a.imageRoot) {
		return a.hooks.SettingsPath()
	}
	if !images {
		return ""
	}
	b, err := json.Marshal(settingsFile{Permissions: &permissionSettings{Allow: []string{ReadRule(a.imageRoot)}}})
	if err != nil {
		return ""
	}
	return string(b)
}
