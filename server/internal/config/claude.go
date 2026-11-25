package config

// ClaudeSettings is a machine's Claude Code harness setup
// (claude_settings, migration 0030). No row reads the zero value.
type ClaudeSettings struct {
	// Binary is the claude command name or path the user set ("": default
	// lookup).
	Binary string `json:"binary"`
	// ConfigDir is the config directory Claude reported when it was last
	// connected: the connection's identity, which health checks.
	ConfigDir string `json:"configDir"`
	// Disconnected: the user disconnected Claude Code on this machine.
	Disconnected bool `json:"disconnected"`
}
