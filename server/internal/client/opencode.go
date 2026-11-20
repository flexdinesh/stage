package client

import "time"

// OpenCodeStatus is the details of GET /api/harnesses/opencode/status
// (docs/api.md).
type OpenCodeStatus struct {
	ConnectionID      int64      `json:"connectionId"`
	State             string     `json:"state"` // unconfigured | connected | unavailable | rejected
	Reason            string     `json:"reason,omitempty"`
	CheckedAt         *time.Time `json:"checkedAt,omitempty"`
	Endpoint          string     `json:"endpoint"`
	BrowserEndpoint   string     `json:"browserEndpoint"`
	WebClient         string     `json:"webClient"`
	HasCredentials    bool       `json:"hasCredentials"`
	EnvironmentFields []string   `json:"environmentFields"`
	Auth              string     `json:"auth"` // none | local | paired | basic
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
	Local             struct {
		Available bool   `json:"available"`
		URL       string `json:"url"`
		Version   string `json:"version"`
		Reason    string `json:"reason"` // "" | missing | invalid | unreachable | environment
	} `json:"local"`
}
