package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// writeFileAtomic writes b to path (mode perm) through a temp file and a
// rename, creating the directory (dirPerm) if needed.
func writeFileAtomic(path string, b []byte, perm, dirPerm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(b)
	if werr == nil {
		werr = f.Chmod(perm)
	}
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
	}
	return werr
}

// tailLines is the last n lines of path (nil when unreadable).
func tailLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	const max = 256 << 10
	if st, err := f.Stat(); err == nil && st.Size() > max {
		_, _ = f.Seek(-max, io.SeekEnd)
	}
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		out = append(out, sc.Text())
		if len(out) > n {
			out = out[1:]
		}
	}
	return out
}

// tailFile writes the last n lines of path to w and, with follow, keeps
// writing appended data until ctx ends, reopening the file when it is
// rotated (replaced or truncated).
func tailFile(ctx context.Context, path string, w io.Writer, n int, follow bool, poll time.Duration) error {
	for _, l := range tailLines(path, n) {
		if _, err := io.WriteString(w, l+"\n"); err != nil {
			return err
		}
	}
	if !follow {
		return nil
	}
	f, err := os.Open(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if f != nil {
		_, _ = f.Seek(0, io.SeekEnd)
	}
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()
	buf := make([]byte, 32<<10)
	for {
		if f != nil {
			for {
				k, err := f.Read(buf)
				if k > 0 {
					if _, werr := w.Write(buf[:k]); werr != nil {
						return werr
					}
				}
				if err != nil {
					break
				}
			}
			// Rotated: the path names another file, or this one shrank.
			cur, cerr := f.Stat()
			st, serr := os.Stat(path)
			pos, _ := f.Seek(0, io.SeekCurrent)
			if serr == nil && cerr == nil && (!os.SameFile(cur, st) || st.Size() < pos) {
				_ = f.Close()
				f = nil
			}
		}
		if f == nil {
			if nf, err := os.Open(path); err == nil {
				f = nf
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
	}
}

// readMaybe reads path; a missing file is (nil, false, nil).
func readMaybe(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}
