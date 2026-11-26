package t3setup

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// DefaultT3URL is the T3 server address without a saved setting.
const DefaultT3URL = "http://127.0.0.1:3773"

// Where the T3 URL comes from.
const (
	SourceSettings = "settings"
	SourceDefault  = "default"
)

// Settings are the public T3 connection settings. Null selects the default.
type Settings struct {
	URL       *string `json:"url"`
	PublicURL *string `json:"publicUrl"`
}

// Endpoint is the T3 address in use.
type Endpoint struct {
	URL       string // server-side MCP/OAuth calls
	PublicURL string // browser "Open in T3" links
	Source    string // SourceSettings | SourceDefault
	PublicSet bool   // PublicURL is saved and independent of URL
}

// Resolve fills in the defaults for saved settings.
func Resolve(settings Settings) Endpoint {
	ep := Endpoint{URL: DefaultT3URL, Source: SourceDefault}
	if settings.URL != nil && *settings.URL != "" {
		ep.URL, ep.Source = *settings.URL, SourceSettings
	}
	ep.URL = strings.TrimRight(ep.URL, "/")
	ep.PublicURL = ep.URL
	if settings.PublicURL != nil && *settings.PublicURL != "" {
		ep.PublicURL, ep.PublicSet = strings.TrimRight(*settings.PublicURL, "/"), true
	}
	return ep
}

func validateSettings(raw json.RawMessage) error {
	settings, err := decodeSettings(raw)
	if err != nil {
		return err
	}
	if err := validateURL("url", settings.URL); err != nil {
		return err
	}
	return validateURL("publicUrl", settings.PublicURL)
}

func decodeSettings(raw json.RawMessage) (Settings, error) {
	var settings *Settings
	if err := db.DecodeStrict(raw, &settings); err != nil {
		return Settings{}, err
	}
	if settings == nil {
		return Settings{}, db.Invalid("T3 settings must be a JSON object")
	}
	return *settings, nil
}

func validateURL(field string, value *string) error {
	if value == nil || *value == "" {
		return nil
	}
	if _, err := NormalizeURL(*value); err != nil {
		return db.Invalid("%s: %s", field, err)
	}
	return nil
}

func saveSettings(ctx context.Context, port harness.Settings, patch map[string]*string) error {
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	return port.Save(ctx, t3.Kind, raw)
}

// NormalizeURL validates a T3 origin and removes its trailing slash.
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	u, err := url.Parse(s)
	if err != nil || s == "" {
		return "", errors.New("url must be an http(s) URL like http://127.0.0.1:3773")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("url must start with http:// or https://")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", errors.New("url needs a host")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("url must be only scheme://host[:port], with no path, query or credentials")
	}
	return strings.ToLower(u.Scheme) + "://" + u.Host, nil
}
