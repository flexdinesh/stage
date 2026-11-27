// Service worker: shows Web Push notifications (docs/api.md "Push payload") and opens their url on click.
// While an app window is focused (Chromium only) the in-app toast replaces the system notification.
// No offline caching. Built as a standalone entry at /sw.js; must import nothing.

// tsconfig.sw.json forces module detection, so this shadows the lib's global `self` without an export.
declare const self: ServiceWorkerGlobalScope;

const ICON = "/icons/icon-192.png";

interface Payload {
  title: string;
  body: string;
  url: string;
  tag: string | null;
  badge: number | null;
}

interface DeliveryRef {
  binding: string;
  eventId: number;
}

const FALLBACK: Payload = { title: "stage", body: "Something changed. Open stage to see it.", url: "/", tag: null, badge: null };
const PRIVATE_REQUEST: RequestInit = { credentials: "same-origin", cache: "no-store", redirect: "error" };
let sessionGeneration = 0;

/** Same-origin path for `raw`. Never return a scheme-relative URL. */
function safeUrl(raw: unknown): string | null {
  if (typeof raw !== "string") return null;
  try {
    const url = new URL(raw, self.location.origin);
    if (url.origin !== self.location.origin || !url.pathname.startsWith("/") || url.pathname.startsWith("//")) return null;
    return `${url.pathname}${url.search}${url.hash}`;
  } catch {
    return null;
  }
}

function parsePayload(raw: unknown, shared: boolean): Payload | null {
  if (typeof raw !== "object" || raw === null) return null;
  const { title, body, url, tag, badge }: Record<string, unknown> = Object.fromEntries(Object.entries(raw));
  if (typeof title !== "string" || title === "") return null;
  const path = safeUrl(url);
  const count = typeof badge === "number" && Number.isFinite(badge) && badge >= 0 ? Math.floor(badge) : null;
  if (shared && (typeof body !== "string" || path === null || typeof tag !== "string" || !Number.isSafeInteger(badge) || count === null)) return null;
  return {
    title,
    body: typeof body === "string" ? body : "",
    url: path ?? "/",
    tag: typeof tag === "string" && tag !== "" ? tag : null,
    badge: count,
  };
}

function hasDeliveryRef(raw: unknown): raw is { binding?: unknown; eventId?: unknown } {
  return typeof raw === "object" && raw !== null && ("binding" in raw || "eventId" in raw);
}

function deliveryRef({ binding, eventId }: { binding?: unknown; eventId?: unknown }): DeliveryRef | null {
  if (typeof binding !== "string" || binding === "" || typeof eventId !== "number" || !Number.isSafeInteger(eventId) || eventId < 0) return null;
  return { binding, eventId };
}

async function isPersonal(): Promise<boolean> {
  try {
    const response = await fetch("/api/auth/status", PRIVATE_REQUEST);
    if (!response.ok) return false;
    const status: unknown = await response.json();
    return typeof status === "object" && status !== null && "mode" in status && status.mode === "personal";
  } catch {
    return false;
  }
}

async function redeem(ref: DeliveryRef, generation: number): Promise<Payload | null> {
  try {
    const response = await fetch("/api/push/delivery", {
      ...PRIVATE_REQUEST,
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(ref),
    });
    if (response.status === 401 && generation === sessionGeneration) await clearNotificationSession();
    return response.status === 200 ? parsePayload(await response.json(), true) : null;
  } catch {
    return null;
  }
}

async function updateBadge(count: number | null): Promise<void> {
  if (count === null || !("setAppBadge" in self.navigator)) return;
  try {
    await (count > 0 ? self.navigator.setAppBadge(count) : self.navigator.clearAppBadge());
  } catch {
    // Badging is best effort.
  }
}

async function clearNotificationSession(): Promise<void> {
  sessionGeneration++;
  await Promise.all([updateBadge(0), closeAllShown()]);
}

async function closeAllShown(): Promise<void> {
  try {
    for (const notification of await self.registration.getNotifications()) notification.close();
  } catch {
    // Notifications may already be unavailable or closed.
  }
}

