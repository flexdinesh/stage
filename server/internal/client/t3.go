package client

import "time"

// T3Status is the details of GET /api/harnesses/t3/status (docs/api.md).
type T3Status struct {
	Connected            bool       `json:"connected"`
	State                string     `json:"state"` // unreachable | unpaired | expired | rejected | connected
	T3URL                string     `json:"t3Url"`
	PublicURL            string     `json:"publicUrl"`
	T3URLSource          string     `json:"t3UrlSource"` // env | settings | default
	EnvironmentID        *string    `json:"environmentId"`
	ClientID             *string    `json:"clientId"`
	TokenExpiresAt       *time.Time `json:"tokenExpiresAt"`
	AdminTokenConfigured bool       `json:"adminTokenConfigured"`
	LastError            *string    `json:"lastError"`
	LastFullSyncAt       *time.Time `json:"lastFullSyncAt"`
	Reachable            bool       `json:"reachable"`
	CheckedAt            *time.Time `json:"checkedAt"`
	ProbeError           *string    `json:"probeError"`
	T3                   *struct {
		EnvironmentID string `json:"environmentId"`
		Label         string `json:"label"`
		ServerVersion string `json:"serverVersion"`
	} `json:"t3"`
	AutoPair struct {
		Available bool    `json:"available"`
		Reason    *string `json:"reason"`
	} `json:"autoPair"`
	Sync struct {
		Running   bool       `json:"running"`
		StartedAt *time.Time `json:"startedAt"`
		Projects  int        `json:"projects"`
		Threads   int        `json:"threads"`
	} `json:"sync"`
}
