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
// into its own folder under Stage's image root (<data dir>/claude/images,
// never the user's repository; ADR 0007 decision 10), lets Claude read the
// root through a permission rule in the launch's settings (ReadRule; Claude
// reads it without a prompt) and lists the paths at the end of the prompt.
// Claude reads them with its Read tool. No --add-dir: Claude refuses an
// added folder it does not trust (ADR 0005 E8).

// imageRetention is how long an attempt's image folder is kept. Claude keeps
// what it read in its transcript, so a resumed session does not need it.
const imageRetention = 30 * 24 * time.Hour

// imageExt is the file extension of each image type Claude reads.
var imageExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// maxImageStem bounds the name part of an image file.
const maxImageStem = 60

// ImageDir is the folder of one launch's images under the image root.
func ImageDir(root, requestID string) string {
	return filepath.Join(root, WorktreeName(requestID))
}

// ReadRule is the permission rule that lets Claude read everything under
// root, an absolute folder ("//" starts an absolute path in a rule).
func ReadRule(root string) string {
	return "Read(/" + filepath.ToSlash(filepath.Clean(root)) + "/**)"
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

// legacyIgnore is the .gitignore earlier versions wrote in a project's
// image folder.
const legacyIgnore = "# Stage: task images for Claude Code launches\n*\n"

// LegacyImageRoot is where earlier versions wrote a project's launch images.
func LegacyImageRoot(project string) string {
	return filepath.Join(project, ".claude", "stage-images")
}

// removeLegacyImages removes what earlier versions wrote in project: the
// stage-* folders and Stage's .gitignore in its .claude/stage-images, then
// that folder if it is left empty. Nothing else under .claude is touched.
func removeLegacyImages(project string) error {
	root := LegacyImageRoot(project)
	es, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range es {
		p := filepath.Join(root, e.Name())
		switch {
		case e.IsDir() && strings.HasPrefix(e.Name(), "stage-"):
			errs = append(errs, os.RemoveAll(p))
		case e.Name() == ".gitignore" && e.Type().IsRegular():
			if b, err := os.ReadFile(p); err == nil && string(b) == legacyIgnore {
				errs = append(errs, os.Remove(p))
			}
		}
	}
	if left, err := os.ReadDir(root); err == nil && len(left) == 0 {
		errs = append(errs, os.Remove(root))
	}
	return errors.Join(errs...)
}
