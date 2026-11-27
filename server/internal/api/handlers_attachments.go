package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
)

// uploadImage stores one staged image (multipart field "file"). The type is
// sniffed from the bytes; the client-sent type is ignored.
func (s *server) uploadImage(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, db.MaxAttachmentBytes+64<<10)
	file, header, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return db.Invalid("image is larger than 10 MB")
		}
		return db.Invalid("multipart field \"file\" is required: %v", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, db.MaxAttachmentBytes+1))
	if err != nil {
		return db.Invalid("read image: %v", err)
	}
	if len(data) == 0 {
		return db.Invalid("image is empty")
	}
	if len(data) > db.MaxAttachmentBytes {
		return db.Invalid("image is larger than 10 MB")
	}
	mime := http.DetectContentType(data)
	if !slices.Contains(db.AttachmentMimeTypes, mime) {
		return db.Invalid("unsupported image type %s: use PNG, JPEG, GIF or WebP", mime)
	}
	a, err := s.Store.CreateAttachment(r.Context(), imageName(header.Filename, mime), mime, data)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, a)
	return nil
}

// imageName is the upload's base name (≤120 chars), or image.<ext>.
func imageName(name, mime string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	if name == "" || name == "." || name == "/" {
		return "image." + strings.TrimPrefix(mime, "image/")
	}
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	return name
}

func (s *server) getAttachment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	a, err := s.Store.GetAttachment(r.Context(), id)
	if err != nil {
		return err
	}
	h := w.Header()
	h.Set("Content-Type", a.MimeType)
	h.Set("Content-Length", fmt.Sprint(len(a.Data)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if s.Access == nil {
		h.Set("Cache-Control", "private, max-age=31536000, immutable") // rows are never modified
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.Data)
	return nil
}

func (s *server) deleteAttachment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteAttachment(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// attachmentIDs validates an attachmentIds list (≤8, positive).
func attachmentIDs(ids []int64) error {
	if len(ids) > db.MaxTaskAttachments {
		return db.Invalid("at most %d images", db.MaxTaskAttachments)
	}
	for _, id := range ids {
		if id <= 0 {
			return db.Invalid("invalid attachment id %d", id)
		}
	}
	return nil
}
