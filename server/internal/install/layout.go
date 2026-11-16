package install

import (
	"fmt"
	"path/filepath"
	"regexp"
)

// Installation modes.
const (
	ModeDesktop  = "desktop"
	ModeHeadless = "headless"
)

// Platform is the machine an installation runs on (injectable for tests).
type Platform struct {
	GOOS, GOARCH string
	Home         string
	Getenv       func(string) string
}

func (p Platform) env(k string) string {
	if p.Getenv == nil {
		return ""
	}
	return p.Getenv(k)
}

// xdg is $key when absolute, else ~/def.
func (p Platform) xdg(key, def string) string {
	if v := p.env(key); filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(p.Home, def)
}

// Names are the per-identity names (docs/desktop.md#identities).
type Names struct {
	Identity   string
	AppName    string // Stage, Stage Dev
	Slug       string // <id>-desktop
	Identifier string // io.github.flexdinesh.<id>.desktop
}

// NamesFor derives the names of identity.
func NamesFor(identity string) Names {
	n := Names{Identity: identity, Slug: identity + "-desktop", Identifier: "io.github.flexdinesh." + identity + ".desktop"}
	switch identity {
	case "stage":
		n.AppName = "Stage"
	case "stage-dev":
		n.AppName = "Stage Dev"
	default:
		n.AppName = identity
	}
	return n
}

// Layout is where one installation lives (docs/install.md#layout).
type Layout struct {
	Names
	Mode      string `json:"mode"`
	Root      string `json:"root"`      // metadata, lock, staging; the headless app
	App       string `json:"app"`       // the directory swapped on update (stable path)
	Prev      string `json:"prev"`      // the previous version, kept for rollback
	CLI       string `json:"cli"`       // the stage binary inside App (the service's exe)
	Desktop   string `json:"desktop"`   // the desktop executable inside App ("" headless)
	Link      string `json:"link"`      // the CLI entrypoint on PATH
	Entry     string `json:"entry"`     // Linux desktop entry ("" otherwise)
	Record    string `json:"record"`    // install.json
	Lock      string `json:"lock"`      // install.lock
	Staging   string `json:"staging"`   // staging parent (same filesystem as App)
	Handler   string `json:"handler"`   // MyGo's Linux URL handler entry ("" otherwise)
	Autostart string `json:"autostart"` // MyGo's login item (Linux autostart entry or macOS 12 agent)
}

var cliNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// RootFor is the install root of identity.
func RootFor(p Platform, identity string) string {
	return filepath.Join(p.Home, ".local", "lib", identity)
}

// LayoutFor derives the layout of identity in mode with cliName (empty:
// the identity) on p.
func LayoutFor(p Platform, identity, mode, cliName string) (Layout, error) {
	if cliName == "" {
		cliName = identity
	}
	if !cliNameRe.MatchString(cliName) {
		return Layout{}, newErr(CodeUsage, "use letters, digits, '.', '_' or '-'", "invalid CLI name %q", cliName)
	}
	if p.Home == "" || !filepath.IsAbs(p.Home) {
		return Layout{}, newErr(CodeUnsupported, "set HOME", "no home directory")
	}
	n := NamesFor(identity)
	root := RootFor(p, identity)
	l := Layout{
		Names: n, Mode: mode, Root: root,
		Record:  filepath.Join(root, "install.json"),
		Lock:    filepath.Join(root, "install.lock"),
		Staging: filepath.Join(root, "staging"),
		Link:    filepath.Join(p.Home, ".local", "bin", cliName),
	}
	switch {
	case mode == ModeHeadless:
		l.App = filepath.Join(root, "app")
		l.CLI = filepath.Join(l.App, "bin", "stage")
	case mode == ModeDesktop && p.GOOS == "linux":
		l.App = filepath.Join(root, "app")
		l.CLI = filepath.Join(l.App, "bin", "stage")
		l.Desktop = filepath.Join(l.App, n.Slug)
		apps := filepath.Join(p.xdg("XDG_DATA_HOME", ".local/share"), "applications")
		l.Entry = filepath.Join(apps, n.Slug+".desktop")
		l.Handler = filepath.Join(apps, desktopName(n.Identifier)+".url-handler.desktop")
		l.Autostart = filepath.Join(p.xdg("XDG_CONFIG_HOME", ".config"), "autostart", desktopName(n.Identifier)+".desktop")
	case mode == ModeDesktop && p.GOOS == "darwin":
		l.App = filepath.Join(p.Home, "Applications", n.AppName+".app")
		l.CLI = filepath.Join(l.App, "Contents", "Resources", "bin", "stage")
		l.Desktop = filepath.Join(l.App, "Contents", "MacOS", n.AppName)
		l.Autostart = filepath.Join(p.Home, "Library", "LaunchAgents", n.Identifier+".plist")
	default:
		return Layout{}, newErr(CodeUnsupported, "", "mode %q is not supported on %s", mode, p.GOOS)
	}
	l.Prev = l.App + ".prev"
	return l, nil
}

// desktopName is MyGo's desktop file name for an app id.
func desktopName(id string) string {
	b := []byte(id)
	for i, r := range b {
		ok := r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !ok {
			b[i] = '-'
		}
	}
	return string(b)
}

// platformTarget is the target name of p (linux-amd64, darwin-arm64).
func platformTarget(p Platform) (string, error) {
	switch p.GOOS {
	case "linux", "darwin":
	default:
		return "", newErr(CodeUnsupported, "Stage supports Linux and macOS", "unsupported OS %s", p.GOOS)
	}
	switch p.GOARCH {
	case "amd64", "arm64":
	default:
		return "", newErr(CodeUnsupported, "Stage supports amd64 and arm64", "unsupported CPU %s", p.GOARCH)
	}
	return fmt.Sprintf("%s-%s", p.GOOS, p.GOARCH), nil
}
