package opencode

import "context"

// MCPServer is OpenCode's status of one configured MCP server (mcp.list).
type MCPServer struct {
	Name string
	// Status is connected, pending, disabled, failed or needs_auth; Error
	// is set for failed and needs_auth.
	Status, Error string
}

// MCPServer reads the named MCP server from GET /api/mcp. No location is
// sent: the service's default location answers, and none is persisted.
// found is false when no server of that name is configured.
func (a *Adapter) MCPServer(ctx context.Context, name string) (MCPServer, bool, error) {
	var res envelope[[]struct {
		Name   string
		Status struct{ Status, Error string }
	}]
	if err := a.get(ctx, "/api/mcp", &res); err != nil {
		return MCPServer{}, false, err
	}
	for _, s := range res.Data {
		if s.Name == name {
			return MCPServer{Name: s.Name, Status: s.Status.Status, Error: s.Status.Error}, true, nil
		}
	}
	return MCPServer{}, false, nil
}
