package config

import "time"

// OpenCode authentication modes (opencode_settings.auth).
const (
	OpenCodeAuthNone   = "none"   // no credentials
	OpenCodeAuthLocal  = "local"  // the password in the local service's registration file (never stored)
	OpenCodeAuthPaired = "paired" // a renewable session token from an `opencode pair` link
	OpenCodeAuthBasic  = "basic"  // entered HTTP Basic credentials
)

// OpenCodeSettings is private server configuration. API responses use
// OpenCodeStatus, which contains only the presence and source of credentials.
type OpenCodeSettings struct {
	Endpoint, BrowserEndpoint, WebClient, Username, Password string
	// Auth is an OpenCodeAuth* mode; TokenIssuedAt is set while paired.
	Auth          string
	TokenIssuedAt *time.Time
}

type OpenCodeEdit struct {
	Endpoint        *string `json:"endpoint"`
	BrowserEndpoint *string `json:"browserEndpoint"`
	WebClient       *string `json:"webClient"`
	Username        *string `json:"username"`
	Password        *string `json:"password"`
	// NewInstance confirms connecting a different OpenCode service: the
	// current connection retires and keeps its tasks and bindings.
	NewInstance bool `json:"newInstance"`
}

type OpenCodeStatus struct {
	ConnectionID    int64      `json:"connectionId"`
	Endpoint        string     `json:"endpoint"`
	BrowserEndpoint string     `json:"browserEndpoint"`
	WebClient       string     `json:"webClient"`
	HasCredentials  bool       `json:"hasCredentials"`
	State           string     `json:"state"`
	Reason          string     `json:"reason,omitempty"`
	CheckedAt       *time.Time `json:"checkedAt,omitempty"`
	Auth            string     `json:"auth"`
	// ExpiresAt is when a paired session token expires (paired only).
	ExpiresAt *time.Time    `json:"expiresAt,omitempty"`
	Local     OpenCodeLocal `json:"local"`
}

// OpenCodeLocal describes the OpenCode service registered on the Stage
// server's machine. Reason is "" when available, else missing, invalid,
// unreachable or environment.
type OpenCodeLocal struct {
	Available bool   `json:"available"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	Reason    string `json:"reason"`
}

// OpenCodeSetup is the non-secret part of a machine's saved OpenCode
// settings: the main server may hold it.
type OpenCodeSetup struct {
	Endpoint        string `json:"endpoint"`
	BrowserEndpoint string `json:"browserEndpoint"`
	WebClient       string `json:"webClient"`
	Auth            string `json:"auth"`
}

// OpenCodeCredential is the secret part of a machine's saved OpenCode
// settings: HTTP Basic credentials or a paired session token and when it
// was issued. It stays where the harness runs (ADR 0002).
type OpenCodeCredential struct {
	Username      string     `json:"username"`
	Password      string     `json:"password"`
	TokenIssuedAt *time.Time `json:"tokenIssuedAt,omitempty"`
}

// Split is c's setup and credential.
func (c OpenCodeSettings) Split() (OpenCodeSetup, OpenCodeCredential) {
	return OpenCodeSetup{Endpoint: c.Endpoint, BrowserEndpoint: c.BrowserEndpoint, WebClient: c.WebClient, Auth: c.Auth},
		OpenCodeCredential{Username: c.Username, Password: c.Password, TokenIssuedAt: c.TokenIssuedAt}
}

// JoinOpenCode is the settings made of s and c.
func JoinOpenCode(s OpenCodeSetup, c OpenCodeCredential) OpenCodeSettings {
	return OpenCodeSettings{Endpoint: s.Endpoint, BrowserEndpoint: s.BrowserEndpoint, WebClient: s.WebClient, Auth: s.Auth,
		Username: c.Username, Password: c.Password, TokenIssuedAt: c.TokenIssuedAt}
}
