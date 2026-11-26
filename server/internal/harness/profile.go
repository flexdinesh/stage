package harness

import (
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Machine profiles: how this server runs, which decides what it may host.
const (
	// ProfileDesktop: the per-user managed service the desktop app ensures.
	ProfileDesktop = "desktop"
	// ProfileServer: a foreground `stage serve`.
	ProfileServer = "server"
	// ProfileContainer: the Docker image. Never hosts local processes.
	ProfileContainer = "container"
)

// ProfileEnv forces a profile; the Docker image sets it to container.
const ProfileEnv = "STAGE_MACHINE_PROFILE"

// LocalHarnessesEnv ("1") lets a foreground server host local harnesses,
// like --local-harnesses.
const LocalHarnessesEnv = "STAGE_LOCAL_HARNESSES"

// Profile controls which harnesses the server or worker can run.
type Profile struct {
	Kind string `json:"kind"`
	// LocalProcesses permits harnesses that require local processes.
	LocalProcesses bool `json:"localProcesses"`
}

// ResolveProfile: a container profile (env) never hosts; a managed server
// does; a foreground server does only when asked (flag or env).
func ResolveProfile(managed, localFlag bool, getenv func(string) string) Profile {
	if strings.TrimSpace(getenv(ProfileEnv)) == ProfileContainer {
		return Profile{Kind: ProfileContainer}
	}
	if managed {
		return Profile{Kind: ProfileDesktop, LocalProcesses: true}
	}
	return Profile{Kind: ProfileServer, LocalProcesses: localFlag || getenv(LocalHarnessesEnv) == "1"}
}

// Allows reports whether d can run on this machine, with the reason when not.
func (p Profile) Allows(d Descriptor) execution.Availability {
	if !d.LocalProcesses || p.LocalProcesses {
		return execution.Availability{Available: true}
	}
	if p.Kind == ProfileContainer {
		return execution.Availability{Reason: d.Name + " runs on your computer, not in a container. Use the Stage desktop app."}
	}
	return execution.Availability{Reason: d.Name + " runs only where Stage runs on your computer: use the desktop app, or start stage serve with --local-harnesses."}
}
