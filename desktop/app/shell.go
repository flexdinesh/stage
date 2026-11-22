package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"github.com/flexdinesh/stage/desktop/app/alerts"
	"github.com/flexdinesh/stage/desktop/app/bridge"
	"github.com/flexdinesh/stage/desktop/app/launch"
	"github.com/flexdinesh/stage/desktop/app/nav"
	"github.com/flexdinesh/stage/desktop/app/target"
)

// e2ePreload is injected into the main page by e2e builds (e2e.go), ""
// otherwise.
var e2ePreload string

// host is the native shell: the main window, its lifecycle, menus, tray,
// links and deep links.
type host struct {
	api       *bridge.APIProxy
	l         *launch.Launcher
	alerts    *alerts.Service
	servers   *target.Store
	dev       bool
	devOrigin string // MYGO_DEV_URL in a development build

	userData, logs string

	win      atomic.Pointer[mygo.Window]
	quitting atomic.Bool

	mu         sync.Mutex
	pending    string // deep link path the UI has not taken yet
	childURL   string // URL of the attachment window being opened
	noticeOnce sync.Once
}

// paths keeps the app's data, cache and logs in identifier-named dirs:
// MyGo's defaults are named after the app ("Stage"), and on macOS
// ~/Library/Application Support/Stage would be the Stage data dir
// (~/Library/Application Support/stage) on a case-insensitive disk.
func (h *host) paths() {
	id := appIdentifier
	if h.dev {
		id += ".dev" // never share an installed app's data
	}
	cfg, _ := os.UserConfigDir()
	cache, _ := os.UserCacheDir()
	h.userData = filepath.Join(cfg, id)
	h.logs = filepath.Join(h.userData, "logs")
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		h.logs = filepath.Join(home, "Library", "Logs", id)
	}
	mygo.App.SetPath(mygo.PathUserData, h.userData)
	mygo.App.SetPath(mygo.PathCache, filepath.Join(cache, id))
	mygo.App.SetPath(mygo.PathLogs, h.logs)
}

// wire registers the app's listeners, before App.Run.
func (h *host) wire() {
	mygo.App.OnSecondInstance(func([]string, string) { h.show() })
	mygo.App.OnActivate(func(bool) { h.show() }) // Dock icon (macOS)
	// A second instance started with a URL fires OnSecondInstance and
	// OnOpenURL: both show, and navigating twice to one path is harmless.
	mygo.App.OnOpenURL(h.openURL)
	// A notification click, also the one that launched the app (macOS
	// relaunches it from Notification Center): switch to its server, then
	// show its target.
	mygo.App.OnNotificationClick(h.clickNotification)
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		h.quitting.Store(true)
		// Quit ends native notifications; keep the cursor for the next run.
		if err := h.alerts.Flush(); err != nil {
			log.Printf("stage desktop: notifications: %v", err)
		}
	})
	mygo.App.OnQuitCanceled(func() { h.quitting.Store(false) })
	// With a listener, closing the last window does not quit: the app
	// stays resident until an explicit Quit.
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnWindowCreated(h.windowCreated)
	mygo.App.SetMenu(h.menu())
	mygo.App.WhenReady(h.ready)
}

// ready creates the window and starts the launcher, once the app runs.
func (h *host) ready() {
	for _, d := range []string{h.userData, h.logs} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			log.Printf("stage desktop: %v", err)
		}
	}
	h.openLog()
	devTools := mygo.DevToolsDisabled
	if h.dev {
		devTools = mygo.DevToolsEnabled
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: appName, URL: "/",
		Width: 1200, Height: 800, MinWidth: 720, MinHeight: 520,
		StateKey: "main",
		// A login launch stays resident without a window.
		Hidden:          mygo.App.WasOpenedAtLogin(),
		BackgroundColor: "light-dark(#ffffff, #1a1d23)",
		Page:            mygo.PageOptions{DevTools: devTools, PreloadScript: e2ePreload},
	})
	page := w.Page()
	page.OnWillNavigate(h.willNavigate)
	page.SetWindowOpenHandler(h.openWindow)
	// The page asks for nothing: notifications are native (never the web
	// Notification API or Web Push), and no camera, microphone or location.
	page.SetPermissionHandler(func(req mygo.PermissionRequest) bool {
		log.Printf("stage desktop: denied permission %v for %s", req.Permissions, req.Origin)
		return false
	})
	w.OnClose(func(e *mygo.CloseEvent) {
		if h.quitting.Load() {
			return
		}
		e.PreventDefault()
		w.Hide()
		if runtime.GOOS == "linux" {
			go h.closeNotice()
		}
	})
	h.win.Store(w)
	h.startTray()
	h.alerts.Start() // before the launcher, whose ready connects it
	h.l.Start()
	if runtime.GOOS == "linux" && !h.dev {
		go h.registerScheme()
	}
}

func (h *host) show() {
	if w := h.win.Load(); w != nil {
		w.Show()
		w.Restore()
		w.Focus()
	}
}

// foreground reports whether the main window is visible and focused: then
// the page's toasts show new notifications, not the OS.
func (h *host) foreground() bool {
	w := h.win.Load()
	return w != nil && w.IsVisible() && w.IsFocused()
}

