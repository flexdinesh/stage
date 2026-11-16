package alerts

import (
	"cmp"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/desktop/app/nav"
)

// Item is one inbox row from the stream (`notification` event).
type Item struct {
	ID int64
	// Event: needs_input, done, failed or settled.
	Event     string
	CreatedAt time.Time
	Read      bool
	// Tag is the group (task-<n>, thread-<id>), "" for identity-less rows.
	Tag   string
	Title string
	Body  string
	// URL is the app path the server suggests (validated by Target).
	URL string
}

// Note tags with a fixed meaning. OS notification IDs are
// "<server id>/<tag>" (local/task-7, local/summary), except TestID.
const (
	// SummaryID is the tag of a server's "N more in Stage" catch-up
	// summary; it opens the inbox.
	SummaryID = "summary"
	// TestID is the confirmation and test notification; it opens Settings.
	TestID = "test"
)

// noteID is the notification tag of an item: its group tag (a newer row
// of the group replaces the shown one), notification-<id> without a tag.
func noteID(it Item) string {
	if it.Tag != "" {
		return it.Tag
	}
	return "notification-" + strconv.FormatInt(it.ID, 10)
}

// PlanCatchUp picks the catch-up notifications after a reconnect: unread
// rows created within window before now and with a group tag, the newest
// per tag; the max newest groups are shown, oldest first, the rest are
// summarized (oldest first).
func PlanCatchUp(items []Item, now time.Time, window time.Duration, max int) (show, summarized []Item) {
	newest := map[string]Item{}
	for _, it := range items {
		if it.Read || it.Tag == "" || it.CreatedAt.IsZero() || now.Sub(it.CreatedAt) > window {
			continue
		}
		if cur, ok := newest[it.Tag]; !ok || it.ID > cur.ID {
			newest[it.Tag] = it
		}
	}
	groups := make([]Item, 0, len(newest))
	for _, it := range newest {
		groups = append(groups, it)
	}
	// Newest first.
	slices.SortFunc(groups, func(a, b Item) int { return cmp.Compare(b.ID, a.ID) })
	if max < 0 {
		max = 0
	}
	if len(groups) > max {
		show, summarized = groups[:max], groups[max:]
	} else {
		show = groups
	}
	slices.Reverse(show)
	slices.Reverse(summarized)
	return show, summarized
}

// Target is the app path a click on notification id opens: its known URL
// (from the shown notification) when nav.AlertPath allows it, else the
// path derived from the ID (task-N → /tasks/N, thread-X → /sessions/X,
// summary → /?inbox, test → /settings), else /.
func Target(id string, known map[string]string) string {
	if u, ok := known[id]; ok {
		if p, ok := nav.AlertPath(u); ok {
			return p
		}
	}
	var derived string
	switch {
	case id == SummaryID:
		derived = "/?inbox"
	case id == TestID:
		derived = "/settings"
	case strings.HasPrefix(id, "task-"):
		derived = "/tasks/" + strings.TrimPrefix(id, "task-")
	case strings.HasPrefix(id, "thread-"):
		derived = "/sessions/" + url.PathEscape(strings.TrimPrefix(id, "thread-"))
	}
	if p, ok := nav.AlertPath(derived); ok {
		return p
	}
	return "/"
}

// isGroupTag reports whether id is a task- or thread- group (closed when
// its group has no unread rows).
func isGroupTag(id string) bool {
	return strings.HasPrefix(id, "task-") || strings.HasPrefix(id, "thread-")
}

// parseTime parses a stream createdAt (RFC 3339, with or without
// fraction; a space separator or no zone, read as UTC, are accepted).
// Unparseable is the zero time.
func parseTime(s string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999Z07",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05.999999999",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
