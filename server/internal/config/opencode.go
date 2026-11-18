package config

import "time"

// OpenCodeSettings is private server configuration. API responses use
// OpenCodeStatus, which contains only the presence and source of credentials.
type OpenCodeSettings struct {
	Endpoint, BrowserEndpoint, WebClient, Username, Password string
}

type OpenCodeEdit struct {
	Endpoint        *string `json:"endpoint"`
	BrowserEndpoint *string `json:"browserEndpoint"`
	WebClient       *string `json:"webClient"`
	Username        *string `json:"username"`
	Password        *string `json:"password"`
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
}
