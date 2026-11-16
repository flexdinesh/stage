package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// tarFile is one regular file of a generated archive.
type tarFile struct {
	Name string
	Mode int64
	Src  string // path on disk
}

// writeTarGz writes a reproducible tar.gz: the given directories and
// files in order, owner 0, the same mtime everywhere and no gzip name or
// time.
func writeTarGz(out string, dirs []string, files []tarFile, mtime time.Time) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		f.Close()
		return err
	}
	tw := tar.NewWriter(gz)
	err = func() error {
		for _, d := range dirs {
			if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: strings.TrimSuffix(d, "/") + "/", Mode: 0o755, ModTime: mtime, Format: tar.FormatPAX}); err != nil {
				return err
			}
		}
		for _, file := range files {
			if err := addFile(tw, file.Name, file.Src, file.Mode, mtime); err != nil {
				return err
			}
		}
		return nil
	}()
	for _, c := range []io.Closer{tw, gz, f} {
		if cerr := c.Close(); err == nil {
			err = cerr
		}
	}
	return err
}

func addFile(tw *tar.Writer, name, src string, mode int64, mtime time.Time) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: st.Size(), ModTime: mtime, Format: tar.FormatPAX}); err != nil {
		return err
	}
	_, err = io.Copy(tw, in)
	return err
}

// packTree archives the directory src under prefix (a macOS .app): its
// directories, files (modes kept, 755 or 644) and symlinks, in lexical
// order, reproducibly.
func packTree(out, src, prefix string, mtime time.Time) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		f.Close()
		return err
	}
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		name := path.Join(prefix, filepath.ToSlash(rel))
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o755, ModTime: mtime, Format: tar.FormatPAX})
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: target, Mode: 0o755, ModTime: mtime, Format: tar.FormatPAX})
		case info.Mode().IsRegular():
			mode := int64(0o644)
			if info.Mode()&0o111 != 0 {
				mode = 0o755
			}
			return addFile(tw, name, p, mode, mtime)
		}
		return fmt.Errorf("%s: unsupported file type %s", p, info.Mode().Type())
	})
	for _, c := range []io.Closer{tw, gz, f} {
		if cerr := c.Close(); err == nil {
			err = cerr
		}
	}
	return err
}

// maxEntry caps one archive member read into memory (binaries are ~30 MB).
const maxEntry = 512 << 20

// walkTarGz calls fn for every member of a tar.gz with its cleaned name
// (no leading "./" or "/"). Members are read lazily: fn reads body if it
// needs the content.
func walkTarGz(r io.Reader, fn func(name string, h *tar.Header, body io.Reader) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		if err := fn(name, h, io.LimitReader(tr, maxEntry)); err != nil {
			return err
		}
	}
}

// arMembers reads a Debian package (an ar archive) into name -> content.
func arMembers(data []byte) (map[string][]byte, error) {
	const magic = "!<arch>\n"
	if !bytes.HasPrefix(data, []byte(magic)) {
		return nil, errors.New("not an ar archive")
	}
	out := map[string][]byte{}
	for off := len(magic); off < len(data); {
		if off+60 > len(data) {
			return nil, errors.New("truncated ar header")
		}
		h := data[off : off+60]
		name := strings.TrimSuffix(strings.TrimSpace(string(h[0:16])), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(h[48:58])), 10, 64)
		if err != nil || size < 0 {
			return nil, fmt.Errorf("ar member %q: bad size", name)
		}
		start := off + 60
		end := start + int(size)
		if end > len(data) {
			return nil, fmt.Errorf("ar member %q is truncated", name)
		}
		out[name] = data[start:end]
		off = end + int(size%2)
	}
	return out, nil
}
