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

/** `command` is a copyable shell command that resumes the session; hosts never open it. */
export interface ExecutionLinks {native: string | null; web: string | null; command: string | null}
export interface Links {
 /** The host chooses an appropriate native or web execution destination. */
 executionDestination(links: ExecutionLinks): string | null;
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
 * - `unsupported`: no service worker / Push API / Notification API (native: no OS notification service)
 * - `ios-install`: iOS/iPadOS browser tab; Web Push needs the Home Screen app
 * - `blocked`: permission denied (only the OS/browser settings can undo it)
 * - `off` / `on`: whether this device receives notifications
 *
 * The native channel never reports `insecure` or `ios-install`.
 */
export type PushState = "insecure" | "unsupported" | "ios-install" | "blocked" | "off" | "on";

/** System notification delivery on this device. */
export interface NotificationDelivery {
  /**
   * How notifications reach this device. Absent means `web-push`.
   * - `web-push`: the server pushes to this browser's subscription
   * - `native`: the desktop host shows OS notifications from the server inbox
   */
  channel?: "web-push" | "native";
  state(): Promise<PushState>;
  /**
   * Call from a user gesture. Web Push: asks permission and registers this
   * device. Native: turns delivery on (also "try again" after `blocked`) and
   * shows a confirmation notification, which may trigger the OS permission prompt.
   */
  enable(): Promise<void>;
  disable(): Promise<void>;
  /** Web Push: asks the server to send a test notification to this device. Native: shows one locally, no server push. */
  test(): Promise<void>;
  /** Closes shown system notifications with `tag` (all when omitted). Best effort, never rejects. */
  closeShown(tag?: string): Promise<void>;
  /** Closes shown notifications for groups already viewed on any device. Best effort, never rejects. */
  syncShown(): Promise<void>;
  /** Runs `listener` whenever a notification is delivered to this device. Returns an unsubscribe. */
  onDelivered(listener: () => void): () => void;
  /** Opens the OS notification settings for this app. Resolves false when the host cannot; absent when it never can. */
  openSystemSettings?(): Promise<boolean>;
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
 * - `unreachable`: a remote server does not answer, or refuses this app (polling continues)
 * - `worker`: this machine is a worker of a main server (`HostState.worker`), so its own server is off until it leaves
 *
 * For a remote target (`HostState.target.kind === "remote"`) only `starting` (steps `server`, `api`),
 * `ready`, `reconnecting`, `unreachable` and `incompatible` occur: the host never manages a remote server. `worker`
 * occurs for this machine's target (`local`) only.
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
  | "incompatible"
  | "unreachable"
  | "worker";

export interface HostProblem {
  code: string;
  message: string;
  fix: string;
}

/** The last Stage server the host reached. */
export interface HostServer {
  /** The target (saved server) id it belongs to: `local` or a remote's. */
  id: string;
  version: string;
  apiVersion: number;
  identity: string;
  /** STAGE_PUBLIC_URL of the server, when configured. */
  publicUrl: string | null;
  /** Unix socket path the host talks to; "" for a remote. */
  socket: string;
  /** Supervised by the per-user service (false: started from a terminal). */
  managed: boolean;
  pid: number | null;
}

/** The Stage server the host connects to: this machine's (`local`) or a saved remote. */
export interface HostTarget {
  /** `local` for this machine; a saved remote's id otherwise. */
  id: string;
  /** "This machine" for local; the user's name for a remote. */
  name: string;
  kind: "local" | "remote";
  /** A remote's base URL (`http(s)://host[:port]`); "" for local. */
  url: string;
}

/**
 * Where this machine's worker stands.
 * - `starting`: the worker service is starting
 * - `online`: running and connected to its main server
 * - `offline`: joined but not connected (service stopped, or main server unreachable)
 * - `needs_update`: the main server speaks another API version
 * - `removed`: the main server removed this machine (its token is revoked)
 * - `failed`: the worker service failed
 */
export type HostWorkerStatus = "starting" | "online" | "offline" | "needs_update" | "removed" | "failed";

/** This machine as a worker of a main server: its harnesses run that server's tasks. */
export interface HostWorker {
  /** The main server's URL (from the worker config). */
  url: string;
  /** This machine's id on the main server. */
  machineId: number;
  /** The saved server that is this main server; null when none matches. */
  serverId: string | null;
  /** That server's name, else the URL's host. */
  serverName: string;
  status: HostWorkerStatus;
  /** Detail sentence, or "". */
  message: string;
  /** What to do about it, or "". */
  fix: string;
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
  /** The active server. A change of `target.id` is a server switch: the app's data belongs to another server. */
  target: HostTarget;
  /** Increments each time the connection becomes ready, so live streams reopen. */
  epoch: number;
  /** Readies of the active server; a switch resets it to 0 (the app was never ready on the new server yet). */
  serverEpoch: number;
  /** A retry, start or restart is running. */
  busy: boolean;
  /** Development run against a foreground dev server. */
  dev: boolean;
  /** This machine as a worker of a main server; null when it is not one. */
  worker: HostWorker | null;
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

/** One Stage server the host can connect to, as listed for switching. */
export interface HostServerEntry {
  id: string;
  name: string;
  kind: "local" | "remote";
  /** A remote's base URL; "" for local. */
  url: string;
  /** The host shows this server. */
  active: boolean;
  /** The server's needs-input count from its notification stream; null until known. */
  needsInput: number | null;
}

/** A remote server to save. */
export interface HostServerInput {
  name: string;
  /** `http(s)://host[:port]`, no path. */
  url: string;
}

/** Result of testing a server URL before saving it. */
export interface HostServerTest {
  /** Something answered at the URL (even a refusal). */
  reachable: boolean;
  /** Its `/api/health`; 0 / "" when it did not answer as a Stage server. */
  apiVersion: number;
  identity: string;
  version: string;
  /** Why the server is not usable; "" when it is. */
  error: string;
  /** What to do about `error`; "". */
  hint: string;
  /** A setting or command to copy that fixes it (`STAGE_ALLOWED_ORIGINS=<url>` for a refused hostname); "". */
  command: string;
}

/**
 * The servers a native host can switch between: this machine's (always first, not editable or removable)
 * and saved remotes on a trusted network (no auth). Methods reject with the host's message on invalid input.
 */
export interface HostServers {
  list(): Promise<HostServerEntry[]>;
  /** Saves a remote server; does not switch to it. */
  add(input: HostServerInput): Promise<HostServerEntry>;
  /** Renames a remote or changes its URL (the active one reconnects on a new URL). */
  update(id: string, input: HostServerInput): Promise<HostServerEntry>;
  /** Removes a remote (switching to this machine first when it is active). */
  remove(id: string): Promise<void>;
  /** Makes a server active; the connection state follows (`HostState.target`, `serverEpoch` 0). */
  switch(id: string): Promise<void>;
  /** Checks a URL without saving it. Never rejects for an unreachable server. */
  test(url: string): Promise<HostServerTest>;
  /** Runs `listener` with the new list on every change (edits, switches, needs-input counts). Returns an unsubscribe. */
  subscribe(listener: (servers: HostServerEntry[]) => void): () => void;
}

/**
 * This machine as a worker of a main server (one role per machine: its own server stays off while it is one).
 * Methods reject with the host's message.
 */
export interface HostWorkerControl {
  /**
   * Joins the active remote with `link`, a pairing link created on it: this machine's own server stops (its data
   * stays) and the worker starts. Login startup moves to the worker.
   */
  join(link: string): Promise<void>;
  /**
   * Stops the worker and leaves its main server, which removes this machine there. `local`: only forget it here
   * (the main server is unreachable or already removed it).
   */
  leave(options: { local: boolean }): Promise<void>;
  /** Restarts the worker onto the app's bundled version. */
  restart(): Promise<void>;
}

/** Host-owned services of a native shell. */
export interface HostServices {
  connection: HostConnection;
  loginStartup: LoginStartup;
  /** Switching between Stage servers; absent when the host connects to one server only. */
  servers?: HostServers;
  /** This machine as a worker of a remote server; absent when the host cannot run one. */
  worker?: HostWorkerControl;
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
