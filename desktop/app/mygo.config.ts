// MyGo configuration of the Stage desktop app (docs/desktop.md). The CLI
// evaluates it with Node (type stripping), so it imports nothing. IDENTITY
// picks the installation: `stage-dev` (default, "Stage Dev") or `stage`
// ("Stage"); the identity is also the deep-link scheme and the bundled
// CLI's identity (mise run dist and mise run release build both, docs/release.md).
const identity = process.env.IDENTITY ?? "stage-dev";
if (identity !== "stage" && identity !== "stage-dev") {
  throw new Error(`IDENTITY must be stage or stage-dev, not ${identity}`);
}
const stable = identity === "stage";
// Release builds on a Mac sign with a Developer ID and notarize the DMG
// (docs/release.md); without these, MyGo signs ad hoc (on macOS only).
const signingIdentity = process.env.MACOS_SIGNING_IDENTITY;
const notaryProfile = process.env.MACOS_NOTARY_PROFILE;

export default {
  name: stable ? "Stage" : "Stage Dev",
  slug: stable ? "stage-desktop" : "stage-dev-desktop",
  // Not io.github.flexdinesh.<id>: that is the service's launchd label, and
  // MyGo's macOS 12 login agent (~/Library/LaunchAgents/<identifier>.plist)
  // would overwrite the service's login copy.
  identifier: stable ? "io.github.flexdinesh.stage.desktop" : "io.github.flexdinesh.stage-dev.desktop",
  version: process.env.STAGE_VERSION ?? "0.0.0",
  license: "MIT",
  urlSchemes: [identity],
  // The UI (desktop/app/ui) builds into dist/, which main.go embeds and
  // serves itself through bridge B, so there is no frontendDist.
  devUrl: "http://localhost:5174",
  devCommand: "pnpm --filter stage-desktop dev",
  buildCommand: "pnpm --filter stage-desktop build",
  bindings: "ui/src/mygo.ts",
  out: "build",
  // MyGo's default Linux formats: the tarball with install.sh, deb, rpm and
  // a PKGBUILD (docs/release.md lists the release assets).
  linux: {
    comment: "Stage task queue for T3 agents",
    categories: ["Development"],
    // GTK 3 and WebKitGTK 4.1 are MyGo's own dependencies of every package.
  },
  macos: {
    minimumSystemVersion: "12.0",
    ...(signingIdentity ? { signingIdentity } : {}),
    ...(notaryProfile ? { notarize: { keychainProfile: notaryProfile } } : {}),
  },
};
