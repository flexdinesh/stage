package serve

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"

	"github.com/flexdinesh/stage/server/internal/auth"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/install"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/ownercontrol"
)

func sharedOrigin(configured string, listeners []*listen.Listener) (string, error) {
	const browserListener = "Shared access needs a browser listener. Add 127.0.0.1:7380 to STAGE_LISTEN and set STAGE_PUBLIC_URL=http://127.0.0.1:7380 for local testing, or set STAGE_PUBLIC_URL to your HTTPS proxy origin."
	origin, err := auth.ParsePublicOrigin(configured)
	if err != nil {
		if configured == "" {
			return "", errors.New(browserListener)
		}
		return "", err
	}
	u, err := url.Parse(origin)
	if err != nil {
		return "", errors.New("Set STAGE_PUBLIC_URL to the browser URL for shared access.")
	}
	for _, l := range listeners {
		if addr, ok := l.Addr().(*net.TCPAddr); ok && !addr.IP.IsLoopback() {
			return "", errors.New("Shared access requires a private upstream. Bind Stage TCP to loopback or use a protected Unix socket, then connect through the HTTPS proxy.")
		}
	}
	if u.Scheme == "https" {
		// TLS terminates at the configured browser-facing proxy. The upstream
		// can be a protected product socket instead of TCP.
		return origin, nil
	}
	port := 80
	if u.Port() != "" {
		port, _ = strconv.Atoi(u.Port())
	}
	for _, l := range listeners {
		addr, ok := l.Addr().(*net.TCPAddr)
		if ok && addr.Port == port && addr.IP.IsLoopback() &&
			(u.Hostname() == "localhost" || addr.IP.Equal(net.ParseIP(u.Hostname()))) {
			return origin, nil
		}
	}
	return "", errors.New(browserListener)
}

func announceShared(o Options, control *ownercontrol.Server, dataDir string, state store.InstallationAccess, setup auth.Link, logger *slog.Logger) {
	home, _ := os.UserHomeDir()
	reissue := install.CLIName(home, buildinfo.Identity) + " owner reissue-setup"
	switch {
	case setup.URL() != "":
		if !publishSetup(o, control, dataDir, setup, logger) {
			logger.Error("Could not write private startup output. Run the setup reissue command as the installation owner.", "command", reissue)
		}
	case state.Mode == store.AccessPending:
		logger.Info("Shared setup is pending. Run the setup reissue command as the installation owner.", "command", reissue)
	}
}

func publishSetup(o Options, control *ownercontrol.Server, dataDir string, setup auth.Link, logger *slog.Logger) bool {
	deadline := setup.ExpiresAt.UTC().Format("15:04:05 UTC on 2 Jan 2006")
	output := "Open this link by " + deadline + " to create the first admin.\n" + setup.URL() + "\n"
	if o.BootstrapOutput != nil && o.Service == nil && o.Profile.Kind != harness.ProfileContainer {
		n, err := io.WriteString(o.BootstrapOutput, output)
		return n == len(output) && err == nil
	}
	if err := control.PublishBootstrap(output); err != nil {
		return false
	}
	logger.Info("First admin setup link is in private startup output.", "file", ownercontrol.BootstrapPath(dataDir))
	return true
}