// bundleID is the app's bundle identifier (`mygo dev` appends .dev).
func (h *host) bundleID() string {
	if h.dev {
		return appIdentifier + ".dev"
	}
	return appIdentifier
}

// identity is the deep-link scheme: the bundled CLI's identity once read.
func (h *host) identity() string {
	if h.l != nil {
		if id := h.l.Identity(); id != "" {
			return id
		}
	}
	return appIdentity
}

func (h *host) policy() nav.Policy {
	p := nav.Policy{Identity: h.identity()}
	if h.dev && h.devOrigin != "" {
		p.DevOrigin = nav.DevOriginOf(h.devOrigin)
	}
	return p
}

// registerScheme registers the deep-link scheme with the desktop (Linux
// release builds; packages declare it too, a tarball install does not).
func (h *host) registerScheme() {
	deadline := time.Now().Add(time.Minute)
	for h.l.State().App.Identity == "" && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if err := mygo.App.RegisterURLScheme(h.identity()); err != nil {
		log.Printf("stage desktop: register %s://: %v", h.identity(), err)
	}
}

// openURL handles a deep link the OS handed over.
func (h *host) openURL(raw string) {
	path, ok := h.policy().DeepLink(raw)
	if !ok {
		log.Printf("stage desktop: ignored link %q", raw)
		h.show()
		return
	}
	h.navigate(path)
}

// navigate shows an app path in the main window.
func (h *host) navigate(path string) {
	h.mu.Lock()
	h.pending = path
	h.mu.Unlock()
	_ = Navigate.Broadcast(path)
	h.show()
}

func (h *host) takeNavigation() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.pending
	h.pending = ""
	return p
}

// willNavigate keeps the main frame on the app's own pages.
func (h *host) willNavigate(e *mygo.NavigateEvent) {
	d := h.policy().Navigate(e.URL)
	if d.Action == nav.Allow {
		return
	}
	e.PreventDefault()
	h.act(d)
}

// openWindow decides window.open() and target=_blank: attachments open in
// a child window without bindings, the rest per the policy.
func (h *host) openWindow(req mygo.WindowOpenRequest) *mygo.WindowOptions {
	d := h.policy().Open(req.URL)
	if d.Action != nav.Child {
		h.act(d)
		return nil
	}
	if !req.UserInitiated {
		log.Printf("stage desktop: denied pop-up %q", req.URL)
		return nil
	}
	h.mu.Lock()
	h.childURL = req.URL
	h.mu.Unlock()
	return &mygo.WindowOptions{
		Title: appName, URL: req.URL, Width: 900, Height: 700, MinWidth: 320, MinHeight: 240,
		Page: mygo.PageOptions{Untrusted: true, DevTools: mygo.DevToolsDisabled},
	}
}

// windowCreated locks an attachment window to its attachment.
func (h *host) windowCreated(w *mygo.Window) {
	if w == h.win.Load() {
		return
	}
	h.mu.Lock()
	cur := h.childURL
	h.childURL = ""
	h.mu.Unlock()
	if cur == "" {
		return
	}
	w.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
		d := h.policy().ChildNavigate(cur, e.URL)
		if d.Action == nav.Allow {
			return
		}
		e.PreventDefault()
		h.act(d)
	})
	w.Page().SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
		h.act(h.policy().ChildNavigate(cur, req.URL))
		return nil
	})
}

// act carries out a decision that is not a plain load.
func (h *host) act(d nav.Decision) {
	switch d.Action {
	case nav.External:
		go func() {
			if err := mygo.Shell.OpenExternal(d.URL); err != nil {
				log.Printf("stage desktop: open %q: %v", d.URL, err)
			}
		}()
	case nav.OpenApp:
		go func() {
			if err := h.openApp(d.URL); err != nil {
				log.Printf("stage desktop: open %q: %v", d.URL, err)
			}
		}()
	case nav.InApp:
		h.navigate(d.Path)
	case nav.Allow, nav.Child:
	default:
		log.Printf("stage desktop: denied %q: %s", d.URL, d.Reason)
	}
}

// openApp hands a t3code:// link to the OS; without a T3 app, it opens
// the thread's T3 web page.
func (h *host) openApp(raw string) error {
	err := mygo.Shell.OpenExternal(raw)
	if err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pub, perr := h.l.T3PublicURL(ctx)
	if perr != nil {
		return fmt.Errorf("%w (no web fallback: %v)", err, perr)
	}
	web, werr := nav.T3WebURL(pub, raw)
	if werr != nil {
		return fmt.Errorf("%w (no web fallback: %v)", err, werr)
	}
	return mygo.Shell.OpenExternal(web)
}

// closeNotice tells a Linux user once, at the first close, that the app
// stays running (GNOME may show no tray icon).
func (h *host) closeNotice() {
	marker := filepath.Join(h.userData, "close-notice-shown")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	h.noticeOnce.Do(func() {
		if err := os.WriteFile(marker, []byte("1\n"), 0o600); err != nil {
			log.Printf("stage desktop: %v", err)
		}
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Type:    mygo.MessageInfo,
			Title:   appName,
			Message: appName + " keeps running.",
			Detail:  "Reopen it from the tray or your app launcher.",
			Buttons: []string{"Keep running", "Quit " + appName},
		})
		if err == nil && res.Button == 1 {
			mygo.App.Quit()
		}
	})
}

