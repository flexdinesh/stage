package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/ownercontrol"
)

const ownerUsage = `Usage:
  stage owner reissue-setup                 Replace the pending first-admin setup link.
  stage owner recover-admin --email EMAIL   Issue a password reset link for an active admin.

Run on the server as the Stage installation owner. Set STAGE_DATA_DIR to the
server's data directory when it differs from the default. Links expire in 30 minutes.
`

func runOwner(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, ownerUsage)
		return 0
	}
	reissue := len(args) == 1 && args[0] == "reissue-setup"
	recover := len(args) == 3 && args[0] == "recover-admin" && args[1] == "--email" && args[2] != ""
	if !reissue && !recover {
		fmt.Fprint(stderr, ownerUsage)
		return 2
	}
	dataDir, err := config.DataDirFrom(os.Getenv)
	if err != nil {
		fmt.Fprintln(stderr, "Set STAGE_DATA_DIR to the server's data directory.")
		return 1
	}
	var link string
	if reissue {
		link, err = ownercontrol.ReissueSetup(ctx, dataDir)
	} else {
		link, err = ownercontrol.RecoverAdmin(ctx, dataDir, args[2])
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := fmt.Fprintln(stdout, link); err != nil {
		fmt.Fprintln(stderr, "Could not print the link. Run the command again to get a new link.")
		return 1
	}
	return 0
}
