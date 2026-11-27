// Package refs parses references to stage tasks, trees, schedules, projects,
// journal entries and sessions: the links the web UI copies (and their
// markdown form), the desktop's deep links (stage://tasks/12, one scheme per
// installation identity), "#12" and bare ids. A journal entry is its owner's
// task or tree link with ?entry=<id>. A harness's own session links are not
// parsed here: the connection's adapter resolves them
// (execution.LinkResolver). Agents pass whatever the user pasted; the host of
// a stage link is ignored because the same stage is reached at many addresses (loopback,
// tailnet, phone).
package refs

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Kind is what a reference names.
type Kind string

const (
	KindTask     Kind = "task"
	KindTree     Kind = "tree"
	KindSession  Kind = "session"
	KindSchedule Kind = "schedule"
	KindProject  Kind = "project"
	KindJournal  Kind = "journal"
)

// Ref is a parsed reference: TaskID for a task, TreeID for a tree,
// ScheduleID for a schedule, ProjectID for a project, EntryID and its owner's
// TaskID or TreeID for a journal entry, SessionID (the external session id;
// ConnectionID 0 when the reference names none) for a session.
type Ref struct {
	ConnectionID int64
	Kind         Kind
	TaskID       int64
	TreeID       int64
	ScheduleID   int64
	ProjectID    int64
	EntryID      int64
	SessionID    string
}

// ErrInvalid: the input names no task, tree, schedule, project, journal entry
// or session.
var ErrInvalid = errors.New("not a stage task, tree, schedule, project, journal entry or session reference: want a stage link (/tasks/<id>, /trees/<id>, /schedules/<id>, /projects/<id>, /tasks/<id>?entry=<id>, /sessions/<id>, stage://tasks/<id>), a harness session link, #<task id> or a session id")

// mdLink matches a whole markdown link, as "Copy link" produces.
var mdLink = regexp.MustCompile(`^\[.*\]\((\S+)\)$`)

var taskNum = regexp.MustCompile(`^#?([1-9][0-9]{0,17})$`)

// deepLink matches the desktop's deep-link schemes: "stage" and the
// developer identities' "stage-<suffix>" (docs/desktop.md).
var deepLink = regexp.MustCompile(`(?i)^stage(-[a-z0-9-]+)?://`)

// Parse reads one reference.
func Parse(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if m := mdLink.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "<"), ">")
	if s == "" {
		return Ref{}, ErrInvalid
	}
	if m := taskNum.FindStringSubmatch(s); m != nil {
		return taskRef(m[1])
	}
	lower := strings.ToLower(s)
	switch {
	case deepLink.MatchString(s):
		return parseDeepLink(s)
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return parseWebLink(s)
	case strings.HasPrefix(s, "/"):
		u, err := url.Parse(s)
		if err != nil {
			return Ref{}, ErrInvalid
		}
		if r, ok := stagePath(u); ok {
			return r, nil
		}
		return Ref{}, ErrInvalid
	}
	if strings.HasPrefix(s, "#") || strings.ContainsAny(s, " \t\r\n/") {
		return Ref{}, ErrInvalid
	}
	return Ref{Kind: KindSession, SessionID: s}, nil
}

func taskRef(digits string) (Ref, error) {
	id, err := positiveID(digits)
	if err != nil {
		return Ref{}, err
	}
	return Ref{Kind: KindTask, TaskID: id}, nil
}

func treeRef(digits string) (Ref, error) {
	id, err := positiveID(digits)
	if err != nil {
		return Ref{}, err
	}
	return Ref{Kind: KindTree, TreeID: id}, nil
}

func scheduleRef(digits string) (Ref, error) {
	id, err := positiveID(digits)
	if err != nil {
		return Ref{}, err
	}
	return Ref{Kind: KindSchedule, ScheduleID: id}, nil
}

func projectRef(digits string) (Ref, error) {
	id, err := positiveID(digits)
	if err != nil {
		return Ref{}, err
	}
	return Ref{Kind: KindProject, ProjectID: id}, nil
}

