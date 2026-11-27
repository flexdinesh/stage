// Command app is the Stage desktop client (docs/desktop.md): a MyGo
// webview hosting @stage/app (desktop/app/ui), which reaches the active
// Stage server through bridge B: this machine's per-user service over its
// Unix socket, or a saved remote server over TCP (package target). The
// bundled stage CLI (resources/<platform>/bin/stage) starts and inspects
// the local service; package launch drives it, package nav polices URLs.
//
//	STAGE_DESKTOP_SOCKET=/run/user/1000/stage-desktopdev/x.sock
//	    development builds only (mise run desktop:dev): talk to this
//	    foreground dev server instead of the service
//	STAGE_DESKTOP_SELFCHECK=1
//	    print how the bundled CLI resolves and exit (release verification,
//	    docs/release.md)
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"

	"github.com/flexdinesh/stage/desktop/app/alerts"
	"github.com/flexdinesh/stage/desktop/app/bridge"
	"github.com/flexdinesh/stage/desktop/app/launch"
	"github.com/flexdinesh/stage/desktop/app/session"
	"github.com/flexdinesh/stage/desktop/app/target"
)

// Set by `mise run dist` / `mise run release` through MYGO_LDFLAGS
// (-X main.appName=Stage ...); the defaults are the developer app's.
var (
	appName       = "Stage Dev"
	appIdentifier = "io.github.flexdinesh.stage-dev.desktop"
	// appIdentity is the deep-link scheme until the bundled CLI reports
	// its identity (they match in a packaged app: mygo.config.ts urlSchemes).
	appIdentity = "stage-dev"
	// appBuild is the release build stamp ("stage-build/1 identity=...
	// version=... commit=...;", docs/release.md), which release
	// verification reads from the executable; empty in unstamped builds.
	appBuild = ""
)

// dist is the UI build (desktop/app/ui, vite outDir ../dist).
//
//go:embed all:dist
var dist embed.FS

// StateChanged is broadcast on every change of the connection state;
// pages subscribe, then read Desktop.State().
var StateChanged = mygo.NewEvent[launch.State]("host:state")

// Navigate asks the UI to show an app path (deep links); a page that was
// not listening yet reads it with Desktop.TakeNavigation().
var Navigate = mygo.NewEvent[string]("host:navigate")

// NotificationDelivered is broadcast with the newest notification id each
// time the active server's stream delivers notifications (shown or not),
// so the page refetches its inbox at once.
var NotificationDelivered = mygo.NewEvent[int64]("notifications:delivered")

// ServersChanged is broadcast with the server list whenever it changes:
// a server added, edited or removed, a switch, or a server's needs-input
// count.
var ServersChanged = mygo.NewEvent[[]ServerEntry]("servers:changed")

func main() {
	if os.Getenv(selfcheckEnv) == "1" {
		os.Exit(selfcheck(os.Stdout, os.Stderr))
	}
	ui, err := fs.Sub(dist, "dist")
	if err != nil {
		log.Fatal(err)
	}
	api := bridge.NewAPIProxy(target.Target{})
	// Bridge B: the page's own scheme serves the UI and /api/* (which
	// replaces MyGo's embedded frontend, so no frontendDist).
	if err := mygo.Protocol.Handle("mygo", bridge.Scheme(api, mygo.FileServer(ui))); err != nil {
		log.Fatal(err)
	}
	h := &host{api: api, dev: mygo.IsDev()}
	mygo.Bind(&Desktop{h: h})

	// `mygo generate` (and `mygo build`) run the app with MYGO_GENERATE to
	// write the TypeScript client: App.Run returns at once, and nothing
	// else may happen (no CLI, socket, paths or scheme registration).
	if os.Getenv("MYGO_GENERATE") == "" {
		if _, err := fs.Stat(ui, "index.html"); err != nil && os.Getenv("MYGO_DEV_URL") == "" {
			log.Print("stage desktop: the UI is not built (desktop/app/dist); run `pnpm --filter @stage/desktop build`")
		}
		if !setup(h) {
			return
		}
	}
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// setup configures the running app before App.Run; false in a second
// instance, which hands its arguments (a deep link) to the first and
// exits.
func setup(h *host) bool {
	h.paths()
	if !mygo.App.RequestSingleInstanceLock() {
		log.Print("stage desktop: already running; the running instance takes over")
		return false
	}
	res, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		log.Printf("stage desktop: resources: %v", err)
	}
	devSocket := ""
	if h.dev {
		devSocket = os.Getenv("STAGE_DESKTOP_SOCKET")
		h.devOrigin = os.Getenv("MYGO_DEV_URL")
	}
	servers, err := target.Open(filepath.Join(h.userData, "servers.json"))
	if err != nil {
		log.Printf("stage desktop: servers: %v", err)
	}
	h.servers = servers
	sessions, err := session.Open(filepath.Join(h.userData, "sessions"), h.accountChanged)
	if err != nil {
		log.Printf("stage desktop: session storage: %v", err)
	}
	h.api.Sessions = sessions
	h.alerts = alerts.New(alerts.Config{
		PrefsPath: filepath.Join(h.userData, "notifications.json"),
		Sink:      mygoSink{},
		RestoreSession: func(t target.Target) alerts.Session {
			if sessions == nil {
				return alerts.Session{}
			}
			return sessions.AlertSession(t)
		},
		AccessEnded: func(t target.Target, id string) {
			if sessions != nil {
				go func() {
					if err := sessions.AlertAccessEnded(t, id); err != nil {
						log.Printf("stage desktop: clear session: %v", err)
					}
				}()
			}
		},
		// The page's toasts cover the server it shows, not the others.
		Foreground: func(id string) bool { return h.foreground() && h.activeID() == id },
		Delivered: func(id string, n int64) {
			if id == h.activeID() {
				_ = NotificationDelivered.Broadcast(n)
			}
		},
		Changed: h.serversChanged,
		Logf:    log.Printf,
	})
	h.l = launch.New(launch.Config{
		CLI:        filepath.Join(res, "bin", "stage"),
		AppName:    appName,
		AppVersion: appVersion(),
		Dev:        h.dev,
		DevSocket:  devSocket,
		Servers:    servers,
		OnChange:   func(s launch.State) { _ = StateChanged.Broadcast(s) },
		OnTarget: func(t target.Target, health launch.Health) {
			previous := h.l.State().Server
			if !h.api.Target().Same(t) || (previous != nil && previous.SharedAccess != health.SharedAccess) {
				h.closeAttachments()
			}
			h.api.SetServer(t, health.SharedAccess)
		},
		// Every ready (first start, recovery, a new server) reconnects
		// that server's notification stream too.
		OnServer: func(t target.Target) {
			if t.Remote() || h.l.Worker() == nil {
				h.alerts.Connect(t)
			}
		},
		// Joining or leaving as a worker drops or restores this machine's
		// own notification stream.
		OnWorker: func(bool) { h.listChanged() },
		CheckSession: func(ctx context.Context, t target.Target) error {
			if sessions == nil {
				return nil
			}
			return sessions.Check(ctx, t)
		},
		GetShared: func(ctx context.Context, t target.Target, path string, v any) error {
			if sessions == nil {
				return errNotRunning
			}
			return launch.Get(ctx, t, path, v, sessions.RoundTrip)
		},
		OpenAtLogin:    mygo.App.OpenAtLogin,
		SetOpenAtLogin: mygo.App.SetOpenAtLogin,
		Logf:           log.Printf,
	})
	h.api.AccountEpoch = func() int { return h.l.State().AccountEpoch }
	h.alerts.SetServers(h.serverTargets())
	h.wire()
	return true
}

func appVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "dev"
}
