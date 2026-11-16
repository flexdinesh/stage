package install

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxMember  = 1 << 30
	maxTotal   = 4 << 30
	maxMembers = 100000
)

// appTree maps an artifact's members into an app directory: strip is the
// member prefix that becomes the app root, rename maps members (after
// strip) to other paths, and cli is the stage binary's path in the app.
type appTree struct {
	strip  string
	rename map[string]string
	cli    string
}

func treeFor(a Artifact, p Platform, mode string) (appTree, error) {
	cli := a.CLI.Path
	switch {
	case mode == ModeHeadless:
		// stage-<v>-<os>-<arch>/{stage,LICENSE,README.md}
		dir := path.Dir(cli)
		if dir == "." || path.Base(cli) != "stage" {
			return appTree{}, newErr(CodeVerify, verifyFix, "unexpected CLI path %s in %s", cli, a.Name)
		}
		return appTree{strip: dir + "/", rename: map[string]string{"stage": "bin/stage"}, cli: "bin/stage"}, nil
	case p.GOOS == "darwin":
		// <Name>.app/Contents/Resources/bin/stage
		top, rest, ok := strings.Cut(cli, "/")
		if !ok || !strings.HasSuffix(top, ".app") {
			return appTree{}, newErr(CodeVerify, verifyFix, "unexpected CLI path %s in %s", cli, a.Name)
		}
		return appTree{strip: top + "/", cli: rest}, nil
	default:
		// MyGo's flat Linux archive: <slug>, bin/stage, <slug>.desktop
		return appTree{cli: cli}, nil
	}
}

// extractApp unpacks archive into dest (which must not exist) as an app
// tree, refusing absolute or escaping names, links leaving the tree and
// anything but directories, regular files and relative symlinks.
func extractApp(archive string, t appTree, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := os.Mkdir(dest, 0o755); err != nil {
		return err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(archive), err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for n := 0; ; n++ {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(archive), err)
		}
		if n >= maxMembers {
			return fmt.Errorf("%s has too many members", filepath.Base(archive))
		}
		name, err := memberName(h.Name)
		if err != nil {
			return err
		}
		if name == "" || name+"/" == t.strip {
			continue
		}
		if t.strip != "" {
			rest, ok := strings.CutPrefix(name, t.strip)
			if !ok {
				return fmt.Errorf("unexpected member %q outside %s", h.Name, strings.TrimSuffix(t.strip, "/"))
			}
			name = rest
		}
		if r, ok := t.rename[name]; ok {
			name = r
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if err := noLinkParents(dest, name); err != nil {
			return fmt.Errorf("member %q: %w", h.Name, err)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if h.Size < 0 || h.Size > maxMember {
				return fmt.Errorf("member %q is too large", h.Name)
			}
			total += h.Size
			if total > maxTotal {
				return fmt.Errorf("%s unpacks to more than %d bytes", filepath.Base(archive), int64(maxTotal))
			}
			if err := writeMember(target, tr, h); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := checkLink(name, h.Linkname); err != nil {
				return fmt.Errorf("member %q: %w", h.Name, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("member %q has unsupported type %q", h.Name, h.Typeflag)
		}
	}
	return nil
}

// memberName is a member's cleaned relative name, refusing absolute,
// escaping and control-character names.
func memberName(raw string) (string, error) {
	if strings.HasPrefix(raw, "/") || strings.Contains(raw, "\\") || strings.ContainsAny(raw, "\x00\n\r") {
		return "", fmt.Errorf("unsafe member %q", raw)
	}
	for _, seg := range strings.Split(raw, "/") {
		if seg == ".." {
			return "", fmt.Errorf("unsafe member %q", raw)
		}
	}
	c := path.Clean(raw)
	if c == "." {
		return "", nil
	}
	return c, nil
}

// noLinkParents refuses a member whose parent directories inside dest
// include a symlink (a chain of in-tree links could otherwise escape).
func noLinkParents(dest, name string) error {
	dir := dest
	parts := strings.Split(path.Dir(name), "/")
	for _, seg := range parts {
		if seg == "." {
			break
		}
		dir = filepath.Join(dir, seg)
		st, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path goes through the symlink %s", seg)
		}
	}
	return nil
}

// checkLink refuses absolute symlinks and ones resolving outside the tree.
func checkLink(name, target string) error {
	if target == "" || strings.HasPrefix(target, "/") || strings.ContainsAny(target, "\x00\n\r") {
		return fmt.Errorf("unsafe link target %q", target)
	}
	res := path.Clean(path.Join(path.Dir(name), target))
	if res == ".." || strings.HasPrefix(res, "../") {
		return fmt.Errorf("link target %q leaves the archive", target)
	}
	return nil
}

func writeMember(target string, r io.Reader, h *tar.Header) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if h.Mode&0o111 != 0 {
		mode = 0o755
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(r, h.Size))
	if err == nil && n != h.Size {
		err = fmt.Errorf("member %q is truncated", h.Name)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// extractFile copies one member (by exact cleaned name) of archive to out
// with mode 0755: `stage update` uses it to unpack the new CLI.
func extractFile(archive, member, out string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s has no %s", filepath.Base(archive), member)
		}
		if err != nil {
			return err
		}
		name, err := memberName(h.Name)
		if err != nil {
			return err
		}
		if name != member {
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size > maxMember {
			return fmt.Errorf("%s in %s is not a regular file", member, filepath.Base(archive))
		}
		return writeMember(out, tr, h)
	}
}
