// Package nav is the desktop app's URL policy (docs/desktop.md): which
// navigations the main window allows, what opens in the browser or another
// app, which windows may open, and how <identity>:// deep links map to app
// paths. It is pure Go, without MyGo, and table-tested.
package nav

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// AppOrigin is the origin of the app's own pages (bridge B).
const AppOrigin = "mygo://localhost"

// Action is what to do with a URL.
type Action int

// Actions.
const (
	// Deny: refuse it (and log).
	Deny Action = iota
	// Allow: let the page load it.
	Allow
	// External: open it in the default browser (http, https).
	External
	// OpenApp: hand it to the OS (t3code://), falling back to T3's web
	// page.
	OpenApp
	// InApp: show Path in the main window (a deep link or an app link
	// opened as a new window).
	InApp
	// Child: open it in a child window without bindings
	// (PageOptions.Untrusted): an attachment opened with target=_blank.
	Child
)

func (a Action) String() string {
	return [...]string{"deny", "allow", "external", "open-app", "in-app", "child"}[a]
}

// Decision is the policy's answer for one URL.
type Decision struct {
	Action Action
	// URL is the URL to load, open or hand over.
	URL string
	// Path is the app path for InApp.
	Path string
	// Reason explains a denial.
	Reason string
}

// Policy decides navigations for one app identity.
type Policy struct {
	// Identity is the deep-link scheme (`stage`, `stage-dev`).
	Identity string
	// DevOrigin is the dev server's origin (MYGO_DEV_URL) in a development
	// build, "" otherwise: pages load from there instead of AppOrigin.
	DevOrigin string
}

// DevOriginOf returns the origin (scheme://host[:port]) of a dev server
// URL, "" when it is not an http(s) URL.
func DevOriginOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

func origin(u *url.URL) string { return strings.ToLower(u.Scheme + "://" + u.Host) }

// own reports whether u is one of the app's pages.
func (p Policy) own(u *url.URL) bool {
	o := origin(u)
	return o == AppOrigin || (p.DevOrigin != "" && o == p.DevOrigin)
}

var attachmentPath = regexp.MustCompile(`^/api/attachments/[0-9]+$`)

// Navigate decides a main-frame navigation (Page.OnWillNavigate): the
// app's own pages load; web links open in the browser, T3 links in T3,
// deep links in the app; anything else is denied.
func (p Policy) Navigate(raw string) Decision {
	u, err := url.Parse(raw)
	if err != nil {
		return Decision{Action: Deny, URL: raw, Reason: "unparseable URL"}
	}
	if p.own(u) {
		return Decision{Action: Allow, URL: raw}
	}
	return p.foreign(u, raw)
}

// Open decides a window.open() or target=_blank request
// (Page.SetWindowOpenHandler): attachments open in an untrusted child
// window, other app pages in the main window, the rest as Navigate.
func (p Policy) Open(raw string) Decision {
	u, err := url.Parse(raw)
	if err != nil {
		return Decision{Action: Deny, URL: raw, Reason: "unparseable URL"}
	}
	if p.own(u) {
		q := u.Query()
		scoped := len(q) == 1 && len(q[target.AttachmentTarget]) == 1 && q.Get(target.AttachmentTarget) != ""
		if attachmentPath.MatchString(u.Path) && (u.RawQuery == "" || scoped) {
			return Decision{Action: Child, URL: raw}
		}
		return Decision{Action: InApp, Path: appPath(u), URL: raw}
	}
	return p.foreign(u, raw)
}

// ChildNavigate decides a navigation in an attachment window: it stays on
// its attachment; web links open in the browser; nothing else loads.
func (p Policy) ChildNavigate(current, raw string) Decision {
	if raw == current {
		return Decision{Action: Allow, URL: raw}
	}
	u, err := url.Parse(raw)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") && !p.own(u) {
		return Decision{Action: External, URL: raw}
	}
	return Decision{Action: Deny, URL: raw, Reason: "attachment windows do not navigate"}
}

// External decides a page's request to open a web page (Links.openExternal).
func (p Policy) External(raw string) Decision {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return Decision{Action: Deny, URL: raw, Reason: "only http(s) pages open in the browser"}
	}
	return Decision{Action: External, URL: raw}
}

// App decides a page's request to open an app link (Links.openApp): T3
// thread links go to the OS, deep links of this app to the app.
func (p Policy) App(raw string) Decision {
	u, err := url.Parse(raw)
	if err != nil {
		return Decision{Action: Deny, URL: raw, Reason: "unparseable URL"}
	}
	switch d := p.foreign(u, raw); d.Action {
	case OpenApp, InApp:
		return d
	}
	return Decision{Action: Deny, URL: raw, Reason: "not a T3 or Stage app link"}
}

