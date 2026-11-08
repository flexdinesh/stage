import { api } from "../api/client.ts";
import type { PushSubscriptionBody } from "../api/types.ts";

/**
 * - `unsupported`: no service worker / Push API / Notification API
 * - `ios-install`: iOS/iPadOS browser tab; Web Push needs the Home Screen app
 * - `blocked`: permission denied (only the OS/browser settings can undo it)
 * - `off` / `on`: whether this browser has a push subscription
 */
export type PushState = "unsupported" | "ios-install" | "blocked" | "off" | "on";

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

async function currentSubscription(): Promise<PushSubscription | null> {
  const reg = await registration();
  return reg === null ? null : reg.pushManager.getSubscription();
}

export async function getPushState(): Promise<PushState> {
  if (isIos() && !isStandalone()) return "ios-install";
  if (!supported()) return "unsupported";
  if (Notification.permission === "denied") return "blocked";
  if (Notification.permission !== "granted") return "off";
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
export async function enablePush(): Promise<void> {
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

export async function disablePush(): Promise<void> {
  const sub = await currentSubscription();
  if (sub === null) return;
  await api.unsubscribePush({ endpoint: sub.endpoint });
  await sub.unsubscribe();
}

export async function sendTestPush(): Promise<void> {
  const sub = await currentSubscription();
  if (sub === null) throw new Error("This browser is not subscribed.");
  await api.testPush({ endpoint: sub.endpoint });
}
