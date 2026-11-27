package db

import (
	"cmp"
	"path"
	"strings"
	"time"
)

// WorkspaceTitle is the display title safe for a scoped destination summary.
func WorkspaceTitle(title, root string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = root
	}
	title = strings.ReplaceAll(title, "\\", "/")
	if title == root || path.IsAbs(title) || (len(title) >= 3 && title[1:3] == ":/") || strings.Contains(title, "://") {
		title = path.Base(title)
	}
	if title == "." || title == "/" || title == "" {
		return "Workspace"
	}
	return title
}

// Destination is the safe display and selection projection of a workspace.
type Destination struct {
	ConnectionID    int64  `json:"connectionId"`
	WorkspaceID     string `json:"workspaceId"`
	Title           string `json:"title"`
	ConnectionLabel string `json:"connectionLabel"`
	MachineLabel    string `json:"machineLabel"`
	Discriminator   string `json:"discriminator,omitempty"`
	ProjectID       *int64 `json:"projectId,omitempty"`
	Available       bool   `json:"available"`
}

// ProjectSummary keeps the legacy binding selectors available to agents.
type ProjectSummary struct {
	ID         int64            `json:"id"`
	Title      string           `json:"title"`
	ArchivedAt *time.Time       `json:"archivedAt"`
	Aliases    []ProjectAlias   `json:"aliases"`
	Bindings   []BindingSummary `json:"bindings"`
}

type BindingSummary struct {
	ID               int64  `json:"id"`
	ConnectionID     int64  `json:"connectionId"`
	WorkspaceID      string `json:"workspaceId"`
	Title            string `json:"title"`
	ConnectionActive bool   `json:"connectionActive"`
	Primary          bool   `json:"primary"`
	Status           string `json:"status"`
}

type ConnectionSummary struct {
	ID         int64  `json:"id"`
	MachineID  int64  `json:"machineId"`
	Harness    string `json:"harness"`
	Label      string `json:"label"`
	Active     bool   `json:"active"`
	Configured bool   `json:"configured"`
}

func SummarizeConnection(c HarnessConnection, name string) ConnectionSummary {
	label := c.Label
	// Some adapters name an instance after its config directory or endpoint.
	if strings.ContainsAny(label, "/\\") {
		label = ""
	}
	return ConnectionSummary{ID: c.ID, MachineID: c.MachineID, Harness: c.Harness, Label: cmp.Or(label, name, c.Harness),
		Active: c.Active, Configured: c.Configured}
}
