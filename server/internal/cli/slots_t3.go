package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/flexdinesh/stage/server/internal/client"
)

// The T3 status panel of `stage harness t3`.
func init() {
	statusPrinters["t3"] = func(w io.Writer, st client.HarnessStatus, cli string) error {
		var s client.T3Status
		if err := json.Unmarshal(st.Details, &s); err != nil {
			return err
		}
		printT3Status(w, s, cli)
		return nil
	}
}

func printT3Status(w io.Writer, s client.T3Status, cli string) {
	fmt.Fprintf(w, "t3 %s at %s (%s)\n", s.State, s.T3URL, urlSource(s.T3URLSource))
	if s.PublicURL != s.T3URL {
		fmt.Fprintf(w, "  public    %s\n", s.PublicURL)
	}
	if s.T3 != nil {
		label := s.T3.Label
		if label == "" {
			label = s.T3.EnvironmentID
		}
		fmt.Fprintf(w, "  server    %s v%s (environment %s)\n", label, s.T3.ServerVersion, s.T3.EnvironmentID)
	} else if s.ProbeError != nil {
		fmt.Fprintf(w, "  server    not answering: %s\n", *s.ProbeError)
	}
	if s.TokenExpiresAt != nil {
		fmt.Fprintf(w, "  token     expires %s\n", s.TokenExpiresAt.Local().Format(time.RFC3339))
	}
	switch {
	case s.Sync.Running:
		fmt.Fprintf(w, "  sync      running: %d projects, %d threads so far\n", s.Sync.Projects, s.Sync.Threads)
	case s.LastFullSyncAt != nil:
		fmt.Fprintf(w, "  sync      last full sync %s\n", s.LastFullSyncAt.Local().Format(time.RFC3339))
	default:
		fmt.Fprintln(w, "  sync      never")
	}
	if s.AutoPair.Available {
		fmt.Fprintln(w, "  auto-pair available")
	} else if s.AutoPair.Reason != nil {
		fmt.Fprintf(w, "  auto-pair unavailable (%s)\n", autoPairReason(*s.AutoPair.Reason))
	}
	if s.LastError != nil && s.State != "connected" {
		fmt.Fprintf(w, "  error     %s\n", *s.LastError)
	}
	switch s.State {
	case "unreachable":
		fmt.Fprintf(w, "next: start T3 (`t3 serve`) on that machine, or point %[1]s at it: %[1]s harness t3 set url=URL\n", cli)
	case "unpaired", "expired", "rejected":
		if s.AutoPair.Available {
			fmt.Fprintf(w, "next: %s harness t3 run auto-pair\n", cli)
		} else {
			fmt.Fprintf(w, "next: run `t3 pair` on the T3 host, then: %s harness t3 run pair code=CODE\n", cli)
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
