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
	ConnectionID      int64      `json:"connectionId"`
	Endpoint          string     `json:"endpoint"`
	BrowserEndpoint   string     `json:"browserEndpoint"`
	WebClient         string     `json:"webClient"`
	HasCredentials    bool       `json:"hasCredentials"`
	EnvironmentFields []string   `json:"environmentFields"`
	State             string     `json:"state"`
	Reason            string     `json:"reason,omitempty"`
	CheckedAt         *time.Time `json:"checkedAt,omitempty"`
	Auth              string     `json:"auth"`
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
