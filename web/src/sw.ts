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

const FALLBACK: Payload = { title: "t3-stage", body: "Something changed. Open t3-stage to see it.", url: "/", tag: null, badge: null };

/** Same-origin path for `raw`, else "/". */
function safeUrl(raw: unknown): string {
  if (typeof raw !== "string") return "/";
  try {
    const url = new URL(raw, self.location.origin);
    return url.origin === self.location.origin ? `${url.pathname}${url.search}${url.hash}` : "/";
  } catch {
    return "/";
  }
}

function parsePayload(data: PushMessageData | null): Payload {
  if (data === null) return FALLBACK;
  let raw: unknown;
  try {
    raw = data.json();
  } catch {
    return FALLBACK;
  }
  if (typeof raw !== "object" || raw === null) return FALLBACK;
  const obj: Record<string, unknown> = Object.fromEntries(Object.entries(raw));
  const { title, body, url, tag, badge } = obj;
  if (typeof title !== "string" || title === "") return FALLBACK;
  return {
    title,
    body: typeof body === "string" ? body : "",
    url: safeUrl(url),
    tag: typeof tag === "string" && tag !== "" ? tag : null,
    badge: typeof badge === "number" && Number.isFinite(badge) && badge >= 0 ? Math.floor(badge) : null,
  };
}

async function updateBadge(count: number | null): Promise<void> {
  if (count === null || !("setAppBadge" in self.navigator)) return;
  try {
    await (count > 0 ? self.navigator.setAppBadge(count) : self.navigator.clearAppBadge());
  } catch {
    // Badging is best effort.
  }
}

self.addEventListener("install", (event) => {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

/**
 * Chromium lets a push skip the notification while a window is focused. Safari
 * and Firefox may revoke the subscription for silent pushes, so they always show it.
 */
function maySkipNotification(): boolean {
  const ua = self.navigator.userAgent;
  return /Chrome\/|Chromium\/|Edg\//.test(ua) && !/Firefox\//.test(ua);
}

async function onPush(p: Payload): Promise<void> {
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  // Client.postMessage takes no targetOrigin (same-origin clients only).
  // oxlint-disable-next-line unicorn/require-post-message-target-origin
  for (const w of windows) w.postMessage({ type: "push" });
  const focused = windows.some((w) => w.focused && w.visibilityState === "visible");
  const tasks: Promise<void>[] = [updateBadge(p.badge)];
  if (!(focused && maySkipNotification())) {
    // NotificationOptions lacks renotify in the DOM lib; it makes a replaced (same tag) notification alert again.
    const options: NotificationOptions & { renotify?: boolean } = { body: p.body, icon: ICON, badge: ICON, data: { url: p.url } };
    if (p.tag !== null) {
      options.tag = p.tag;
      options.renotify = true;
    }
    tasks.push(self.registration.showNotification(p.title, options));
  }
  await Promise.all(tasks);
}

self.addEventListener("push", (event) => {
  event.waitUntil(onPush(parsePayload(event.data)));
});

function clickUrl(data: unknown): string {
  if (typeof data !== "object" || data === null || !("url" in data)) return "/";
  return safeUrl(data.url);
}

async function openUrl(url: string): Promise<void> {
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  const existing = windows.find((c) => new URL(c.url).origin === self.location.origin);
  if (existing !== undefined) {
    const focused = await existing.focus();
    try {
      await focused.navigate(url);
      return;
    } catch {
      // Not controlled by this worker; fall through to a new window.
    }
  }
  await self.clients.openWindow(url);
}

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const data: unknown = event.notification.data;
  event.waitUntil(openUrl(clickUrl(data)));
});
