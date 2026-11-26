package config

// CodexSettings is a machine's Codex connection setup (codex_settings).
// Non-secret only: Codex keeps its own login under CODEX_HOME. A worker
// reads and saves it on the main server (setup.codex.get and save).
type CodexSettings struct {
	// Binary is the Codex CLI ("" = codex on PATH).
	Binary string `json:"binary"`
	// Connected: the connect action succeeded and Disconnect has not run
	// since, so a restart restores the connection.
	Connected bool `json:"connected"`
}
