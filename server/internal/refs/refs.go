// Package refs parses references to stage tasks and sessions: the links the
// web UI copies (and their markdown form), the desktop's deep links
// (stage://tasks/12, one scheme per installation identity), T3 web and app
// links, "#12" and bare ids. Agents pass whatever the user pasted; the host of a stage link
// is ignored because the same stage is reached at many addresses (loopback,
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
	KindTask    Kind = "task"
	KindSession Kind = "session"
)

// Ref is a parsed reference: TaskID for a task, ThreadID (T3 thread id) for
// a session.
type Ref struct {
	ConnectionID int64
	Kind         Kind
	TaskID       int64
	ThreadID     string
}

// ErrInvalid: the input names no task or session.
var ErrInvalid = errors.New("not a stage task or session reference: want a stage link (/tasks/<id>, /sessions/<id>, stage://tasks/<id>), a T3 thread link, #<task id> or a thread id")

// mdLink matches a whole markdown link, as "Copy link" produces.
var mdLink = regexp.MustCompile(`^\[.*\]\((\S+)\)$`)

var taskNum = regexp.MustCompile(`^#?([1-9][0-9]{0,17})$`)

// deepLink matches the desktop's deep-link schemes: "stage" and the
// developer identities' "stage-<suffix>" (docs/desktop.md).
var deepLink = regexp.MustCompile(`(?i)^stage(-[a-z0-9-]+)?://`)

// Parse reads one reference. t3PublicURL is the T3 web base that T3 thread
// links start with ("" disables matching them).
func Parse(s, t3PublicURL string) (Ref, error) {
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
	case strings.HasPrefix(lower, "t3code://"):
		return parseAppLink(s)
	case deepLink.MatchString(s):
		return parseDeepLink(s)
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return parseWebLink(s, t3PublicURL)
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
	return Ref{Kind: KindSession, ThreadID: s}, nil
}

func taskRef(digits string) (Ref, error) {
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id <= 0 {
		return Ref{}, ErrInvalid
	}
	return Ref{Kind: KindTask, TaskID: id}, nil
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

// stagePath matches a stage page path ending in tasks/<id> or sessions/<id>;
// any prefix (a proxy sub-path), query and fragment are ignored.
func stagePath(u *url.URL) (Ref, bool) {
	segs, ok := segments(u)
	if !ok || len(segs) < 2 {
		return Ref{}, false
	}
	kind, id := segs[len(segs)-2], segs[len(segs)-1]
	switch kind {
	case "tasks":
		r, err := taskRef(id)
		return r, err == nil
	case "sessions":
		if len(segs) >= 4 && segs[len(segs)-4] == "connections" {
			c, err := strconv.ParseInt(segs[len(segs)-3], 10, 64)
			if err != nil || c <= 0 {
				return Ref{}, false
			}
			return Ref{Kind: KindSession, ConnectionID: c, ThreadID: id}, true
		}
		return Ref{Kind: KindSession, ThreadID: id}, true
	}
	return Ref{}, false
}

// parseDeepLink reads stage://tasks/<id> and stage://sessions/<id> (the
// host is the kind), or the same paths under stage://localhost/.
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
	if len(segs) != 2 {
		return Ref{}, ErrInvalid
	}
	switch segs[0] {
	case "tasks":
		return taskRef(segs[1])
	case "sessions":
		return Ref{Kind: KindSession, ThreadID: segs[1]}, nil
	}
	return Ref{}, ErrInvalid
}

// parseAppLink reads t3code://threads/<environmentId>/<threadId>.
func parseAppLink(s string) (Ref, error) {
	u, err := url.Parse(s)
	if err != nil || u.Host != "threads" {
		return Ref{}, ErrInvalid
	}
	segs, ok := segments(u)
	if !ok || len(segs) != 2 {
		return Ref{}, ErrInvalid
	}
	return Ref{Kind: KindSession, ThreadID: segs[1]}, nil
}

// parseWebLink reads a stage page link (any host, checked first) or a T3
// web thread link (<t3PublicURL>/<environmentId>/<threadId>).
func parseWebLink(s, t3PublicURL string) (Ref, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Ref{}, ErrInvalid
	}
	if r, ok := stagePath(u); ok {
		return r, nil
	}
	if r, ok := t3Path(u, t3PublicURL); ok {
		return r, nil
	}
	return Ref{}, ErrInvalid
}

func t3Path(u *url.URL, t3PublicURL string) (Ref, bool) {
	if t3PublicURL == "" {
		return Ref{}, false
	}
	base, err := url.Parse(t3PublicURL)
	if err != nil || !strings.EqualFold(base.Scheme, u.Scheme) || !strings.EqualFold(base.Host, u.Host) {
		return Ref{}, false
	}
	baseSegs, ok := segments(base)
	if !ok {
		return Ref{}, false
	}
	segs, ok := segments(u)
	if !ok || len(segs) != len(baseSegs)+2 {
		return Ref{}, false
	}
	for i, b := range baseSegs {
		if segs[i] != b {
			return Ref{}, false
		}
	}
	return Ref{Kind: KindSession, ThreadID: segs[len(segs)-1]}, true
}
