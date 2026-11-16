// Desktop host: the shared app (@stage/app) in the MyGo webview. /api goes over the app's bridge to the Stage
// server's Unix socket; the shell owns the connection, links, clipboard and login startup (docs/desktop.md).
// No service worker and no Web Push: native notifications are a separate capability.
import { StageApp } from "@stage/app";
import "@stage/app/index.css";
import type { Platform } from "@stage/app/platform";
import { createRoot } from "react-dom/client";
import { Desktop, events } from "./mygo.ts";
import { desktopClipboard } from "./platform/clipboard.ts";
import { desktopHost } from "./platform/host.ts";
import { desktopLinks } from "./platform/links.ts";
import type { DesktopNative } from "./platform/native.ts";
import { desktopStorage } from "./platform/storage.ts";
import { desktopTransport } from "./platform/transport.ts";

const native: DesktopNative = {
  state: () => Desktop.state(),
  onState: (listener) => events.hostState.on(listener),
  onNavigate: (listener) => events.hostNavigate.on(listener),
  takeNavigation: () => Desktop.takeNavigation(),
  retry: () => Desktop.retry(),
  startServer: () => Desktop.startServer(),
  restartServer: () => Desktop.restartServer(),
  diagnostics: () => Desktop.diagnostics(),
  copyText: (text) => Desktop.copyText(text),
  openExternal: (url) => Desktop.openExternal(url),
  openApp: (url) => Desktop.openApp(url),
  loginStartup: () => Desktop.loginStartup(),
  setLoginStartup: (enabled) => Desktop.setLoginStartup(enabled),
};

const host = desktopHost(native);
const platform: Platform = {
  transport: desktopTransport(),
  clipboard: desktopClipboard(native),
  links: desktopLinks(native, host.current),
  attention: { setNeedsInput: () => {} },
  storage: desktopStorage(),
  host,
};

const root = document.getElementById("root");
if (root === null) throw new Error("#root element missing");

createRoot(root).render(<StageApp platform={platform} />);
