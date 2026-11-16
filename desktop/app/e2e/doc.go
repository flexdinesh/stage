// Package e2e drives the built desktop app through MyGo's automation
// plugin (tests under the e2e build tag) against a real `stage serve` on
// a temp socket, through the development socket override. It needs a
// webview: Linux with WebKitGTK 4.1 and a display (X11 or Wayland), or
// macOS. Run it with `mise run desktop:e2e`, which builds the UI first.
package e2e
