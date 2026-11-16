package install

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Uninstall removes this identity's installation: the service definition,
// the app, and the links and entries it owns. Data, configuration, logs,
// database backups and desktop preferences are kept.
func Uninstall(ctx context.Context, d Deps) *Result {
	res := &Result{Notes: []string{}, Warnings: []string{}}
	err := uninstall(ctx, d, res)
	return finish(res, err)
}

func uninstall(ctx context.Context, d Deps, res *Result) error {
	root := RootFor(d.Platform, d.Identity)
	rec, err := readRecord(filepath.Join(root, "install.json"))
	if err != nil {
		return wrapErr(CodeFailed, err, "", "unreadable install record")
	}
	if rec == nil {
		return newErr(CodeUnsupported, "nothing to do; remove a Stage installed another way with its own tool", "no Stage installed by the installer here (%s)", filepath.Join(root, "install.json"))
	}
	l, err := LayoutFor(d.Platform, d.Identity, rec.Mode, rec.CLIName)
	if err != nil {
		return err
	}
	lk, err := lockRoot(l)
	if err != nil {
		return err
	}
	defer lk.Release()
	if rec, err = readRecord(l.Record); err != nil || rec == nil {
		return wrapErr(CodeFailed, err, "", "install record changed")
	}
	res.Version, res.Mode, res.App = rec.Version, rec.Mode, rec.App

	if l.Desktop != "" {
		if pids := d.Desktop.Running(l); len(pids) > 0 {
			if err := d.Desktop.Quit(ctx, pids); err != nil {
				return wrapErr(CodeFailed, err, fmt.Sprintf("quit %s yourself, then retry; nothing was removed", l.AppName), "could not quit %s", l.AppName)
			}
			res.Desktop = "quit"
		}
	}

	st := d.Service.Status(ctx)
	switch {
	case serviceOurs(st, l):
		rep := d.Service.Uninstall(ctx)
		switch rep.Code {
		case 0:
			res.Service = "removed"
		case 6:
			res.Service = "unavailable"
		default:
			return newErr(CodeFailed, rep.Fix, "could not remove the %s service: %s; nothing else was removed", d.Identity, rep.Message)
		}
	case st.Definition.Present:
		res.Service = "foreign"
		res.note("the %s service runs %s, not this install; it was left in place", d.Identity, st.Definition.Exe)
	default:
		res.Service = "not-installed"
	}

	for _, path := range []string{l.Link, l.Entry} {
		if path == "" {
			continue
		}
		fp, err := fingerprint(path)
		if err != nil || fp.Kind == "" {
			continue
		}
		if o, ok := rec.owned(path); (ok && same(o, fp)) || (path == l.Link && fp.Kind == "symlink" && fp.Target == l.CLI) {
			if err := os.Remove(path); err != nil {
				res.warn("could not remove %s: %v", path, err)
			}
		} else {
			res.note("kept %s: it changed since it was installed", path)
		}
	}
	// MyGo's runtime entries (URL handler, login item) that point into the app.
	for _, path := range []string{l.Handler, l.Autostart} {
		if path == "" {
			continue
		}
		if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, []byte(l.App)) {
			if err := os.Remove(path); err != nil {
				res.warn("could not remove %s: %v", path, err)
			}
		}
	}
	refreshDesktopDB(ctx, d, l)

	for _, path := range []string{l.App, l.Prev} {
		if err := os.RemoveAll(path); err != nil {
			return permErr(err, path)
		}
	}
	if err := os.RemoveAll(l.Root); err != nil {
		return permErr(err, l.Root)
	}
	res.OK = true
	res.Message = fmt.Sprintf("uninstalled %s %s; your data was kept", l.AppName, rec.Version)
	for _, p := range retained(d, l, st.DataDir, st.ServiceEnv.Path) {
		if _, err := os.Stat(p); err == nil {
			res.Retained = append(res.Retained, p)
		}
	}
	return nil
}

// retained lists what uninstall keeps (if present).
func retained(d Deps, l Layout, dataDir, serviceEnv string) []string {
	var out []string
	if dataDir != "" {
		out = append(out, dataDir)
	}
	if serviceEnv != "" {
		out = append(out, filepath.Dir(serviceEnv))
	}
	switch d.GOOS {
	case "darwin":
		out = append(out, filepath.Join(d.Home, "Library", "Logs", d.Identity),
			filepath.Join(d.Home, "Library", "Application Support", l.Identifier))
	default:
		out = append(out, filepath.Join(d.xdg("XDG_CONFIG_HOME", ".config"), l.Identifier))
	}
	return out
}
