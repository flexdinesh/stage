// Command app is the Stage desktop client (docs/desktop.md): a MyGo
// webview hosting @stage/app (desktop/app/ui), which reaches the per-user
// Stage service over its Unix socket only, through bridge B. The bundled
// stage CLI (resources/<platform>/bin/stage) starts and inspects the
// service; package launch drives it, package nav polices URLs.
//
//	STAGE_DESKTOP_SOCKET=/run/user/1000/stage-desktopdev/x.sock
//	    development builds only (mise run desktop:dev): talk to this
//	    foreground dev server instead of the service
package main

import (
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"

	"github.com/flexdinesh/stage/desktop/app/bridge"
	"github.com/flexdinesh/stage/desktop/app/launch"
)

// Set by `mise run desktop:package` through MYGO_LDFLAGS
// (-X main.appName=Stage ...); the defaults are the developer app's.
var (
	appName       = "Stage Dev"
	appIdentifier = "io.github.flexdinesh.stage-dev.desktop"
	// appIdentity is the deep-link scheme until the bundled CLI reports
	// its identity (they match in a packaged app: mygo.config.ts urlSchemes).
	appIdentity = "stage-dev"
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

func main() {
	ui, err := fs.Sub(dist, "dist")
	if err != nil {
		log.Fatal(err)
	}
	api := bridge.NewAPIProxy("")
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
			log.Print("stage desktop: the UI is not built (desktop/app/dist); run `pnpm --filter stage-desktop build`")
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
	h.l = launch.New(launch.Config{
		CLI:            filepath.Join(res, "bin", "stage"),
		AppName:        appName,
		AppVersion:     appVersion(),
		Dev:            h.dev,
		DevSocket:      devSocket,
		OnChange:       func(s launch.State) { _ = StateChanged.Broadcast(s) },
		OnSocket:       h.api.SetSocket,
		OpenAtLogin:    mygo.App.OpenAtLogin,
		SetOpenAtLogin: mygo.App.SetOpenAtLogin,
		Logf:           log.Printf,
	})
	h.wire()
	return true
}

func appVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "dev"
}
