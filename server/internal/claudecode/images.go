package claudecode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Images reach a background session as files: Stage writes a launch's images
// into its own folder under the workspace's .claude/stage-images, adds that
// folder with --add-dir (Claude reads it without a permission prompt) and
// lists the paths at the end of the prompt. Claude reads them with its Read
// tool. The folder is inside the workspace because Claude refuses an added
// folder it does not trust, and trust covers the folders below a trusted one
// (claude 2.1.332); a worktree session's cwd is its worktree, so the folder
// is added either way. A "*" .gitignore keeps the images out of Git.

// imageRetention is how long an attempt's image folder is kept. Claude keeps
// what it read in its transcript, so a resumed session does not need it.
const imageRetention = 30 * 24 * time.Hour

// ImageRoot is the folder of a workspace's launch images.
func ImageRoot(workspace string) string {
	return filepath.Join(workspace, ".claude", "stage-images")
}

// imageExt is the file extension of each image type Claude reads.
var imageExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// maxImageStem bounds the name part of an image file.
const maxImageStem = 60

// ImageDir is the folder of one launch's images.
func ImageDir(workspace, requestID string) string {
	return filepath.Join(ImageRoot(workspace), WorktreeName(requestID))
}

// ignoreAll is the image root's .gitignore: Git ignores the folder and
// everything in it.
const ignoreAll = "# Stage: task images for Claude Code launches\n*\n"

// prepareImageRoot creates the image root with its .gitignore.
func prepareImageRoot(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	p := filepath.Join(root, ".gitignore")
	if b, err := os.ReadFile(p); err == nil && string(b) == ignoreAll {
		return nil
	}
	return writeFile(p, []byte(ignoreAll))
}

// ImageFileName is image i's file name (1-based): its index, its name kept to
// letters, digits, '.', '_' and '-', and the extension of its type.
func ImageFileName(i int, img execution.Image) (string, error) {
	ext, ok := imageExt[img.MimeType]
	if !ok {
		return "", fmt.Errorf("Claude Code can't read %s images; attach PNG, JPEG, GIF or WebP", img.MimeType)
	}
	stem := strings.TrimSuffix(filepath.Base(img.Name), filepath.Ext(img.Name))
	var b strings.Builder
	for _, r := range stem {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-'):
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteRune('-')
		}
		if b.Len() >= maxImageStem {
			break
		}
	}
	s := strings.Trim(b.String(), ".-")
	if s == "" {
		s = "image"
	}
	return fmt.Sprintf("%d-%s%s", i, s, ext), nil
}

// checkImages validates an intent's images before anything runs.
func checkImages(imgs []execution.Image) error {
	for i, img := range imgs {
		if _, err := ImageFileName(i+1, img); err != nil {
			return err
		}
		if len(img.Data) == 0 {
			return fmt.Errorf("image %q is empty", img.Name)
		}
	}
	return nil
}

// writeImages writes a launch's images into dir (created 0700; files 0600,
// each written whole then renamed) and returns their paths in order. The
// names depend only on the images, so writing again rewrites the same files.
func writeImages(dir string, imgs []execution.Image) ([]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(imgs))
	for i, img := range imgs {
		name, err := ImageFileName(i+1, img)
		if err != nil {
			return nil, err
		}
		p := filepath.Join(dir, name)
		if err := writeFile(p, img.Data); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

func writeFile(p string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600)
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// ImagePrompt is the block appended to the prompt that names each image's
// file. Names are shown on one line each, without control characters.
func ImagePrompt(imgs []execution.Image, paths []string) string {
	var b strings.Builder
	b.WriteString("Images attached to this task (open each with the Read tool):")
	for i, p := range paths {
		name := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, imgs[i].Name)
		fmt.Fprintf(&b, "\n%d. %s: %s", i+1, strings.TrimSpace(name), p)
	}
	return b.String()
}

// pruneImages removes the attempt folders under root last changed before
// cutoff. Best effort: a launch never fails on it.
func pruneImages(root string, cutoff time.Time) error {
	es, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range es {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "stage-") {
			continue
		}
		fi, err := e.Info()
		if err != nil || !fi.ModTime().Before(cutoff) {
			continue
		}
		errs = append(errs, os.RemoveAll(filepath.Join(root, e.Name())))
	}
	return errors.Join(errs...)
}
