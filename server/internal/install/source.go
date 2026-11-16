package install

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
	"strconv"
	"strings"
	"time"
)

// Repo is where Stage releases live.
const Repo = "flexdinesh/stage"

// Source finds and downloads releases.
type Source interface {
	// Latest is the newest stable (non-draft, non-prerelease) version.
	Latest(ctx context.Context) (string, error)
	// Fetch downloads the asset name of version into dest (at most max bytes).
	Fetch(ctx context.Context, version, name, dest string, max int64) error
}

// GitHub is the GitHub releases source. With a token it uses the API
// (private repositories); without one, the releases/latest redirect and
// direct download URLs, which have no API rate limit.
type GitHub struct {
	Repo   string
	API    string // https://api.github.com
	Web    string // https://github.com
	Token  string
	Client *http.Client

	assets map[string]map[string]string // version -> name -> API asset URL
}

// NewGitHub is the real source; token may be empty.
func NewGitHub(token string) *GitHub {
	return &GitHub{Repo: Repo, API: "https://api.github.com", Web: "https://github.com", Token: token,
		Client: &http.Client{Timeout: 10 * time.Minute}}
}

// Token finds a GitHub token: GH_TOKEN, GITHUB_TOKEN, else `gh auth token`.
func Token(ctx context.Context, d Deps) string {
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(d.env(k)); v != "" {
			return v
		}
	}
	gh, err := d.LookPath("gh")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := d.Run(ctx, nil, gh, "auth", "token")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (g *GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return http.DefaultClient
}

func (g *GitHub) Latest(ctx context.Context) (string, error) {
	var tag string
	if g.Token != "" {
		var rel struct {
			TagName string `json:"tag_name"`
		}
		if err := g.apiJSON(ctx, "/repos/"+g.Repo+"/releases/latest", &rel); err != nil {
			return "", err
		}
		tag = rel.TagName
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.Web+"/"+g.Repo+"/releases/latest", nil)
		if err != nil {
			return "", err
		}
		c := *g.client()
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := c.Do(req)
		if err != nil {
			return "", netErr(err)
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode/100 != 3 || !strings.Contains(loc, "/releases/tag/") {
			if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusNotFound {
				return "", notFound(g.Repo)
			}
			return "", httpErr(resp, g.Repo)
		}
		tag = loc[strings.LastIndex(loc, "/releases/tag/")+len("/releases/tag/"):]
		if t, err := url.PathUnescape(tag); err == nil {
			tag = t
		}
	}
	v := strings.TrimPrefix(tag, "v")
	if !strings.HasPrefix(tag, "v") || !ValidVersion(v) || Prerelease(v) {
		return "", newErr(CodeNetwork, "", "the latest release tag %q is not a stable Stage version", tag)
	}
	return v, nil
}

func (g *GitHub) Fetch(ctx context.Context, version, name, dest string, max int64) error {
	var u string
	accept := ""
	if g.Token != "" {
		if g.assets == nil {
			g.assets = map[string]map[string]string{}
		}
		if g.assets[version] == nil {
			var rel struct {
				Assets []struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				} `json:"assets"`
			}
			if err := g.apiJSON(ctx, "/repos/"+g.Repo+"/releases/tags/v"+url.PathEscape(version), &rel); err != nil {
				return err
			}
			m := map[string]string{}
			for _, a := range rel.Assets {
				m[a.Name] = a.URL
			}
			g.assets[version] = m
		}
		u = g.assets[version][name]
		if u == "" {
			return newErr(CodeNetwork, "", "release v%s has no asset %s", version, name)
		}
		accept = "application/octet-stream"
	} else {
		u = fmt.Sprintf("%s/%s/releases/download/v%s/%s", g.Web, g.Repo, url.PathEscape(version), url.PathEscape(name))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	g.auth(req)
	resp, err := g.client().Do(req)
	if err != nil {
		return netErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return newErr(CodeNetwork, tokenFix(g.Token), "release v%s has no asset %s (or the repository is private)", version, name)
		}
		return httpErr(resp, g.Repo)
	}
	return saveBody(resp.Body, dest, max)
}

// saveBody writes at most max bytes of r to dest (created exclusively).
func saveBody(r io.Reader, dest string, max int64) error {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return permErr(err, dest)
	}
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dest)
		if isNoSpace(err) {
			return permErr(err, filepath.Dir(dest))
		}
		return wrapErr(CodeNetwork, err, "check your connection and retry; nothing was installed", "download of %s was interrupted", filepath.Base(dest))
	}
	if n > max {
		_ = os.Remove(dest)
		return newErr(CodeVerify, verifyFix, "%s is larger than expected", filepath.Base(dest))
	}
	return nil
}

func (g *GitHub) auth(req *http.Request) {
	if g.Token != "" && strings.HasPrefix(req.URL.String(), g.API+"/") {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
}

func (g *GitHub) apiJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.API+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	g.auth(req)
	resp, err := g.client().Do(req)
	if err != nil {
		return netErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return notFound(g.Repo)
	}
	if resp.StatusCode != http.StatusOK {
		return httpErr(resp, g.Repo)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, metaMax)).Decode(v); err != nil {
		return wrapErr(CodeNetwork, err, "retry later", "unexpected GitHub API response")
	}
	return nil
}

func notFound(repo string) error {
	return newErr(CodeNetwork, "if the repository is private, log in with `gh auth login` (or set GH_TOKEN) and retry",
		"no stable Stage release found at github.com/%s", repo)
}

func tokenFix(token string) string {
	if token == "" {
		return "if the repository is private, log in with `gh auth login` (or set GH_TOKEN) and retry"
	}
	return "check that your GitHub login can read the repository"
}

func netErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Timeout() {
		return wrapErr(CodeNetwork, err, "check your connection and retry; nothing was installed", "GitHub timed out")
	}
	return wrapErr(CodeNetwork, err, "check your connection (are you offline?) and retry; nothing was installed", "cannot reach GitHub")
}

func httpErr(resp *http.Response, repo string) error {
	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
		(resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "") {
		when := "later"
		if s := resp.Header.Get("X-RateLimit-Reset"); s != "" {
			if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
				when = "after " + time.Unix(sec, 0).Format(time.Kitchen)
			}
		} else if s := resp.Header.Get("Retry-After"); s != "" {
			when = "in " + s + "s"
		}
		return newErr(CodeNetwork, "retry "+when+", or log in with `gh auth login` for a higher limit", "GitHub rate limit reached")
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return newErr(CodeNetwork, "log in again with `gh auth login` (or fix GH_TOKEN) and retry", "GitHub refused access to github.com/%s (HTTP %d)", repo, resp.StatusCode)
	}
	return newErr(CodeNetwork, "retry later", "GitHub answered HTTP %d", resp.StatusCode)
}