// ownerRef reads a task ("tasks") or tree ("trees") id, or a journal entry
// of it when u names one (?entry=<id>).
func ownerRef(kind, digits string, u *url.URL) (Ref, error) {
	r, err := taskRef(digits)
	if kind == "trees" {
		r, err = treeRef(digits)
	}
	q := u.Query()
	if err != nil || !q.Has("entry") {
		return r, err
	}
	id, err := positiveID(q.Get("entry"))
	if err != nil {
		return Ref{}, err
	}
	r.Kind, r.EntryID = KindJournal, id
	return r, nil
}

// positiveID reads an id above zero.
func positiveID(digits string) (int64, error) {
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrInvalid
	}
	return id, nil
}

// segments splits u's path into unescaped, non-empty segments.
func segments(u *url.URL) ([]string, bool) {
	var out []string
	for _, p := range strings.Split(u.EscapedPath(), "/") {
		if p == "" {
			continue
		}
		seg, err := url.PathUnescape(p)
		if err != nil {
			return nil, false
		}
		out = append(out, seg)
	}
	return out, true
}

// stagePath matches a stage page path ending in tasks/<id>, trees/<id>,
// schedules/<id>, projects/<id> or sessions/<id>; any prefix (a proxy
// sub-path), query (except a task or tree's entry) and fragment are ignored.
// Schedule and project paths must be the whole path, so common foreign links
// (github.com/orgs/x/projects/3) are not read as stage links.
func stagePath(u *url.URL) (Ref, bool) {
	segs, ok := segments(u)
	if !ok || len(segs) < 2 {
		return Ref{}, false
	}
	kind, id := segs[len(segs)-2], segs[len(segs)-1]
	switch kind {
	case "tasks", "trees":
		r, err := ownerRef(kind, id, u)
		return r, err == nil
	case "schedules":
		r, err := scheduleRef(id)
		return r, err == nil && len(segs) == 2
	case "projects":
		r, err := projectRef(id)
		return r, err == nil && len(segs) == 2
	case "sessions":
		if len(segs) >= 4 && segs[len(segs)-4] == "connections" {
			c, err := strconv.ParseInt(segs[len(segs)-3], 10, 64)
			if err != nil || c <= 0 {
				return Ref{}, false
			}
			return Ref{Kind: KindSession, ConnectionID: c, SessionID: id}, true
		}
		return Ref{Kind: KindSession, SessionID: id}, true
	}
	return Ref{}, false
}

// parseDeepLink reads stage://tasks/<id>, stage://trees/<id>,
// stage://schedules/<id>, stage://projects/<id> and stage://sessions/<id>
// (the host is the kind), or the same paths under stage://localhost/.
func parseDeepLink(s string) (Ref, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Ref{}, ErrInvalid
	}
	segs, ok := segments(u)
	if !ok {
		return Ref{}, ErrInvalid
	}
	if host := strings.ToLower(u.Host); host != "" && host != "localhost" {
		segs = append([]string{host}, segs...)
	}
	if len(segs) == 4 && segs[0] == "connections" && segs[2] == "sessions" {
		connection, err := strconv.ParseInt(segs[1], 10, 64)
		if err != nil || connection <= 0 {
			return Ref{}, ErrInvalid
		}
		return Ref{Kind: KindSession, ConnectionID: connection, SessionID: segs[3]}, nil
	}
	if len(segs) != 2 {
		return Ref{}, ErrInvalid
	}
	switch segs[0] {
	case "tasks", "trees":
		return ownerRef(segs[0], segs[1], u)
	case "schedules":
		return scheduleRef(segs[1])
	case "projects":
		return projectRef(segs[1])
	case "sessions":
		return Ref{Kind: KindSession, SessionID: segs[1]}, nil
	}
	return Ref{}, ErrInvalid
}

// parseWebLink reads a stage page link (any host).
func parseWebLink(s string) (Ref, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Ref{}, ErrInvalid
	}
	if r, ok := stagePath(u); ok {
		return r, nil
	}
	return Ref{}, ErrInvalid
}