func (p Policy) foreign(u *url.URL, raw string) Decision {
	switch scheme := strings.ToLower(u.Scheme); {
	case scheme == "http" || scheme == "https":
		if u.Host == "" || u.User != nil {
			return Decision{Action: Deny, URL: raw, Reason: "web link without host or with credentials"}
		}
		return Decision{Action: External, URL: raw}
	case scheme == "t3code":
		if _, _, ok := t3Thread(u); !ok {
			return Decision{Action: Deny, URL: raw, Reason: "not a t3code://threads/<env>/<id> link"}
		}
		return Decision{Action: OpenApp, URL: raw}
	case p.Identity != "" && scheme == strings.ToLower(p.Identity):
		path, ok := p.DeepLink(raw)
		if !ok {
			return Decision{Action: Deny, URL: raw, Reason: "unknown deep link"}
		}
		return Decision{Action: InApp, Path: path, URL: raw}
	}
	return Decision{Action: Deny, URL: raw, Reason: "scheme " + u.Scheme + " is not allowed"}
}

// appPath is u's path with its query and fragment.
func appPath(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		p += "#" + u.EscapedFragment()
	}
	return p
}

var taskNum = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

// DeepLink maps an <identity>:// link to an app path: tasks/<n>, trees/<n>,
// schedules/<n> and projects/<n> → the same path,
// connections/<c>/sessions/<id> → that path (id escaped), and the bare
// scheme → /. A task or tree link keeps a valid `entry=<n>` (a journal
// entry); any other query and the fragment are ignored. The kind may be the
// host (`stage://tasks/12`) or follow a `localhost` host
// (`stage://localhost/tasks/12`).
func (p Policy) DeepLink(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || p.Identity == "" || !strings.EqualFold(u.Scheme, p.Identity) || u.User != nil || u.Port() != "" {
		return "", false
	}
	var segs []string
	switch host := strings.ToLower(u.Hostname()); host {
	case "", "localhost":
	default:
		segs = append(segs, host)
	}
	// Split the escaped path, so an encoded slash stays in its segment.
	for _, s := range strings.Split(strings.Trim(u.EscapedPath(), "/"), "/") {
		if s == "" {
			continue
		}
		seg, err := url.PathUnescape(s)
		if err != nil {
			return "", false
		}
		segs = append(segs, seg)
	}
	switch {
	case len(segs) == 0:
		return "/", true
	case len(segs) == 2 && (segs[0] == "tasks" || segs[0] == "trees") && taskNum.MatchString(segs[1]):
		path := "/" + segs[0] + "/" + segs[1]
		if entry := u.Query().Get("entry"); taskNum.MatchString(entry) {
			path += "?entry=" + entry
		}
		return path, true
	case len(segs) == 2 && (segs[0] == "schedules" || segs[0] == "projects") && taskNum.MatchString(segs[1]):
		return "/" + segs[0] + "/" + segs[1], true
	case len(segs) == 4 && segs[0] == "connections" && taskNum.MatchString(segs[1]) && segs[2] == "sessions" &&
		segs[3] != "" && segs[3] != "." && segs[3] != "..":
		return "/connections/" + segs[1] + "/sessions/" + url.PathEscape(segs[3]), true
	}
	return "", false
}

// t3Thread parses t3code://threads/<env>/<id>.
func t3Thread(u *url.URL) (env, id string, ok bool) {
	if !strings.EqualFold(u.Scheme, "t3code") || !strings.EqualFold(u.Host, "threads") {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// T3WebURL is the T3 web page of a t3code://threads/<env>/<id> link:
// <publicURL>/<env>/<id>, as the server builds t3WebUrl.
func T3WebURL(publicURL, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	env, id, ok := t3Thread(u)
	if !ok {
		return "", errors.New("not a t3code://threads/<env>/<id> link")
	}
	base, err := url.Parse(strings.TrimRight(publicURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "", errors.New("T3 public URL is not an http(s) URL")
	}
	return base.String() + "/" + url.PathEscape(env) + "/" + url.PathEscape(id), nil
}

// AlertPath validates the app path a native notification click opens:
// /tasks/<n>, /trees/<n>, /schedules/<n>, /settings and /?inbox, exactly
// (no query or fragment; historical /sessions and #answer paths are
// rejected). It returns p unchanged when allowed.
func AlertPath(p string) (string, bool) {
	switch p {
	case "/settings", "/?inbox":
		return p, true
	}
	for _, prefix := range []string{"/tasks/", "/trees/", "/schedules/"} {
		if n, ok := strings.CutPrefix(p, prefix); ok && taskNum.MatchString(n) {
			return p, true
		}
	}
	return "", false
}
