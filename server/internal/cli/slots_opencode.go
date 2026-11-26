package cli

import (
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/client"
)

type openCodeStatus struct {
	ConnectionID      int64      `json:"connectionId"`
	State             string     `json:"state"` // unconfigured | connected | unavailable | rejected
	Reason            string     `json:"reason,omitempty"`
	CheckedAt         *time.Time `json:"checkedAt,omitempty"`
	Endpoint          string     `json:"endpoint"`
	BrowserEndpoint   string     `json:"browserEndpoint"`
	WebClient         string     `json:"webClient"`
	HasCredentials    bool       `json:"hasCredentials"`
	EnvironmentFields []string   `json:"environmentFields"`
	Auth              string     `json:"auth"` // none | local | paired | basic
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
	Local             struct {
		Available bool   `json:"available"`
		URL       string `json:"url"`
		Version   string `json:"version"`
		Reason    string `json:"reason"` // "" | missing | invalid | unreachable | environment
	} `json:"local"`
}

// The OpenCode status panel of `stage harness opencode`.
func init() {
	statusPrinters["opencode"] = func(w io.Writer, st client.HarnessStatus, cli string) error {
		var s openCodeStatus
		if err := json.Unmarshal(st.Details, &s); err != nil {
			return err
		}
		printOpenCodeStatus(w, s, cli)
		return nil
	}
}

func printOpenCodeStatus(w io.Writer, s openCodeStatus, cli string) {
	if s.State == "unconfigured" {
		line(w, toneWarn, "opencode %s", toneWarn.paint("not connected"))
	} else {
		t := stateTone(s.State)
		line(w, t, "opencode %s at %s %s", t.paint(s.State), s.Endpoint, dim("("+openCodeAuth(s.Auth)+")"))
	}
	if s.BrowserEndpoint != "" {
		kv(w, "browser", "%s", s.BrowserEndpoint)
	}
	if s.WebClient != "" {
		kv(w, "web", "%s", s.WebClient)
	}
	if len(s.EnvironmentFields) > 0 {
		kv(w, "managed", "by the server environment: %s", strings.Join(s.EnvironmentFields, ", "))
	}
	if s.ExpiresAt != nil {
		kv(w, "token", "expires %s %s", s.ExpiresAt.Local().Format(time.RFC3339), dim("(renewed automatically)"))
	}
	switch l := s.Local; {
	case l.Available:
		kv(w, "local", "OpenCode %s at %s", l.Version, l.URL)
	case l.Reason == "missing":
		kv(w, "local", "%s", dim("no OpenCode service registered on the server's machine"))
	case l.Reason == "invalid":
		kv(w, "local", "%s", toneWarn.paint("the OpenCode service file on the server's machine is invalid"))
	case l.Reason == "unreachable":
		kv(w, "local", "OpenCode registered at %s %s", l.URL, toneWarn.paint("is not answering"))
	}
	if s.Reason != "" && s.State != "connected" {
		kv(w, "error", "%s", toneBad.paint(s.Reason))
	}
	pair := []step{
		{"opencode pair", "on the OpenCode host"},
		{cli + " harness opencode run pair link=LINK", "with the link it prints"},
	}
	switch {
	case s.State == "unconfigured" && s.Local.Available:
		nextSteps(w,
			step{cli + " harness opencode run connect-local", "connect the local OpenCode"},
			step{cli + " harness opencode run pair link=LINK", "or pair a remote service"})
	case s.State == "unconfigured", s.State == "rejected" && s.Auth == "paired":
		nextSteps(w, pair...)
	case s.State == "rejected" && s.Auth == "local":
		nextSteps(w, step{cli + " harness opencode run connect-local", ""})
	case s.State == "rejected":
		nextSteps(w, step{cli + " harness opencode run pair link=LINK", "or update the OpenCode credentials in Settings"})
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
