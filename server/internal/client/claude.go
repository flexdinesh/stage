package client

// ClaudeDetails is the details of GET /api/harnesses/claude/status
// (docs/api.md, Claude Code's setup).
type ClaudeDetails struct {
	// Binary is the resolved claude executable ("" when none was found).
	Binary string `json:"binary,omitempty"`
	// Version is claude --version ("" until the checks ran).
	Version         string `json:"version,omitempty"`
	ConfigDirectory string `json:"configDirectory,omitempty"`
	LoggedIn        bool   `json:"loggedIn"`
	// Scope: background launches run in their own systemd user scope.
	Scope bool `json:"scope"`
}
