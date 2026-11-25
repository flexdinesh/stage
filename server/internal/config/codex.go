package config

// CodexSettings is a machine's Codex connection setup (codex_settings).
// Non-secret only: Codex keeps its own login under CODEX_HOME.
type CodexSettings struct {
	// Binary is the Codex CLI ("" = codex on PATH).
	Binary string
	// Connected: the connect action succeeded and Disconnect has not run
	// since, so a restart restores the connection.
	Connected bool
}
