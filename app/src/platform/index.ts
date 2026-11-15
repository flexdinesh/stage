// Platform contract: everything the shared app needs from its host (browser
// today, the MyGo desktop shell later). Product code reaches these through
// usePlatform()/useApi(); it never touches the underlying browser APIs.
// Hosts implement each part; docs/architecture.md describes the boundaries.

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
  /** Origin copied Stage links point at (task and session URLs). */
  appOrigin(): string;
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

export interface Platform {
  transport: Transport;
  clipboard: Clipboard;
  links: Links;
  notifications: NotificationDelivery;
  attention: Attention;
  storage: PreferenceStorage;
}
