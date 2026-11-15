// Package e2e drives the built fixture app through MyGo's automation
// plugin (tests under the e2e build tag). It needs a webview: Linux with
// WebKitGTK 4.1 and a display (X11 or Wayland), or macOS. Run it with
// `mise run fixture:e2e`, which builds the probe page first. See
// ../CHECKS.md.
package e2e
