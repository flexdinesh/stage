// Package gitmeta reads repository metadata without a git binary (the
// release image has none). It is search metadata only: never an identity
// or proof that two checkouts are the same repository.
package gitmeta

import (
	"bufio"
	"bytes"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// maxConfig bounds a read of .git files; real ones are a few KiB.
const maxConfig = 1 << 20

// RemoteIdentity normalizes a remote URL to host/path ("github.com/o/r").
// Credentials, local directory and file remotes yield "".
func RemoteIdentity(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		host, path, ok := strings.Cut(raw, ":")
		if !ok || host == "" || strings.ContainsAny(host, "/\\") {
			return ""
		}
		raw = "ssh://" + host + "/" + path
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Path == "" || (u.Scheme != "ssh" && u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "git") {
		return ""
	}
	return strings.ToLower(u.Host) + strings.TrimSuffix(strings.TrimRight(u.Path, "/"), ".git")
}

// OriginRepo is the normalized origin remote of the checkout rooted at dir
// ("" when dir is no Git checkout, has no origin, or is unreadable). It
// follows a linked worktree's .git file and commondir to the shared config.
func OriginRepo(dir string) string {
	common, ok := commonDir(dir)
	if !ok {
		return ""
	}
	b, ok := read(filepath.Join(common, "config"))
	if !ok {
		return ""
	}
	return RemoteIdentity(originURL(b))
}

// Translate maps a harness-side directory under harnessRoot to the same
// directory under stageRoot (a declared shared path). It refuses a target
// whose symlinks resolve outside stageRoot.
func Translate(harnessRoot, stageRoot, dir string) (string, bool) {
	rel, ok := beneath(harnessRoot, dir)
	if !ok || stageRoot == "" {
		return "", false
	}
	root, err := filepath.EvalSymlinks(stageRoot)
	if err != nil {
		return "", false
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		return "", false
	}
	if _, ok = beneath(root, target); !ok {
		return "", false
	}
	return target, true
}

func beneath(root, dir string) (string, bool) {
	rel, err := filepath.Rel(root, dir)
	return rel, err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func read(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxConfig))
	return b, err == nil
}

func commonDir(dir string) (string, bool) {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", false
	}
	gitDir := dotGit
	if !info.IsDir() {
		b, ok := read(dotGit)
		if !ok {
			return "", false
		}
		target, found := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
		if !found {
			return "", false
		}
		gitDir = resolve(dir, strings.TrimSpace(target))
	}
	if b, ok := read(filepath.Join(gitDir, "commondir")); ok {
		return resolve(gitDir, strings.TrimSpace(string(b))), true
	}
	return gitDir, true
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
