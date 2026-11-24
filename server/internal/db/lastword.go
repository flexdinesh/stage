package db

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// LastWordCheckedAt is the thread updated_at the last successful last-word
// check saw (nil = never checked). ErrNotFound if the thread is not mirrored.
func (s *Store) LastWordCheckedAt(ctx context.Context, threadID string) (*time.Time, error) {
	return read(ctx, s, func(tx store.Tx) (*time.Time, error) { return tx.Threads().LastWordCheckedAt(ctx, threadID) })
}

// SetThreadLastWord records a last-word check at thread updated_at checked.
// r nil (no reply found) advances the marker and keeps the stored reply. A
// check older than the stored marker is ignored (concurrent sync loops).
func (s *Store) SetThreadLastWord(ctx context.Context, threadID string, checked time.Time, r *execution.Reply) error {
	var row *store.LastWordRow
	if r != nil {
		var at *time.Time
		if !r.UpdatedAt.IsZero() {
			at = &r.UpdatedAt
		}
		row = &store.LastWordRow{Text: r.Text, Preview: optionalText(PreviewText(r.Text)), Truncated: r.Truncated,
			RunID: optionalText(r.RunID), At: at}
	}
	return s.e.Write(ctx, func(tx store.Tx) error { return tx.Threads().SetLastWord(ctx, threadID, checked, row) })
}

// optionalText is s, or nil when empty (stored as NULL).
func optionalText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// GetThreadLastWord is the stored reply excerpt; nil when none has been read.
func (s *Store) GetThreadLastWord(ctx context.Context, threadID string) (*LastWord, error) {
	var (
		text *string
		lw   LastWord
	)
	err := s.e.Read(ctx, func(tx store.Tx) error {
		var err error
		text, lw, err = tx.Threads().LastWord(ctx, threadID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if text == nil {
		return nil, nil
	}
	lw.Text = *text
	return &lw, nil
}

// previewMaxRunes caps the list-row preview (rows clamp it to one line).
const previewMaxRunes = 240

var (
	mdFence     = regexp.MustCompile("^(```|~~~)")
	mdRule      = regexp.MustCompile(`^[\s|:*_=-]+$`)
	mdLineLead  = regexp.MustCompile(`^(#{1,6}\s+|>\s?|[-*+]\s+(\[[ xX]\]\s+)?|\d+[.)]\s+)+`)
	mdImage     = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink      = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	mdEmphasis  = regexp.MustCompile("(\\*\\*|__|~~|`)")
	mdTablePipe = regexp.MustCompile(`\s*\|\s*`)
	spaces      = regexp.MustCompile(`\s+`)
)

// PreviewText turns a markdown reply into one plain line: block markers,
// fences, rules, link/image syntax and emphasis marks dropped, whitespace
// collapsed, capped at previewMaxRunes. Single * and _ stay (identifiers).
func PreviewText(md string) string {
	var parts []string
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || mdFence.MatchString(line) || mdRule.MatchString(line) {
			continue
		}
		line = mdLineLead.ReplaceAllString(line, "")
		line = mdImage.ReplaceAllString(line, "$1")
		line = mdLink.ReplaceAllString(line, "$1")
		line = mdEmphasis.ReplaceAllString(line, "")
		if strings.HasPrefix(line, "|") || strings.HasSuffix(line, "|") {
			line = strings.Trim(mdTablePipe.ReplaceAllString(line, " "), " ")
		}
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	out := spaces.ReplaceAllString(strings.Join(parts, " "), " ")
	if r := []rune(out); len(r) > previewMaxRunes {
		out = strings.TrimSpace(string(r[:previewMaxRunes]))
	}
	return out
}
