//go:build tools

// Package internal pins backend dependencies that later phases use, so
// parallel work never edits go.mod. Remove an import once it has real usage.
package internal

import (
	_ "github.com/modelcontextprotocol/go-sdk/mcp"
)
