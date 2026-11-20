package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/client"
)

// The OpenCode status panel of `stage harness opencode`.
func init() {
	statusPrinters["opencode"] = func(w io.Writer, st client.HarnessStatus, cli string) error {
		var s client.OpenCodeStatus
		if err := json.Unmarshal(st.Details, &s); err != nil {
			return err
		}
		printOpenCodeStatus(w, s, cli)
		return nil
	}
}

func printOpenCodeStatus(w io.Writer, s client.OpenCodeStatus, cli string) {
	if s.State == "unconfigured" {
		fmt.Fprintln(w, "opencode not connected")
	} else {
		fmt.Fprintf(w, "opencode %s at %s (%s)\n", s.State, s.Endpoint, openCodeAuth(s.Auth))
	}
	if s.BrowserEndpoint != "" {
		fmt.Fprintf(w, "  browser   %s\n", s.BrowserEndpoint)
	}
	if s.WebClient != "" {
		fmt.Fprintf(w, "  web       %s\n", s.WebClient)
	}
	if len(s.EnvironmentFields) > 0 {
		fmt.Fprintf(w, "  managed   by the server environment: %s\n", strings.Join(s.EnvironmentFields, ", "))
	}
	if s.ExpiresAt != nil {
		fmt.Fprintf(w, "  token     expires %s (renewed automatically)\n", s.ExpiresAt.Local().Format(time.RFC3339))
	}
	switch l := s.Local; {
	case l.Available:
		fmt.Fprintf(w, "  local     OpenCode %s at %s\n", l.Version, l.URL)
	case l.Reason == "missing":
		fmt.Fprintln(w, "  local     no OpenCode service registered on the server's machine")
	case l.Reason == "invalid":
		fmt.Fprintln(w, "  local     the OpenCode service file on the server's machine is invalid")
	case l.Reason == "unreachable":
		fmt.Fprintf(w, "  local     OpenCode registered at %s is not answering\n", l.URL)
	}
	if s.Reason != "" && s.State != "connected" {
		fmt.Fprintf(w, "  error     %s\n", s.Reason)
	}
	pair := "next: run `opencode pair` on the OpenCode host, then: " + cli + " harness opencode run pair link=LINK"
	switch {
	case s.State == "unconfigured" && s.Local.Available:
		fmt.Fprintf(w, "next: %[1]s harness opencode run connect-local (or pair a remote service: %[1]s harness opencode run pair link=LINK)\n", cli)
	case s.State == "unconfigured", s.State == "rejected" && s.Auth == "paired":
		fmt.Fprintln(w, pair)
	case s.State == "rejected" && s.Auth == "local":
		fmt.Fprintf(w, "next: %s harness opencode run connect-local\n", cli)
	case s.State == "rejected":
		fmt.Fprintf(w, "next: update the OpenCode credentials in Settings, or: %s harness opencode run pair link=LINK\n", cli)
	}
}

func openCodeAuth(a string) string {
	switch a {
	case "local":
		return "the server machine's service"
	case "paired":
		return "paired"
	case "basic":
		return "basic credentials"
	}
	return "no credentials"
}
