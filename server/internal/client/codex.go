package client

import "time"

// CodexStatus is the details of GET /api/harnesses/codex/status
// (docs/api.md). The state and reason are the status's own.
type CodexStatus struct {
	Binary           string `json:"binary"`
	CLIVersion       string `json:"cliVersion"`
	AppServerVersion string `json:"appServerVersion"`
	CodexHome        string `json:"codexHome"`
	Daemon           string `json:"daemon"` // running | stopped | unknown
	Account          *struct {
		Type  string `json:"type"`
		Email string `json:"email"`
		Plan  string `json:"plan"`
	} `json:"account"`
	Fix       string     `json:"fix"`
	CheckedAt *time.Time `json:"checkedAt"`
}
