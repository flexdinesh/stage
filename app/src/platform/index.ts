// Platform contract: everything the shared app needs from its host (the
// browser/PWA and the MyGo desktop shell). Product code reaches these through
// usePlatform()/useApi(); it never touches the underlying browser APIs.
// Hosts implement each part; optional parts are capabilities a host may lack.
// docs/architecture.md describes the boundaries.

/** Paths are Stage API paths relative to the API root, e.g. `/tasks/12`. */
export interface Transport {
  /** One HTTP request. Resolves with the response for any status; rejects only when the request could not be made. */
  request(path: string, init: RequestInit): Promise<Response>;
  /** URL the page can load directly (an `<img src>` or a link), for immutable assets such as attachments. */
  assetUrl(path: string): string;
  /** Opens a server-sent event stream. The host owns reconnection (resuming from the last event id). */
  subscribe(path: string, handlers: StreamHandlers): Subscription;
}

export interface StreamHandlers {
  open?: () => void;
  /** The stream dropped. `closed`: it will not reconnect (the server refused it). */
  error?: (closed: boolean) => void;
  /** Named events and their raw `data`, e.g. `{ item: (data) => ... }`. */
  events: Record<string, (data: string) => void>;
}

export interface Subscription {
  /** Stops the stream; no handler runs afterwards. */
  close(): void;
}

export interface Clipboard {
  writeText(text: string): Promise<void>;
}

export interface Links {
  /**
   * The copyable Stage link for an app path (`/tasks/12`, `/sessions/<id>`):
   * the page's own address in a browser, the public URL or a `stage://` deep
   * link on desktop. Agents (`stage show`, MCP `read_ref`) parse every form.
   */
  stageLink(path: string): string;
  /** Opens an external web page outside the app. */
  openExternal(url: string): void;
  /** Hands an app link (t3code://) to the OS. */
  openApp(url: string): void;
}

/**
 * - `insecure`: not a secure context (plain HTTP on a non-localhost origin); push needs HTTPS or localhost
 * - `unsupported`: no service worker / Push API / Notification API
 * - `ios-install`: iOS/iPadOS browser tab; Web Push needs the Home Screen app
 * - `blocked`: permission denied (only the OS/browser settings can undo it)
 * - `off` / `on`: whether this device receives notifications
 */
export type PushState = "insecure" | "unsupported" | "ios-install" | "blocked" | "off" | "on";

/** System notification delivery on this device. */
export interface NotificationDelivery {
  state(): Promise<PushState>;
  /** Call from a user gesture: asks permission and registers this device. */
  enable(): Promise<void>;
  disable(): Promise<void>;
  /** Asks the server to send a test notification to this device. */
  test(): Promise<void>;
  /** Closes shown system notifications with `tag` (all when omitted). Best effort, never rejects. */
  closeShown(tag?: string): Promise<void>;
  /** Closes shown notifications for groups already viewed on any device. Best effort, never rejects. */
  syncShown(): Promise<void>;
  /** Runs `listener` whenever a notification is delivered to this device. Returns an unsubscribe. */
  onDelivered(listener: () => void): () => void;
}

/** Ambient needs-input signal outside the page (tab icon, app badge). */
export interface Attention {
  /** `undefined` while the count is unknown. */
  setNeedsInput(count: number | undefined): void;
}

/** Per-device preferences. Never throws: unavailable storage reads as null and drops writes. */
export interface PreferenceStorage {
  get(key: string): string | null;
  /** `null` removes the key. */
  set(key: string, value: string | null): void;
}

/**
 * Where a host-managed connection to the Stage server stands. Only hosts that
 * start and supervise the connection themselves (desktop) provide one.
 * - `starting`: ensuring the per-user service and waiting for readiness
 * - `ready`: the server answers with a compatible API
 * - `reconnecting`: it was ready and stopped answering; retrying
 * - `stopped`: not running (after the reconnect window, or stopped on purpose)
 * - `failed`: the service or server failed (exited, crash loop, unhealthy)
 * - `unsupported`: no usable per-user service manager on this machine
 * - `misconfigured`: invalid configuration, permissions or app location
 * - `conflict`: another (terminal) server owns the data and cannot be used
 * - `incompatible`: the server speaks another API version
 */
export type HostPhase =
  | "starting"
  | "ready"
  | "reconnecting"
  | "stopped"
  | "failed"
  | "unsupported"
  | "misconfigured"
  | "conflict"
  | "incompatible";

export interface HostProblem {
  code: string;
  message: string;
  fix: string;
}

/** The last Stage server the host reached. */
export interface HostServer {
  version: string;
  apiVersion: number;
  identity: string;
  /** STAGE_PUBLIC_URL of the server, when configured. */
  publicUrl: string | null;
  /** Unix socket path the host talks to. */
  socket: string;
  /** Supervised by the per-user service (false: started from a terminal). */
  managed: boolean;
  pid: number | null;
}

export interface HostState {
  phase: HostPhase;
  /** Progress while starting. */
  step: "service" | "server" | "api" | null;
  /** One-line headline for the phase ("Stage server failed to start"). */
  title: string;
  /** Detail sentence, or "". */
  message: string;
  /** What to do about it, or "". */
  fix: string;
  /** A shell command that fixes or works around it, or "". */
  command: string;
  problems: HostProblem[];
  /** Last server log lines, oldest first. */
  logTail: string[];
  app: { name: string; version: string; identity: string; apiVersion: number };
  server: HostServer | null;
  /** Connected to a server started from a terminal, not the service. */
  foreground: boolean;
  /** Server build differs from the app's bundled server. */
  versionSkew: boolean;
  /** Increments each time the connection becomes ready, so live streams reopen. */
  epoch: number;
  /** A retry, start or restart is running. */
  busy: boolean;
  /** Development run against a foreground dev server. */
  dev: boolean;
}

export interface HostConnection {
  /** The latest state; the same object until it changes. */
  current(): HostState;
  /** Runs `listener` on every change. Returns an unsubscribe. */
  subscribe(listener: (state: HostState) => void): () => void;
  retry(): Promise<void>;
  startServer(): Promise<void>;
  /** Restarts the service onto the app's bundled server. */
  restartServer(): Promise<void>;
  /** Plain-text report for support: versions, status, problems, log tail. */
  diagnostics(): Promise<string>;
}

/** Start the app (and the Stage service) at login. */
export interface LoginStartup {
  get(): Promise<boolean>;
  set(enabled: boolean): Promise<void>;
}

/** Host-owned services of a native shell. */
export interface HostServices {
  connection: HostConnection;
  loginStartup: LoginStartup;
  /** Paths the host asks the app to show (deep links, menu items). Returns an unsubscribe. */
  onNavigate(listener: (path: string) => void): () => void;
}

export interface Platform {
  transport: Transport;
  clipboard: Clipboard;
  links: Links;
  /** System notification delivery; absent when the host has none (yet). */
  notifications?: NotificationDelivery;
  attention: Attention;
  storage: PreferenceStorage;
  /** Native shell services; absent in the browser. */
  host?: HostServices;
}
