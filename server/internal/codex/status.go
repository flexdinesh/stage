package codex

import "context"

// MCPServerStatus is Codex's view of one configured MCP server
// (mcpServerStatus/list).
type MCPServerStatus struct {
	Name string `json:"name"`
	// ToolsError: Codex could not start the server or list its tools.
	ToolsError *string `json:"toolsError"`
}

// MCPServer reads the configured MCP server name through the daemon
// (mcpServerStatus/list, limited to that server). Codex starts the server
// to list its tools, so a broken registration reports ToolsError. found is
// false when no server of that name is configured.
func (cl *Client) MCPServer(ctx context.Context, name string) (MCPServerStatus, bool, error) {
	var res struct {
		Data []MCPServerStatus `json:"data"`
	}
	params := map[string]any{"serverName": name, "detail": "toolsAndAuthOnly"}
	if err := cl.call(ctx, "mcpServerStatus/list", params, &res); err != nil {
		return MCPServerStatus{}, false, err
	}
	for _, s := range res.Data {
		if s.Name == name {
			return s, true, nil
		}
	}
	return MCPServerStatus{}, false, nil
}

// RateLimitWindow is one usage window of the account's Codex limit.
type RateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
	// ResetsAt is a Unix time in seconds.
	ResetsAt *int64 `json:"resetsAt"`
}

// RateLimits is the account's Codex usage (account/rateLimits/read,
// rateLimits: the single-bucket view).
type RateLimits struct {
	Primary   *RateLimitWindow `json:"primary"`
	Secondary *RateLimitWindow `json:"secondary"`
}

// RateLimits reads the signed-in account's usage windows. Codex answers
// only for a ChatGPT login.
func (cl *Client) RateLimits(ctx context.Context) (RateLimits, error) {
	var res struct {
		RateLimits RateLimits `json:"rateLimits"`
	}
	if err := cl.call(ctx, "account/rateLimits/read", map[string]any{"excludeResetCreditDetails": true}, &res); err != nil {
		return RateLimits{}, err
	}
	return res.RateLimits, nil
}
