package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

const (
	statusPath = "/api/auth/status"
	healthPath = "/api/health"
	loginPath  = "/api/auth/login"
	logoutPath = "/api/auth/logout"
	cookieName = "stage_session"
)

// Status is the public account bootstrap information at a Stage server.
type Status struct {
	Mode           string `json:"mode"`
	CanonicalURL   string `json:"canonicalUrl"`
	InstallationID string `json:"installationId"`
}

func read(ctx context.Context, t target.Target, tr http.RoundTripper, path string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, t.BaseURL()+path, nil)
	if err != nil {
		return err
	}
	r.Host = t.Host()
	res, err := tr.RoundTrip(r)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("Stage could not verify the server at this address")
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(into)
}

// RoundTrip forwards a request to t. The caller has already validated the
// page origin, and tr must dial only t. An authenticated request always
// checks the installation before sending a cookie, even after a restart.
func (s *Store) RoundTrip(t target.Target, tr http.RoundTripper, r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Del("Cookie")
	r.Header.Del("Authorization")
	r.Header.Del("Origin")
	path := r.URL.Path
	if path == statusPath {
		return tr.RoundTrip(r)
	}
	if strings.HasPrefix(path, "/api/auth/") && path != loginPath && path != logoutPath {
		return denied(r, http.StatusForbidden, "Open account links in your browser."), nil
	}
	old := s.current(t)
	bound := old.Value != "" && old.Binding.Endpoint == endpoint(t)
	if path == logoutPath && r.Method == http.MethodPost && bound {
		defer func() { _ = s.replace(context.Background(), t, old, credential{}) }()
	}
	key, err := resolve(r.Context(), t, tr)
	var inaccessible *targetAccessError
	if err != nil && !errors.As(err, &inaccessible) {
		return nil, err
	}
	if bound && (old.Binding != key || old.ctx.Err() != nil) {
		if err := s.replace(r.Context(), t, old, credential{}); err != nil {
			if errors.Is(err, errChanged) && r.Context().Err() == nil {
				return denied(r, http.StatusUnauthorized, "Sign in to continue."), nil
			}
			return nil, err
		}
		old = s.current(t)
		if old.Value != "" {
			return denied(r, http.StatusUnauthorized, "Sign in to continue."), nil
		}
	}
	if inaccessible != nil {
		return denied(r, http.StatusForbidden, inaccessible.Error()), nil
	}
	u, _ := url.Parse(key.Canonical)
	r.Host = u.Host // the local transport still dials only the Unix socket
	if r.Method != http.MethodGet || path == loginPath {
		r.Header.Set("Origin", key.Canonical)
	}
	var stop func() bool
	if path != loginPath && old.Value != "" && old.Binding == key {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: old.Value})
		ctx, cancel := context.WithCancel(r.Context())
		stop = context.AfterFunc(old.ctx, cancel)
		r = r.WithContext(ctx)
		res, err := s.authenticated(t, tr, r, old, stop)
		if err != nil {
			stop()
			cancel()
			return nil, err
		}
		res.Body = &responseBody{ReadCloser: res.Body, done: func() { stop(); cancel() }}
		return res, nil
	}
	res, err := tr.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if path == loginPath && r.Method == http.MethodPost && res.StatusCode == http.StatusOK {
		c, ok := browserCredential(res, key)
		if !ok {
			res.Body.Close()
			return nil, errors.New("Stage did not issue a valid session")
		}
		if err := s.replace(r.Context(), t, old, c); err != nil {
			res.Body.Close()
			return nil, err
		}
	}
	res.Header.Del("Set-Cookie")
	return res, nil
}

type targetAccessError struct{ message string }

func (e *targetAccessError) Error() string { return e.message }

func resolve(ctx context.Context, t target.Target, tr http.RoundTripper) (binding, error) {
	var status Status
	if err := read(ctx, t, tr, statusPath, &status); err != nil {
		return binding{}, err
	}
	canonical, err := target.NormalizeURL(status.CanonicalURL)
	if err != nil || canonical != status.CanonicalURL || status.InstallationID == "" || (t.Remote() && canonical != t.URL) {
		return binding{}, &targetAccessError{"Connect to this Stage server at its canonical URL."}
	}
	u, _ := url.Parse(canonical)
	ip, _ := netip.ParseAddr(u.Hostname())
	if t.Remote() && u.Scheme != "https" && u.Hostname() != "localhost" && !ip.IsLoopback() {
		return binding{}, &targetAccessError{"Use HTTPS to sign in to this Stage server."}
	}
	if status.Mode != "claimed" {
		return binding{}, &targetAccessError{"Finish setting up this Stage server in your browser."}
	}
	return binding{Endpoint: endpoint(t), Canonical: canonical, Installation: status.InstallationID}, nil
}

func (s *Store) authenticated(t target.Target, tr http.RoundTripper, r *http.Request, old *slot, stop func() bool) (*http.Response, error) {
	res, err := tr.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	clear := res.StatusCode == http.StatusUnauthorized ||
		(r.Method == http.MethodPost && res.StatusCode == http.StatusNoContent &&
			(r.URL.Path == logoutPath || r.URL.Path == "/api/account/password"))
	if clear {
		stop() // finish delivering this response after ending other requests
		if err := s.replace(r.Context(), t, old, credential{}); err != nil && !errors.Is(err, errChanged) {
			res.Body.Close()
			return nil, err
		}
	}
	res.Header.Del("Set-Cookie")
	return res, nil
}

// Check is a passive health check of an existing credential. The shared
// server returns its identity only for authenticated browser health on
// its canonical host. This check does not extend the session idle time.
func (s *Store) Check(ctx context.Context, t target.Target) error {
	old := s.current(t)
	if old.Value == "" || old.Binding.Endpoint != endpoint(t) {
		return nil
	}
	tr := t.Transport()
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, t.BaseURL()+healthPath, nil)
	if err != nil {
		return err
	}
	r.Host = t.Host()
	res, err := s.RoundTrip(t, tr, r)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil
	}
	var health struct {
		Identity string `json:"identity"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&health); err != nil {
		return err
	}
	if health.Identity == "" {
		return s.replace(ctx, t, old, credential{})
	}
	return nil
}

func browserCredential(res *http.Response, key binding) (credential, bool) {
	for _, c := range res.Cookies() {
		if c.Name != cookieName || !c.HttpOnly || c.Domain != "" || c.Path != "/" || c.MaxAge < 0 ||
			c.Secure != strings.HasPrefix(key.Canonical, "https://") || !c.Expires.After(time.Now()) {
			continue
		}
		if validToken(c.Value) {
			return credential{Binding: key, Value: c.Value, Expires: c.Expires, Scope: rand.Text()}, true
		}
	}
	return credential{}, false
}

func validToken(value string) bool {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == 32
}

type responseBody struct {
	io.ReadCloser
	done func()
}

func (b *responseBody) Close() error {
	defer b.done()
	return b.ReadCloser.Close()
}

func denied(r *http.Request, code int, message string) *http.Response {
	b, _ := json.Marshal(map[string]string{"error": message})
	return &http.Response{Request: r, StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(string(b))), ContentLength: int64(len(b))}
}
