// Command fixture is the MyGo integration fixture of the Stage desktop
// shell (docs/adr/0001-desktop-shell.md): a webview that reaches a Stage
// server (or the stub, cmd/stagestub) over a Unix domain socket only,
// through one of two candidate bridges, and exercises the native
// behaviors the shell needs. It is internal groundwork, not a product.
//
//	STAGE_FIXTURE_SOCKET=/tmp/sf/stage.sock  the Stage socket (required)
//	STAGE_FIXTURE_BRIDGE=scheme|fetch        the bridge (required)
//	STAGE_FIXTURE_UI_DIR=apps/web/dist       scheme only: serve this
//	                                         frontend (the real Stage UI)
//	                                         instead of the probe page
//	STAGE_FIXTURE_AUTORUN=1                  run the probe's checks on load
package main

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/fetch"

	"github.com/flexdinesh/stage/desktop/fixture/bridge"
)

// dist is the probe page, built by `mise run fixture:e2e` (web/).
//
//go:embed all:dist
var dist embed.FS

type config struct {
	socket, bridge, uiDir string
	autorun               bool
}

// generating is set while `mygo generate` (and `mygo build`) runs the app
// to write the TypeScript client: App.Run then returns without a window,
// so main must reach it without a socket.
var generating = os.Getenv("MYGO_GENERATE") != ""

func loadConfig() (config, error) {
	if generating {
		return config{socket: "/nonexistent.sock", bridge: "scheme"}, nil
	}
	c := config{
		socket:  os.Getenv("STAGE_FIXTURE_SOCKET"),
		bridge:  os.Getenv("STAGE_FIXTURE_BRIDGE"),
		uiDir:   os.Getenv("STAGE_FIXTURE_UI_DIR"),
		autorun: os.Getenv("STAGE_FIXTURE_AUTORUN") == "1",
	}
	if c.socket == "" || !filepath.IsAbs(c.socket) {
		return c, errors.New("STAGE_FIXTURE_SOCKET must be an absolute socket path")
	}
	switch c.bridge {
	case "scheme":
	case "fetch":
		if c.uiDir != "" {
			return c, errors.New("STAGE_FIXTURE_UI_DIR needs the scheme bridge: the real UI uses relative /api, EventSource and <img>")
		}
	default:
		return c, fmt.Errorf("STAGE_FIXTURE_BRIDGE must be scheme or fetch, not %q", c.bridge)
	}
	return c, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal("fixture: ", err)
	}
	probe, err := fs.Sub(dist, "dist")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := fs.Stat(probe, "index.html"); err != nil && cfg.uiDir == "" && !generating {
		log.Fatal("fixture: the probe page is not built: run `mise run fixture:e2e` or `pnpm build` in desktop/fixture/web")
	}
	mygo.SetFrontend(probe)

	switch cfg.bridge {
	case "fetch":
		// Bridge A: @mygo-plugins/fetch requests http://stage.invalid/api/*.
		mygo.Use(fetch.New(fetch.Options{Client: bridge.UnixClient(cfg.socket), Allow: bridge.Allow}))
	case "scheme":
		// Bridge B: the page's own scheme serves the frontend and /api/*.
		ui := probe
		if cfg.uiDir != "" {
			ui = os.DirFS(cfg.uiDir)
		}
		if err := mygo.Protocol.Handle("mygo", bridge.Scheme(bridge.NewAPIProxy(cfg.socket), mygo.FileServer(ui))); err != nil {
			log.Fatal(err)
		}
	}

	if !generating && !mygo.App.RequestSingleInstanceLock() {
		// Another instance owns the lock and was handed our arguments; it
		// shows its window (OnSecondInstance).
		log.Print("fixture: another instance is running; it shows its window")
		return
	}

	var win atomic.Pointer[mygo.Window]
	var quitting atomic.Bool
	show := func() {
		if w := win.Load(); w != nil {
			w.Show()
			w.Focus()
		}
	}
	mygo.Bind(&Fixture{bridge: cfg.bridge, win: &win, show: show})

	mygo.App.OnSecondInstance(func([]string, string) { show() })
	mygo.App.OnNotificationClick(func(string) { show() }) // clicks that relaunched the app (macOS)
	mygo.App.OnActivate(func(bool) { show() })            // Dock icon (macOS)
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting.Store(true) })
	mygo.App.OnQuitCanceled(func() { quitting.Store(false) })
	// With a listener here, closing the last window does not quit: the app
	// stays for native notifications until an explicit Quit.
	mygo.App.OnWindowAllClosed(func() {})

	mygo.App.SetMenu(appMenu(show))

	mygo.App.WhenReady(func() {
		url := "/?bridge=" + cfg.bridge
		if cfg.autorun {
			url += "&autorun=1"
		}
		if cfg.uiDir != "" {
			url = "/"
		}
		w := mygo.NewWindow(mygo.WindowOptions{Title: "Stage fixture", URL: url, Width: 1000, Height: 760})
		// Close hides: the process stays for notifications. Quit closes.
		w.OnClose(func(e *mygo.CloseEvent) {
			if !quitting.Load() {
				e.PreventDefault()
				w.Hide()
			}
		})
		win.Store(w)
		startTray(show)
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func appMenu(show func()) *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "Fixture", Submenu: []*mygo.MenuItem{
			{Label: "Show Window", Click: func(*mygo.MenuItem, *mygo.Window) { show() }},
			{Label: "Send Test Notification", Click: func(*mygo.MenuItem, *mygo.Window) { testNotification(show) }},
			{Label: "Close Window", Accelerator: "CmdOrCtrl+W", Click: func(_ *mygo.MenuItem, w *mygo.Window) {
				if w != nil {
					w.Close()
				}
			}},
			mygo.Separator(),
			{Label: "Quit Fixture", Accelerator: "CmdOrCtrl+Q", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		}},
	})
}
