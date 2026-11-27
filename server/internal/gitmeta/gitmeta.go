// Package gitmeta reads checkout metadata without running Git.
package gitmeta

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxConfig bounds each metadata read.
const maxConfig = 1 << 20

// Info describes the nearest Git checkout, including linked worktrees and
// submodules. A checkout can have a common directory without a remote.
type Info struct {
	Top       string
	CommonDir string
	Remote    string
	Subpath   string
}

// Inspect reads Git metadata for a canonical absolute workspace directory.
// The first .git entry governs the workspace, even if it is unreadable.
func Inspect(dir string) Info {
	if !filepath.IsAbs(dir) {
		return Info{}
	}
	for top := dir; ; top = filepath.Dir(top) {
		dotGit := filepath.Join(top, ".git")
		fi, err := os.Stat(dotGit)
		if err == nil {
			common := commonDir(dotGit, fi)
			if common == "" {
				return Info{}
			}
			rel, err := filepath.Rel(top, dir)
			if err != nil {
				return Info{}
			}
			b, _ := read(filepath.Join(common, "config"))
			return Info{Top: top, CommonDir: common, Remote: RemoteIdentity(originURL(b)), Subpath: filepath.ToSlash(rel)}
		}
		if !errors.Is(err, os.ErrNotExist) || top == filepath.Dir(top) {
			return Info{}
		}
		// A dangling .git symlink still shadows any outer checkout.
		if _, err := os.Lstat(dotGit); !errors.Is(err, os.ErrNotExist) {
			return Info{}
		}
	}
}

// read accepts small regular metadata files.
func read(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxConfig {
		return nil, errors.New("invalid Git metadata file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxConfig))
}

func commonDir(dotGit string, info os.FileInfo) string {
	gitDir := dotGit
	if !info.IsDir() {
		b, err := read(dotGit)
		if err != nil {
			return ""
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
		target = strings.TrimSpace(target)
		if !ok || target == "" {
			return ""
		}
		gitDir = resolve(filepath.Dir(dotGit), target)
	}
	// Resolve the gitdir first so that a relative commondir is interpreted
	// on the same filesystem path as the rest of its metadata.
	gitDir = canonicalDirectory(gitDir)
	if gitDir == "" {
		return ""
	}
	b, err := read(filepath.Join(gitDir, "commondir"))
	if errors.Is(err, os.ErrNotExist) {
		return gitDir
	}
	target := strings.TrimSpace(string(b))
	if err != nil || target == "" {
		return ""
	}
	return canonicalDirectory(resolve(gitDir, target))
}

// Main is the main root of the checkout whose top is top: the main working
// tree of a linked worktree, the common directory of a bare repository's
// linked worktree, else top itself. Like Claude Code, it trusts a linked
// worktree only when its admin directory sits under the common directory's
// worktrees and points back at this checkout.
func Main(top string) string {
	b, err := read(filepath.Join(top, ".git"))
	if err != nil {
		return top
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	target = strings.TrimSpace(target)
	if !ok || target == "" {
		return top
	}
	admin := resolve(top, target)
	b, err = read(filepath.Join(admin, "commondir"))
	common := strings.TrimSpace(string(b))
	if err != nil || common == "" {
		return top
	}
	common = resolve(admin, common)
	if filepath.Dir(admin) != filepath.Join(common, "worktrees") {
		return top
	}
	b, err = read(filepath.Join(admin, "gitdir"))
	back := strings.TrimSpace(string(b))
	if err != nil || back == "" {
		return top
	}
	self, err := filepath.EvalSymlinks(top)
	if err != nil {
		return top
	}
	if p, err := filepath.EvalSymlinks(resolve(admin, back)); err != nil || p != filepath.Join(self, ".git") {
		return top
	}
	main := filepath.Dir(common)
	if filepath.Base(common) != ".git" {
		if _, err := os.Lstat(filepath.Join(common, ".git")); err == nil {
			return top
		}
		main = common
	}
	if p, err := filepath.EvalSymlinks(main); err == nil {
		return p
	}
	return main
}

func canonicalDirectory(dir string) string {
	p, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ""
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		return ""
	}
	return p
}

func resolve(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// originURL is remote "origin"'s url in a git config file. Includes and
// insteadOf rewrites are ignored: an unresolved remote just yields less
// metadata.
func originURL(config []byte) string {
	in := false
	sc := bufio.NewScanner(bytes.NewReader(config))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			in = isOrigin(line)
			continue
		}
		if !in {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "url") {
			return strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return ""
}

func isOrigin(header string) bool {
	h, ok := strings.CutSuffix(strings.TrimPrefix(header, "["), "]")
	if !ok {
		return false
	}
	section, sub, ok := strings.Cut(strings.TrimSpace(h), " ")
	return ok && strings.EqualFold(section, "remote") && strings.TrimSpace(sub) == `"origin"`
}
