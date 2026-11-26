import type { Api, PushSubscriptionBody } from "@stage/app";
import type { NotificationDelivery, PushState } from "@stage/app/platform";

/** Server endpoints Web Push uses. */
export type PushApi = Pick<Api, "pushConfig" | "subscribePush" | "unsubscribePush" | "testPush" | "unreadNotificationTags">;

/** Browser facts the push state derives from. */
export interface PushEnv {
  secure: boolean;
  ios: boolean;
  standalone: boolean;
  supported: boolean;
  permission: NotificationPermission;
}

/**
 * Push state without the subscription lookup; null means permission is
 * granted, so `on`/`off` depends on whether a subscription exists. An insecure
 * context comes first: browsers hide the push APIs there, and installing to
 * the Home Screen would not help.
 */
export function pushStateFor(env: PushEnv): Exclude<PushState, "on"> | null {
  if (!env.secure) return "insecure";
  if (env.ios && !env.standalone) return "ios-install";
  if (!env.supported) return "unsupported";
  if (env.permission === "denied") return "blocked";
  if (env.permission !== "granted") return "off";
  return null;
}

function isIos(): boolean {
  const ua = navigator.userAgent;
  // iPadOS reports a desktop Mac UA; touch points tell them apart.
  return /iPad|iPhone|iPod/.test(ua) || (ua.includes("Macintosh") && navigator.maxTouchPoints > 1);
}

function isStandalone(): boolean {
  if (window.matchMedia("(display-mode: standalone)").matches) return true;
  return "standalone" in navigator && navigator.standalone === true;
}

const supported = () => "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;

/** The active registration, or null when /sw.js was never registered (dev server). Never hangs. */
async function registration(): Promise<ServiceWorkerRegistration | null> {
  const reg = await navigator.serviceWorker.getRegistration();
  return reg === undefined ? null : navigator.serviceWorker.ready;
}

async function requireRegistration(): Promise<ServiceWorkerRegistration> {
  const reg = await registration();
  if (reg === null) throw new Error("Service worker not registered. Notifications need the production build.");
  return reg;
}

/** Tag of a notification: its own, else the copy in data (iOS drops tag; see sw.ts). */
function tagOf(n: Notification): string | null {
  if (n.tag !== "") return n.tag;
  const data: unknown = n.data;
  if (typeof data !== "object" || data === null || !("tag" in data)) return null;
  return typeof data.tag === "string" ? data.tag : null;
}

/** Closes shown system notifications with `tag` (all when omitted). Best effort. */
async function closeSystemNotifications(tag?: string): Promise<void> {
  if (!("serviceWorker" in navigator)) return;
  try {
    const reg = await registration();
    if (reg === null) return;
    for (const n of await reg.getNotifications()) if (tag === undefined || tagOf(n) === tag) n.close();
  } catch {
    // Not supported or no permission; nothing to close.
  }
}

/**
 * Shown tags to close: task/thread tags whose group has no unread notification
 * (viewed on any device). Other tags (e.g. `test`) and untagged ones stay.
 */
export function staleTags(shown: readonly (string | null)[], unread: ReadonlySet<string>): Set<string> {
  const stale = new Set<string>();
  for (const tag of shown) {
    if (tag !== null && (tag.startsWith("task-") || tag.startsWith("session-")) && !unread.has(tag)) stale.add(tag);
  }
  return stale;
}

/**
 * Closes system notifications for groups already viewed on any device. Best
 * effort; WebKit ignores close() on notifications younger than ~30s, so a later
 * sync retries.
 */
async function syncSystemNotifications(api: PushApi): Promise<void> {
  if (!("serviceWorker" in navigator)) return;
  try {
    const reg = await registration();
    if (reg === null) return;
    const shown = await reg.getNotifications();
    if (shown.length === 0) return;
    const stale = staleTags(shown.map(tagOf), new Set((await api.unreadNotificationTags()).tags));
    for (const n of shown) {
      const tag = tagOf(n);
      if (tag !== null && stale.has(tag)) n.close();
    }
  } catch {
    // Offline, unsupported or no permission; the next sync retries.
  }
}

async function currentSubscription(): Promise<PushSubscription | null> {
  const reg = await registration();
  return reg === null ? null : reg.pushManager.getSubscription();
}

async function getPushState(): Promise<PushState> {
  const ok = supported();
  const state = pushStateFor({
    secure: window.isSecureContext,
    ios: isIos(),
    standalone: isStandalone(),
    supported: ok,
    permission: ok ? Notification.permission : "default",
  });
  if (state !== null) return state;
  return (await currentSubscription()) === null ? "off" : "on";
}

function base64UrlToBytes(value: string): Uint8Array<ArrayBuffer> {
  const base64 = value.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(value.length / 4) * 4, "=");
  const raw = atob(base64);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes;
}

function sameKey(current: ArrayBuffer | null, key: Uint8Array): boolean {
  if (current === null) return false;
  const a = new Uint8Array(current);
  return a.length === key.length && a.every((b, i) => b === key[i]);
}

function subscriptionBody(sub: PushSubscription): PushSubscriptionBody {
  const keys = sub.toJSON().keys;
  const p256dh = keys?.["p256dh"];
  const auth = keys?.["auth"];
  if (p256dh === undefined || auth === undefined) throw new Error("Browser returned a push subscription without keys.");
  return { endpoint: sub.endpoint, keys: { p256dh, auth } };
}

/** Call from a user gesture: asks permission, subscribes this browser and registers it with the server. */
async function enablePush(api: PushApi): Promise<void> {
  // First await: Safari only shows the prompt while the gesture is still active.
  const permission = await Notification.requestPermission();
  if (permission !== "granted") {
    throw new Error(permission === "denied" ? "Notifications are blocked for this site." : "Notification permission was not granted.");
  }
  const reg = await requireRegistration();
  const key = base64UrlToBytes((await api.pushConfig()).publicKey);
  let sub = await reg.pushManager.getSubscription();
  if (sub !== null && !sameKey(sub.options.applicationServerKey, key)) {
    // Subscribed with an old server key (e.g. a reset database): resubscribe.
    await sub.unsubscribe();
    sub = null;
  }
  sub ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
  await api.subscribePush(subscriptionBody(sub));
}

async function disablePush(api: PushApi): Promise<void> {
  const sub = await currentSubscription();
  if (sub === null) return;
  await api.unsubscribePush({ endpoint: sub.endpoint });
  await sub.unsubscribe();
}

async function sendTestPush(api: PushApi): Promise<void> {
  const sub = await currentSubscription();
  if (sub === null) throw new Error("This browser is not subscribed.");
  await api.testPush({ endpoint: sub.endpoint });
}

/** The service worker posts `{type: "push"}` on every push it shows (or skips for a focused window). */
function onPushMessage(listener: () => void): () => void {
  if (!("serviceWorker" in navigator)) return () => undefined;
  function onMessage(e: MessageEvent<unknown>) {
    const data = e.data;
    if (typeof data === "object" && data !== null && "type" in data && data.type === "push") listener();
  }
  navigator.serviceWorker.addEventListener("message", onMessage);
  return () => navigator.serviceWorker.removeEventListener("message", onMessage);
}

/** Web Push through the service worker (/sw.js), registered by main.tsx in production builds. */
export function webPushNotifications(api: PushApi): NotificationDelivery {
  return {
    state: getPushState,
    enable: () => enablePush(api),
    disable: () => disablePush(api),
    test: () => sendTestPush(api),
    closeShown: closeSystemNotifications,
    syncShown: () => syncSystemNotifications(api),
    onDelivered: onPushMessage,
  };
}
