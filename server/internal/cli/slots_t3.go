package cli

import (
	"encoding/json"
	"io"
	"time"

	"github.com/flexdinesh/stage/server/internal/client"
)

// t3Status is the details of GET /api/harnesses/t3/status (docs/api.md).
type t3Status struct {
	Connected            bool       `json:"connected"`
	State                string     `json:"state"` // unreachable | unpaired | expired | rejected | connected
	T3URL                string     `json:"t3Url"`
	PublicURL            string     `json:"publicUrl"`
	T3URLSource          string     `json:"t3UrlSource"` // env | settings | default
	EnvironmentID        *string    `json:"environmentId"`
	ClientID             *string    `json:"clientId"`
	TokenExpiresAt       *time.Time `json:"tokenExpiresAt"`
	AdminTokenConfigured bool       `json:"adminTokenConfigured"`
	LastError            *string    `json:"lastError"`
	LastFullSyncAt       *time.Time `json:"lastFullSyncAt"`
	Reachable            bool       `json:"reachable"`
	CheckedAt            *time.Time `json:"checkedAt"`
	ProbeError           *string    `json:"probeError"`
	T3                   *struct {
		EnvironmentID string `json:"environmentId"`
		Label         string `json:"label"`
		ServerVersion string `json:"serverVersion"`
	} `json:"t3"`
	AutoPair struct {
		Available bool    `json:"available"`
		Reason    *string `json:"reason"`
	} `json:"autoPair"`
	Sync struct {
		Running   bool       `json:"running"`
		StartedAt *time.Time `json:"startedAt"`
		Projects  int        `json:"projects"`
		Threads   int        `json:"threads"`
	} `json:"sync"`
}

// The T3 status panel of `stage harness t3`.
func init() {
	statusPrinters["t3"] = func(w io.Writer, st client.HarnessStatus, cli string) error {
		var s t3Status
		if err := json.Unmarshal(st.Details, &s); err != nil {
			return err
		}
		printT3Status(w, s, cli)
		return nil
	}
}

func printT3Status(w io.Writer, s t3Status, cli string) {
	t := stateTone(s.State)
	line(w, t, "t3 %s at %s %s", t.paint(s.State), s.T3URL, dim("("+urlSource(s.T3URLSource)+")"))
	if s.PublicURL != s.T3URL {
		kv(w, "public", "%s", s.PublicURL)
	}
	if s.T3 != nil {
		label := s.T3.Label
		if label == "" {
			label = s.T3.EnvironmentID
		}
		kv(w, "server", "%s v%s %s", label, s.T3.ServerVersion, dim("(environment "+s.T3.EnvironmentID+")"))
	} else if s.ProbeError != nil {
		kv(w, "server", "%s %s", toneBad.paint("not answering:"), *s.ProbeError)
	}
	if s.TokenExpiresAt != nil {
		kv(w, "token", "expires %s", s.TokenExpiresAt.Local().Format(time.RFC3339))
	}
	switch {
	case s.Sync.Running:
		kv(w, "sync", "running: %d projects, %d threads so far", s.Sync.Projects, s.Sync.Threads)
	case s.LastFullSyncAt != nil:
		kv(w, "sync", "last full sync %s", s.LastFullSyncAt.Local().Format(time.RFC3339))
	default:
		kv(w, "sync", "never")
	}
	if s.AutoPair.Available {
		kv(w, "auto-pair", "available")
	} else if s.AutoPair.Reason != nil {
		kv(w, "auto-pair", "unavailable %s", dim("("+autoPairReason(*s.AutoPair.Reason)+")"))
	}
	if s.LastError != nil && s.State != "connected" {
		kv(w, "error", "%s", toneBad.paint(*s.LastError))
	}
	switch s.State {
	case "unreachable":
		nextSteps(w,
			step{"t3 serve", "start T3 on that machine"},
			step{cli + " harness t3 set url=URL", "or point " + cli + " at it"})
	case "unpaired", "expired", "rejected":
		if s.AutoPair.Available {
			nextSteps(w, step{cli + " harness t3 run auto-pair", ""})
		} else {
			nextSteps(w,
				step{"t3 pair", "on the T3 host"},
				step{cli + " harness t3 run pair code=CODE", "with the code it prints"})
		}
	}
}

func urlSource(src string) string {
	switch src {
	case "settings":
		return "saved setting"
	}
	return "default"
}

func autoPairReason(r string) string {
	switch r {
	case "not_local":
		return "T3 is not on the server's machine"
	case "cli_not_found":
		return "t3 CLI not found on the server's machine"
	case "fake":
		return "fake T3"
	}
	return r
}
