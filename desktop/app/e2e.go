//go:build e2e

package main

// The automation plugin lets e2e/ drive the built app over the socket
// MYGO_AUTOMATION names. Release builds leave it out.
import _ "github.com/egoist/mygo/plugins/automation"

func init() {
	// Records CSP violations from the first script on, for e2e/ to read.
	e2ePreload = `window.__stageCSP = [];
document.addEventListener("securitypolicyviolation", (e) => {
  window.__stageCSP.push(e.violatedDirective + " " + e.blockedURI);
});`
}
