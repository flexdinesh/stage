package install

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// preflight refuses to overwrite files the installer does not own: the
// CLI link and, for the Linux desktop, its desktop entry.
func preflight(d Deps, l Layout, prior *Record) error {
	fp, err := fingerprint(l.Link)
	if err != nil {
		return permErr(err, l.Link)
	}
	if !linkOK(fp, l, prior) {
		what := "a file"
		if fp.Kind == "symlink" {
			what = "a symlink to " + fp.Target
		}
		return newErr(CodeUnsupported,
			fmt.Sprintf("remove it if it is an old Stage CLI (for example from `mise run install-cli`), or install the CLI under another name with --cli-name NAME; nothing was changed"),
			"%s is %s that this installer did not create", l.Link, what)
	}
	if l.Entry != "" {
		fp, err := fingerprint(l.Entry)
		if err != nil {
			return permErr(err, l.Entry)
		}
		if fp.Kind != "" {
			if o, ok := prior.owned(l.Entry); !ok || !same(o, fp) {
				fix := "move it away, then retry; nothing was changed"
				if data, err := os.ReadFile(l.Entry); err == nil && bytes.Contains(data, []byte("/.local/"+l.Slug+".app/")) {
					fix = fmt.Sprintf("it belongs to MyGo's per-user installer: run `sh %s-install.sh --uninstall` (or delete ~/.local/%s.app and the entry), then retry; nothing was changed", l.Slug, l.Slug)
				}
				return newErr(CodeUnsupported, fix, "%s exists and was not created by this installer", l.Entry)
			}
		}
	}
	return nil
}

func linkOK(fp Owned, l Layout, prior *Record) bool {
	switch {
	case fp.Kind == "":
		return true
	case fp.Kind == "symlink" && fp.Target == l.CLI:
		return true
	}
	o, ok := prior.owned(l.Link)
	return ok && same(o, fp)
}

// register points the CLI link at the app and registers the desktop entry
// (URL scheme, icon) or, on macOS, the bundle. It returns the files it owns.
func register(ctx context.Context, d Deps, l Layout, res *Result) ([]Owned, error) {
	var owned []Owned
	if err := symlinkAtomic(l.CLI, l.Link); err != nil {
		return nil, permErr(err, l.Link)
	}
	owned = append(owned, Owned{Path: l.Link, Kind: "symlink", Target: l.CLI})
	if l.Mode != ModeDesktop {
		return owned, nil
	}
	switch d.GOOS {
	case "linux":
		src := filepath.Join(l.App, l.Slug+".desktop")
		data, err := os.ReadFile(src)
		if err != nil {
			return owned, fmt.Errorf("desktop entry: %w", err)
		}
		entry := desktopEntry(data, l)
		if err := writeFileAtomic(l.Entry, entry, 0o644); err != nil {
			return owned, permErr(err, l.Entry)
		}
		owned = append(owned, Owned{Path: l.Entry, Kind: "file", SHA256: bytesSHA256(entry)})
		refreshDesktopDB(ctx, d, l)
		if scheme := schemeOf(entry); scheme != "" {
			tryRun(ctx, d, "xdg-mime", "default", filepath.Base(l.Entry), "x-scheme-handler/"+scheme)
		}
	case "darwin":
		tryRun(ctx, d, "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", "-f", l.App)
	}
	return owned, nil
}

// desktopEntry rewrites MyGo's desktop entry for the installed location:
// Exec and Icon become absolute paths into the app directory (as MyGo's
// install.sh does), so the entry survives updates unchanged.
func desktopEntry(data []byte, l Layout) []byte {
	var b strings.Builder
	icon := filepath.Join(l.App, l.Slug+".png")
	_, iconErr := os.Stat(icon)
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "Exec="):
			args := strings.TrimPrefix(strings.TrimPrefix(line, "Exec="), l.Slug)
			line = "Exec=" + quoteExec(l.Desktop) + args
		case strings.HasPrefix(line, "TryExec="):
			line = "TryExec=" + l.Desktop
		case strings.HasPrefix(line, "Icon="):
			if iconErr != nil {
				continue
			}
			line = "Icon=" + icon
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// quoteExec quotes a path for a desktop entry's Exec key.
func quoteExec(p string) string {
	if !strings.ContainsAny(p, " \t\"'\\$`") {
		return p
	}
	r := strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", "\\\\`", "$", `\\$`)
	return `"` + r.Replace(p) + `"`
}

func schemeOf(entry []byte) string {
	for _, line := range strings.Split(string(entry), "\n") {
		if v, ok := strings.CutPrefix(line, "MimeType="); ok {
			for _, t := range strings.Split(v, ";") {
				if s, ok := strings.CutPrefix(t, "x-scheme-handler/"); ok {
					return s
				}
			}
		}
	}
	return ""
}

func refreshDesktopDB(ctx context.Context, d Deps, l Layout) {
	if l.Entry != "" {
		tryRun(ctx, d, "update-desktop-database", filepath.Dir(l.Entry))
	}
}

// tryRun runs an optional helper; a missing or failing one is ignored.
func tryRun(ctx context.Context, d Deps, name string, args ...string) {
	if !filepath.IsAbs(name) {
		p, err := d.LookPath(name)
		if err != nil {
			return
		}
		name = p
	} else if _, err := os.Stat(name); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, _ = d.Run(ctx, nil, name, args...)
}

// hasWebKit reports whether WebKitGTK 4.1 is in the linker cache.
func hasWebKit(ctx context.Context, d Deps) bool {
	for _, ldconfig := range []string{"/sbin/ldconfig", "/usr/sbin/ldconfig", "ldconfig"} {
		name := ldconfig
		if !filepath.IsAbs(name) {
			p, err := d.LookPath(name)
			if err != nil {
				continue
			}
			name = p
		} else if _, err := os.Stat(name); err != nil {
			continue
		}
		out, err := d.Run(ctx, nil, name, "-p")
		if err != nil {
			continue
		}
		return bytes.Contains(out, []byte("libwebkit2gtk-4.1.so.0"))
	}
	return false
}

// webkitFix is the install command for this distribution (never run).
func webkitFix(d Deps) string {
	cmds := []struct{ tool, cmd string }{
		{"apt-get", "sudo apt-get install libwebkit2gtk-4.1-0 libgtk-3-0"},
		{"dnf", "sudo dnf install webkit2gtk4.1 gtk3"},
		{"pacman", "sudo pacman -S webkit2gtk-4.1 gtk3"},
		{"zypper", "sudo zypper install libwebkit2gtk-4_1-0 gtk3"},
	}
	for _, c := range cmds {
		if _, err := d.LookPath(c.tool); err == nil {
			return fmt.Sprintf("install them with `%s`, then retry; or install the CLI/server only with --headless", c.cmd)
		}
	}
	return "install WebKitGTK 4.1 and GTK 3 with your package manager, then retry; or install the CLI/server only with --headless"
}
