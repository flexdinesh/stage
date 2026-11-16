// Desktop host: the shared app (@stage/app) in the MyGo webview. /api goes over the app's bridge to the active
// Stage server (this machine's Unix socket, or a saved remote over TCP); the shell owns the connection, server
// switching, links, clipboard and login startup (docs/desktop.md).
// No service worker and no Web Push: the Go process shows native notifications and owns the app badge.
import { StageApp } from "@stage/app";
import "@stage/app/index.css";
import type { Platform } from "@stage/app/platform";
import { createRoot } from "react-dom/client";
import { Desktop, events } from "./mygo.ts";
import { desktopClipboard } from "./platform/clipboard.ts";
import { desktopHost } from "./platform/host.ts";
import { desktopLinks } from "./platform/links.ts";
import type { DesktopNative, NativeNotifications, NativeServers } from "./platform/native.ts";
import { desktopNotifications } from "./platform/notifications.ts";
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

const nativeNotifications: NativeNotifications = {
  notificationState: () => Desktop.notificationState(),
  enableNotifications: () => Desktop.enableNotifications(),
  disableNotifications: () => Desktop.disableNotifications(),
  testNotification: () => Desktop.testNotification(),
  closeNotifications: (tag) => Desktop.closeNotifications(tag),
  syncNotifications: () => Desktop.syncNotifications(),
  openNotificationSettings: () => Desktop.openNotificationSettings(),
  onNotificationDelivered: (listener) => events.notificationsDelivered.on(() => listener()),
};

const nativeServers: NativeServers = {
  servers: () => Desktop.servers(),
  addServer: (input) => Desktop.addServer(input),
  updateServer: (id, input) => Desktop.updateServer(id, input),
  removeServer: (id) => Desktop.removeServer(id),
  switchServer: (id) => Desktop.switchServer(id),
  testServer: (url) => Desktop.testServer(url),
  onServers: (listener) => events.serversChanged.on(listener),
};

const host = desktopHost(native, nativeServers);
const platform: Platform = {
  transport: desktopTransport(),
  clipboard: desktopClipboard(native),
  links: desktopLinks(native, host.current),
  notifications: desktopNotifications(nativeNotifications),
  // The Go process sets the badge from the notification stream (one writer, also while the window is closed).
  attention: { setNeedsInput: () => {} },
  storage: desktopStorage(),
  host,
};

const root = document.getElementById("root");
if (root === null) throw new Error("#root element missing");

createRoot(root).render(<StageApp platform={platform} />);
