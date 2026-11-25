package claudecode_test

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// An image's file name is its index, a safe form of its name and the
// extension of its type, whatever the name claims.
func TestImageFileName(t *testing.T) {
	long := strings.Repeat("a", 100) + ".png"
	for _, c := range []struct{ name, mime, want string }{
		{"error.png", "image/png", "3-error.png"},
		{"photo.PNG", "image/jpeg", "3-photo.jpg"},
		{"anim", "image/gif", "3-anim.gif"},
		{"a/b/../shot.webp", "image/webp", "3-shot.webp"},
		{"Bildschirmfoto über 2026.png", "image/png", "3-Bildschirmfoto-ber-2026.png"},
		{"日本語.png", "image/png", "3-image.png"},
		{"", "image/png", "3-image.png"},
		{"..png", "image/png", "3-image.png"},
		{long, "image/png", "3-" + strings.Repeat("a", 60) + ".png"},
	} {
		got, err := claudecode.ImageFileName(3, execution.Image{Name: c.name, MimeType: c.mime})
		if err != nil || got != c.want {
			t.Errorf("%q %s: %q %v, want %q", c.name, c.mime, got, err, c.want)
		}
	}
	if _, err := claudecode.ImageFileName(1, execution.Image{Name: "a.svg", MimeType: "image/svg+xml"}); err == nil {
		t.Error("svg accepted")
	}
}
