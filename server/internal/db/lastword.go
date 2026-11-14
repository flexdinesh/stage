package db

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/t3"
)

// LastWordCheckedAt is the thread updated_at the last successful last-word
// check saw (nil = never checked). ErrNotFound if the thread is not mirrored.
func (s *Store) LastWordCheckedAt(ctx context.Context, threadID string) (*time.Time, error) {
	var at *time.Time
	err := s.pool.QueryRow(ctx, `SELECT last_word_checked_at FROM threads WHERE t3_thread_id = $1`, threadID).Scan(&at)
	return at, notFound(err)
}

// SetThreadLastWord records a last-word check at thread updated_at checked.
// r nil (no reply found) advances the marker and keeps the stored reply. A
// check older than the stored marker is ignored (concurrent sync loops).
func (s *Store) SetThreadLastWord(ctx context.Context, threadID string, checked time.Time, r *t3.Reply) error {
	if r == nil {
		_, err := s.pool.Exec(ctx, `
UPDATE threads SET last_word_checked_at = $2
WHERE t3_thread_id = $1 AND (last_word_checked_at IS NULL OR last_word_checked_at <= $2)`, threadID, checked)
		return err
	}
	var at *time.Time
	if !r.UpdatedAt.IsZero() {
		at = &r.UpdatedAt
	}
	_, err := s.pool.Exec(ctx, `
UPDATE threads SET last_word_text = $3, last_word_preview = $4, last_word_truncated = $5,
  last_word_run_id = $6, last_word_item_id = $7, last_word_at = $8, last_word_checked_at = $2
WHERE t3_thread_id = $1 AND (last_word_checked_at IS NULL OR last_word_checked_at <= $2)`,
		threadID, checked, r.Text, nullIfEmpty(PreviewText(r.Text)), r.Truncated,
		nullIfEmpty(r.RunID), nullIfEmpty(r.ItemID), at)
	return err
}

// GetThreadLastWord is the stored reply excerpt; nil when none has been read.
func (s *Store) GetThreadLastWord(ctx context.Context, threadID string) (*LastWord, error) {
	var (
		text *string
		lw   LastWord
	)
	err := s.pool.QueryRow(ctx, `
SELECT last_word_text, last_word_truncated, last_word_run_id, last_word_at
FROM threads WHERE t3_thread_id = $1`, threadID).Scan(&text, &lw.Truncated, &lw.RunID, &lw.At)
	if err != nil {
		return nil, notFound(err)
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
