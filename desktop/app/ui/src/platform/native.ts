import type { HostServerEntry, HostServerInput, HostServerTest, HostState } from "@stage/app/platform";

/**
 * The Go side of the desktop app as the adapters use it: the generated MyGo client (src/mygo.ts) wired in
 * main.tsx, or a fake in tests and the dev preview.
 */
export interface DesktopNative {
  state(): Promise<HostState>;
  /** Every state change. Returns an unsubscribe. */
  onState(listener: (state: HostState) => void): () => void;
  /** App paths the shell asks to show (deep links, a second launch). Returns an unsubscribe. */
  onNavigate(listener: (path: string) => void): () => void;
  /** The deep link the app was launched with, once; "" when none. */
  takeNavigation(): Promise<string>;
  retry(): Promise<void>;
  startServer(): Promise<void>;
  restartServer(): Promise<void>;
  diagnostics(): Promise<string>;
  copyText(text: string): Promise<void>;
  openExternal(url: string): Promise<void>;
  openApp(url: string): Promise<void>;
  loginStartup(): Promise<boolean>;
  setLoginStartup(enabled: boolean): Promise<void>;
}

/** The Go side's worker calls (desktop/app/launch/worker.go), as the host adapter uses them. */
export interface NativeWorker {
  joinWorker(link: string): Promise<void>;
  leaveWorker(local: boolean): Promise<void>;
  restartWorker(): Promise<void>;
}

/** The Go side's server list and switching (desktop/app/servers.go), as the host adapter uses it. */
export interface NativeServers {
  servers(): Promise<HostServerEntry[]>;
  addServer(input: HostServerInput): Promise<HostServerEntry>;
  updateServer(id: string, input: HostServerInput): Promise<HostServerEntry>;
  removeServer(id: string): Promise<void>;
  switchServer(id: string): Promise<void>;
  testServer(url: string): Promise<HostServerTest>;
  /** Every list change (servers:changed). Returns an unsubscribe. */
  onServers(listener: (servers: HostServerEntry[]) => void): () => void;
}

/** The Go side's native notifications (desktop/app/alerts), as the notifications adapter uses it. */
export interface NativeNotifications {
  /** "unsupported" | "off" | "blocked" | "on"; anything else reads as unsupported. */
  notificationState(): Promise<string>;
  enableNotifications(): Promise<void>;
  disableNotifications(): Promise<void>;
  testNotification(): Promise<void>;
  /** Closes the notification shown for `tag` (task-12, session-x); all of them for "". */
  closeNotifications(tag: string): Promise<void>;
  syncNotifications(): Promise<void>;
  /** False when the system has no known notification settings to open. */
  openNotificationSettings(): Promise<boolean>;
  /** Each delivery from the server's notification stream, shown or not. Returns an unsubscribe. */
  onNotificationDelivered(listener: () => void): () => void;
}

/** Before the shell's first answer: starting, nothing known yet. */
export const INITIAL_STATE: HostState = {
  phase: "starting",
  step: null,
  title: "Starting Stage",
  message: "",
  fix: "",
  command: "",
  problems: [],
  logTail: [],
  app: { name: "Stage", version: "", identity: "", apiVersion: 0 },
  server: null,
  foreground: false,
  versionSkew: false,
  target: { id: "local", name: "This machine", kind: "local", url: "" },
  epoch: 0,
  serverEpoch: 0,
  busy: false,
  dev: false,
  worker: null,
};
