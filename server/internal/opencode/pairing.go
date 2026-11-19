package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PairUser is the HTTP Basic username OpenCode expects with its password or
// a pairing session token.
const PairUser = "opencode"

// TokenTTL is how long an OpenCode pairing session token is accepted.
const TokenTTL = 30 * 24 * time.Hour

// Registration is the discovery file the OpenCode service writes on its
// machine: $XDG_STATE_HOME/opencode/service.json, else
// ~/.local/state/opencode/service.json.
type Registration struct {
	ID, Version, URL, Password string
	PID                        int
}

// RegistrationError explains why the local registration is unusable.
// Reason is missing or invalid; the message never contains the password.
type RegistrationError struct {
	Reason, Path, Detail string
}

func (e *RegistrationError) Error() string {
	if e.Reason == "missing" {
		return fmt.Sprintf("No OpenCode service file found at %s. Start OpenCode on the Stage server's machine.", e.Path)
	}
	return fmt.Sprintf("The OpenCode service file at %s is unreadable or invalid (%s).", e.Path, e.Detail)
}

// RegistrationPath is where the local OpenCode service registers itself.
func RegistrationPath() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, _ := os.UserHomeDir()
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "opencode", "service.json")
}

// ReadRegistration reads and validates the local OpenCode registration.
func ReadRegistration() (Registration, error) {
	path := RegistrationPath()
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return Registration{}, &RegistrationError{Reason: "missing", Path: path}
	}
	if e != nil {
		return Registration{}, &RegistrationError{Reason: "invalid", Path: path, Detail: "unreadable"}
	}
	var raw struct {
		ID, Version, URL, Password string
		PID                        int
	}
	if e = json.Unmarshal(b, &raw); e != nil {
		return Registration{}, &RegistrationError{Reason: "invalid", Path: path, Detail: "not JSON"}
	}
	u, e := serviceURL("url", raw.URL)
	if e != nil || raw.PID <= 0 {
		return Registration{}, &RegistrationError{Reason: "invalid", Path: path, Detail: "missing a valid url or pid"}
	}
	return Registration{ID: raw.ID, Version: raw.Version, URL: u, Password: raw.Password, PID: raw.PID}, nil
}

var pairHTTP = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// call performs a request outside an Adapter. Transport errors drop the URL,
// which can carry a pairing code.
func call(ctx context.Context, method, target, password string, out any) error {
	req, e := http.NewRequestWithContext(ctx, method, target, nil)
	if e != nil {
		return errors.New("invalid OpenCode request")
	}
	req.Header.Set("Accept", "application/json")
	if password != "" {
		req.SetBasicAuth(PairUser, password)
	}
	resp, e := pairHTTP.Do(req)
	if e != nil {
		var ue *url.Error
		if errors.As(e, &ue) {
			e = ue.Err
		}
		return fmt.Errorf("OpenCode request: %w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError{resp.StatusCode}
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, out); e != nil {
		return errors.New("invalid OpenCode JSON")
	}
	return nil
}

// Probe asks a registered service for its version (GET /api/info).
func Probe(ctx context.Context, r Registration) (string, error) {
	var info serverInfo
	if e := call(ctx, http.MethodGet, r.URL+"/api/info", r.Password, &info); e != nil {
		return "", e
	}
	return info.Version, nil
}

// ParsePairLink validates a link printed by `opencode pair`
// (http(s)://host:port/auth/connect/CODE) and returns the service origin
// and the single-use code. Errors never echo the link.
func ParsePairLink(raw string) (endpoint, code string, err error) {
	const field = "Pairing link"
	s := strings.TrimSpace(raw)
	if e := invisible(field, s); e != nil {
		return "", "", e
	}
	u, e := url.Parse(s)
	switch {
	case s == "":
		return "", "", errors.New("Paste the link printed by opencode pair")
	case e != nil || (u.Scheme != "https" && u.Scheme != "http"):
		return "", "", fmt.Errorf("%s must be an http:// or https:// URL", field)
	case u.Host == "":
		return "", "", fmt.Errorf("%s needs a host", field)
	case u.User != nil:
		return "", "", fmt.Errorf("%s must not include credentials", field)
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return "", "", fmt.Errorf("%s must not include a query or fragment", field)
	}
	code, ok := strings.CutPrefix(u.Path, "/auth/connect/")
	if !ok || code == "" || strings.Contains(code, "/") {
		return "", "", fmt.Errorf("%s must look like http://host:port/auth/connect/CODE", field)
	}
	return u.Scheme + "://" + u.Host, code, nil
}

// ErrPairingExpired: OpenCode refused the code (expired or already used).
var ErrPairingExpired = errors.New("This pairing link expired or was already used. Run opencode pair for a new one.")

// Redeem exchanges a single-use pairing code for a session token.
func Redeem(ctx context.Context, endpoint, code string) (string, error) {
	var out struct{ Token string }
	e := call(ctx, http.MethodGet, endpoint+"/auth/connect/"+url.PathEscape(code), "", &out)
	var status responseError
	if errors.As(e, &status) && status.code == 401 {
		return "", ErrPairingExpired
	}
	if e != nil {
		return "", e
	}
	if out.Token == "" {
		return "", errors.New("OpenCode returned no pairing token")
	}
	return out.Token, nil
}

// Renew trades a valid session token for a fresh one: it asks the service
// for a pairing code (POST /api/pair) and redeems it.
func Renew(ctx context.Context, endpoint, token string) (string, error) {
	var out struct{ Code string }
	if e := call(ctx, http.MethodPost, endpoint+"/api/pair", token, &out); e != nil {
		return "", e
	}
	if out.Code == "" {
		return "", errors.New("OpenCode returned no pairing code")
	}
	return Redeem(ctx, endpoint, out.Code)
}
