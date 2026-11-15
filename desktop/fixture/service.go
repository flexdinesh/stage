package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"sync/atomic"

	"github.com/egoist/mygo"
)

// Fixture is the Go service the probe page calls through the generated
// client (web/src/mygo.ts): what the native checks need, nothing of
// Stage's business.
type Fixture struct {
	bridge string
	win    *atomic.Pointer[mygo.Window]
	show   func()
}

// Info describes the running fixture for the probe's report.
type Info struct {
	Bridge                 string `json:"bridge"`
	GOOS                   string `json:"goos"`
	GOARCH                 string `json:"goarch"`
	MyGo                   string `json:"mygo"`
	NotificationsSupported bool   `json:"notificationsSupported"`
	PID                    int    `json:"pid"`
}

// Info reports the bridge, platform and MyGo version.
func (f *Fixture) Info() Info {
	return Info{
		Bridge:                 f.bridge,
		GOOS:                   runtime.GOOS,
		GOARCH:                 runtime.GOARCH,
		MyGo:                   mygoVersion(),
		NotificationsSupported: mygo.NotificationsSupported(),
		PID:                    os.Getpid(),
	}
}

// Notify shows a native notification; clicking it shows and focuses the
// window, also after the window was closed (hidden).
func (f *Fixture) Notify(title, body string) error { return notify(title, body, f.show) }

func notify(title, body string, show func()) error {
	n := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body, Group: "stage-fixture"})
	n.OnClick(show)
	return n.Show()
}

// testNotification is the menu and tray item: it works while the window
// is hidden, which the page's button cannot.
func testNotification(show func()) {
	go func() {
		if err := notify("Stage fixture", "Click to bring the window back", show); err != nil {
			log.Printf("fixture: notification: %v", err)
		}
	}()
}

// CloseWindow closes the window as its close button does: it hides.
func (f *Fixture) CloseWindow() {
	if w := f.win.Load(); w != nil {
		w.Close()
	}
}

// Quit quits the app, which ends its streams; the Stage server (or stub)
// keeps running.
func (f *Fixture) Quit() { mygo.App.Quit() }

func mygoVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, d := range info.Deps {
			if d.Path == "github.com/egoist/mygo" {
				return d.Version
			}
		}
	}
	return "unknown"
}

// startTray adds a tray icon with Show and Quit, where the desktop has a
// tray (AppIndicator on Linux); without one the app still runs.
func startTray(show func()) {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:      "stage-fixture",
		Icon:    trayIcon(),
		ToolTip: "Stage fixture",
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Show Window", Click: func(*mygo.MenuItem, *mygo.Window) { show() }},
			{Label: "Send Test Notification", Click: func(*mygo.MenuItem, *mygo.Window) { testNotification(show) }},
			{Label: "Quit Fixture", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
		}),
	})
	if err != nil {
		log.Printf("fixture: no tray: %v", err)
		return
	}
	_ = tray
}

// trayIcon is a 32x32 ink square, generated so the fixture carries no
// binary asset.
func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 4; y < 28; y++ {
		for x := 4; x < 28; x++ {
			img.Set(x, y, color.NRGBA{R: 0x1f, G: 0x23, B: 0x28, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Printf("fixture: tray icon: %v", err)
	}
	return buf.Bytes()
}
