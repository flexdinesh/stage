package serve

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// fallbackLocalLabel names the local machine when nothing better is known.
const fallbackLocalLabel = "Stage server"

// defaultLocalLabel derives the local machine's default name from where the
// server runs, never from the viewer: in a container (whose hostname is a
// random id) the host of STAGE_PUBLIC_URL, elsewhere the hostname, as a
// worker names itself on join. Empty or unusable input falls back to
// "Stage server". Capped at db.MaxMachineLabel characters.
func defaultLocalLabel(profile harness.Profile, publicURL string, hostname func() (string, error)) string {
	var label string
	if profile.Kind == harness.ProfileContainer {
		if u, err := url.Parse(strings.TrimSpace(publicURL)); err == nil {
			label = u.Hostname()
		}
	} else if h, err := hostname(); err == nil {
		label = h
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return fallbackLocalLabel
	}
	if r := []rune(label); len(r) > db.MaxMachineLabel {
		label = string(r[:db.MaxMachineLabel])
	}
	return label
}

// nameLocalMachine sets the local machine's default name while it is unset
// (migration 0022); a name the user gave is kept. A failure only logs: the
// label is cosmetic.
func nameLocalMachine(ctx context.Context, store *db.Store, profile harness.Profile, publicURL string, logger *slog.Logger) {
	label := defaultLocalLabel(profile, publicURL, os.Hostname)
	set, err := store.SetDefaultLocalLabel(ctx, label)
	if err != nil {
		logger.Warn("name local machine", "err", err)
		return
	}
	if set {
		logger.Info("local machine named", "label", label)
	}
}