self.addEventListener("install", (event) => {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("message", (event) => {
  const data: unknown = event.data;
  if (typeof data === "object" && data !== null && "type" in data && data.type === "notification-session-changed") {
    event.waitUntil(clearNotificationSession());
  }
});

/**
 * Chromium lets a push skip the notification while a window is focused. Safari
 * and Firefox may revoke the subscription for silent pushes, so they always show it.
 */
function maySkipNotification(): boolean {
  const ua = self.navigator.userAgent;
  return /Chrome\/|Chromium\/|Edg\//.test(ua) && !/Firefox\//.test(ua);
}

async function showPush(p: Payload, ref: DeliveryRef | null, generation: number): Promise<void> {
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  if (generation !== sessionGeneration) return;
  // Client.postMessage takes no targetOrigin (same-origin clients only).
  // oxlint-disable-next-line unicorn/require-post-message-target-origin
  for (const w of windows) w.postMessage({ type: "push" });
  const focused = windows.some((w) => w.focused && w.visibilityState === "visible");
  const tasks: Promise<void>[] = [updateBadge(p.badge)];
  if (!(focused && maySkipNotification())) {
    // NotificationOptions lacks renotify in the DOM lib; it makes a replaced (same tag) notification alert again.
    const options: NotificationOptions & { renotify?: boolean } = {
      body: p.body,
      icon: ICON,
      badge: ICON,
      data: ref === null ? { url: p.url, tag: p.tag } : { ...ref, tag: p.tag },
    };
    if (p.tag !== null) {
      options.tag = p.tag;
      options.renotify = true;
    }
    tasks.push(showReplacing(p.title, options, p.tag, generation));
  }
  await Promise.all(tasks);
  // Only after showing: iOS revokes the subscription unless a push shows a notification promptly.
  if (generation === sessionGeneration) await closeViewed(generation);
}

/**
 * Cross-device clear (docs/api.md "Clear on view"): closes task/thread
 * notifications whose group has no unread entry, i.e. was viewed on any device.
 * Same rule as staleTags in the browser adapter. Best effort; WebKit ignores close() on
 * notifications younger than ~30s, so a later push or app open retries.
 */
async function closeViewed(generation: number): Promise<void> {
  try {
    const shown = await self.registration.getNotifications();
    if (shown.length === 0) return;
    const res = await fetch("/api/notifications/unread-tags", PRIVATE_REQUEST);
    if (res.status === 401 && generation === sessionGeneration) await clearNotificationSession();
    if (!res.ok || generation !== sessionGeneration) return;
    const raw: unknown = await res.json();
    if (generation !== sessionGeneration) return;
    if (typeof raw !== "object" || raw === null || !("tags" in raw) || !Array.isArray(raw.tags)) return;
    const unread = new Set(raw.tags.filter((t: unknown): t is string => typeof t === "string"));
    for (const n of shown) {
      const tag = tagOf(n);
      if (tag !== null && (tag.startsWith("task-") || tag.startsWith("session-")) && !unread.has(tag)) n.close();
    }
  } catch {
    // Offline or unsupported; the next sync retries.
  }
}

/** Tag of a notification: its own, else the copy in data (iOS drops tag). */
function tagOf(n: Notification): string | null {
  if (n.tag !== "") return n.tag;
  const data: unknown = n.data;
  if (typeof data !== "object" || data === null || !("tag" in data)) return null;
  return typeof data.tag === "string" ? data.tag : null;
}

/** iOS ignores tag replacement (WebKit bug 258922): close same-tag notifications first. */
async function showReplacing(title: string, options: NotificationOptions, tag: string | null, generation: number): Promise<void> {
  if (tag !== null) {
    try {
      const shown = await self.registration.getNotifications();
      if (generation !== sessionGeneration) return;
      for (const n of shown) if (tagOf(n) === tag) n.close();
    } catch {
      // Best effort; tag replacement still applies where supported.
    }
  }
  if (generation === sessionGeneration) await self.registration.showNotification(title, options);
}

async function onPush(data: PushMessageData | null): Promise<void> {
  const generation = sessionGeneration;
  let raw: unknown = null;
  try {
    raw = data?.json();
  } catch {
    // Only a verified personal server may show a generic legacy notification.
  }
  let ref: DeliveryRef | null = null;
  if (hasDeliveryRef(raw)) {
    ref = deliveryRef(raw);
    if (ref === null) return;
  }
  const payload = ref === null
    ? (await isPersonal() ? parsePayload(raw, false) ?? FALLBACK : null)
    : await redeem(ref, generation);
  if (payload !== null && generation === sessionGeneration) await showPush(payload, ref, generation);
}

self.addEventListener("push", (event) => {
  event.waitUntil(onPush(event.data));
});

async function clickUrl(data: unknown, generation: number): Promise<string> {
  if (hasDeliveryRef(data)) {
    const ref = deliveryRef(data);
    return ref === null ? "/" : (await redeem(ref, generation))?.url ?? "/";
  }
  if (typeof data !== "object" || data === null || !("url" in data)) return "/";
  return await isPersonal() ? safeUrl(data.url) ?? "/" : "/";
}

async function openNotification(data: unknown): Promise<void> {
  const generation = sessionGeneration;
  const url = await clickUrl(data, generation);
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  const existing = windows.find((c) => new URL(c.url).origin === self.location.origin);
  if (existing !== undefined) {
    const focused = await existing.focus();
    try {
      await focused.navigate(generation === sessionGeneration ? url : "/");
      return;
    } catch {
      // Not controlled by this worker; fall through to a new window.
    }
  }
  await self.clients.openWindow(generation === sessionGeneration ? url : "/");
}

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const data: unknown = event.notification.data;
  event.waitUntil(openNotification(data));
});