func (h *host) menu() *mygo.Menu {
	click := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { fn() }
	}
	quit := &mygo.MenuItem{Label: "Quit " + appName, Accelerator: "CmdOrCtrl+Q", Click: click(mygo.App.Quit)}
	var items []*mygo.MenuItem
	if runtime.GOOS == "darwin" {
		items = append(items, &mygo.MenuItem{Label: appName, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout}, mygo.Separator(),
			{Role: mygo.RoleServices}, mygo.Separator(),
			{Role: mygo.RoleHide}, {Role: mygo.RoleHideOthers}, {Role: mygo.RoleUnhide}, mygo.Separator(),
			quit,
		}})
	} else {
		items = append(items, &mygo.MenuItem{Label: "File", Submenu: []*mygo.MenuItem{quit}})
	}
	view := []*mygo.MenuItem{
		{Label: "Reload", Accelerator: "CmdOrCtrl+R", Click: click(func() {
			if w := h.win.Load(); w != nil {
				w.Page().Reload()
			}
		})},
		mygo.Separator(),
		{Role: mygo.RoleResetZoom}, {Role: mygo.RoleZoomIn}, {Role: mygo.RoleZoomOut},
		mygo.Separator(),
		{Role: mygo.RoleToggleFullScreen},
	}
	if h.dev {
		view = append(view, mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleToggleDevTools})
	}
	back, forward := "Alt+Left", "Alt+Right"
	if runtime.GOOS == "darwin" {
		back, forward = "Cmd+[", "Cmd+]"
	}
	page := func(fn func(*mygo.Page)) func(*mygo.MenuItem, *mygo.Window) {
		return click(func() {
			if w := h.win.Load(); w != nil {
				fn(w.Page())
			}
		})
	}
	goMenu := []*mygo.MenuItem{
		{Label: "Back", Accelerator: back, Click: page((*mygo.Page).GoBack)},
		{Label: "Forward", Accelerator: forward, Click: page((*mygo.Page).GoForward)},
	}
	items = append(items,
		&mygo.MenuItem{Role: mygo.RoleEditMenu},
		&mygo.MenuItem{Label: "View", Submenu: view},
		&mygo.MenuItem{Label: "Go", Submenu: goMenu},
		&mygo.MenuItem{Label: "Window", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleMinimize},
			{Label: "Close", Accelerator: "CmdOrCtrl+W", Click: func(_ *mygo.MenuItem, w *mygo.Window) {
				if w == nil {
					w = h.win.Load()
				}
				if w != nil {
					w.Close()
				}
			}},
		}},
		&mygo.MenuItem{Label: "Help", Submenu: []*mygo.MenuItem{
			{Label: "Copy Diagnostics", Click: click(func() { go h.copyDiagnostics() })},
			{Label: "Show Logs", Click: click(func() { go h.showLogs() })},
		}},
	)
	return mygo.NewMenu(items)
}

func (h *host) copyDiagnostics() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mygo.Clipboard.WriteText(h.l.Diagnostics(ctx))
}

// showLogs writes the recent server log next to the app's own log and
// opens the folder.
func (h *host) showLogs() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := os.WriteFile(filepath.Join(h.logs, "diagnostics.txt"), []byte(h.l.Diagnostics(ctx)), 0o600); err != nil {
		log.Printf("stage desktop: %v", err)
	}
	if err := mygo.Shell.OpenPath(h.logs); err != nil {
		log.Printf("stage desktop: open %s: %v", h.logs, err)
	}
}

// openLog sends the app's log to <logs>/desktop.log too, restarting it
// past 1 MiB.
func (h *host) openLog() {
	p := filepath.Join(h.logs, "desktop.log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(p); err == nil && fi.Size() > 1<<20 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(p, flags, 0o600)
	if err != nil {
		log.Printf("stage desktop: log file: %v", err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

// startTray adds a tray icon with Show and Quit where the desktop has a
// tray (AppIndicator on Linux); without one the app still runs.
func (h *host) startTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:      "stage",
		Icon:    trayIcon(),
		ToolTip: appName,
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Show " + appName, Click: func(*mygo.MenuItem, *mygo.Window) { h.show() }},
			mygo.Separator(),
			{Label: "Quit " + appName, Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		}),
	})
	if err != nil {
		log.Printf("stage desktop: no tray: %v", err)
		return
	}
	tray.OnClick(h.show)
}

// trayIcon is a 32x32 ink square with a light bar, generated so the app
// carries no binary asset.
func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := color.NRGBA{R: 0x1f, G: 0x23, B: 0x28, A: 0xff}
	paper := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	for y := 4; y < 28; y++ {
		for x := 4; x < 28; x++ {
			c := ink
			if y >= 14 && y < 18 && x >= 9 && x < 23 {
				c = paper
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Printf("stage desktop: tray icon: %v", err)
	}
	return buf.Bytes()
}
