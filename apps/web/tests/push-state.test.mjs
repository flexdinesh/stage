import assert from "node:assert/strict";
import { test } from "node:test";
import { clearPushSession, pushStateFor, webPushNotifications } from "../src/platform/webPush.ts";

/** A secure desktop browser with push support and no permission decision yet. */
const base = { secure: true, ios: false, standalone: false, supported: true, permission: "default" };

test("insecure context reports insecure, not unsupported", () => {
  // Browsers hide the push APIs outside a secure context.
  assert.equal(pushStateFor({ ...base, secure: false, supported: false }), "insecure");
});

test("insecure wins over every other condition", () => {
  assert.equal(pushStateFor({ ...base, secure: false, ios: true }), "insecure");
  assert.equal(pushStateFor({ ...base, secure: false, permission: "denied" }), "insecure");
  assert.equal(pushStateFor({ ...base, secure: false, permission: "granted" }), "insecure");
});

test("secure context without push APIs is unsupported", () => {
  assert.equal(pushStateFor({ ...base, supported: false }), "unsupported");
});

test("iOS browser tab must install first; the Home Screen app proceeds", () => {
  assert.equal(pushStateFor({ ...base, ios: true, supported: false }), "ios-install");
  assert.equal(pushStateFor({ ...base, ios: true, standalone: true }), "off");
});

test("permission maps to blocked, off, or a subscription lookup", () => {
  assert.equal(pushStateFor({ ...base, permission: "denied" }), "blocked");
  assert.equal(pushStateFor(base), "off");
  assert.equal(pushStateFor({ ...base, permission: "granted" }), null);
});

function installPushBrowser(t, registration) {
  const globals = {
    window: { isSecureContext: true, matchMedia: () => ({ matches: false }), PushManager: {}, Notification: {} },
    navigator: {
      userAgent: "Chrome/100",
      serviceWorker: { getRegistration: async () => registration, ready: Promise.resolve(registration) },
    },
    Notification: { permission: "granted", requestPermission: async () => "granted" },
  };
  for (const [name, value] of Object.entries(globals)) {
    const original = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, { value, configurable: true });
    t.after(() => original === undefined ? Reflect.deleteProperty(globalThis, name) : Object.defineProperty(globalThis, name, original));
  }
}

test("a local subscription is off until the current authenticated session binds it", async (t) => {
  const endpoint = "https://push.example/subscription";
  const sub = {
    endpoint,
    options: { applicationServerKey: new Uint8Array([0]).buffer },
    toJSON: () => ({ keys: { p256dh: "key", auth: "auth" } }),
    unsubscribe: () => assert.fail("reuse the existing subscription"),
  };
  let current = sub;
  installPushBrowser(t, { pushManager: {
    getSubscription: async () => current,
    subscribe: () => assert.fail("reuse the existing subscription"),
  } });
  let bound = false;
  const checked = [];
  const delivery = webPushNotifications({
    checkPushSubscription: async (body) => {
      checked.push(body);
      return { bound };
    },
    pushConfig: async () => ({ publicKey: "AA" }),
    subscribePush: async (body) => {
      assert.deepEqual(body, { endpoint, keys: { p256dh: "key", auth: "auth" } });
      bound = true;
    },
  });
  assert.equal(await delivery.state(), "off");
  await delivery.enable();
  assert.equal(await delivery.state(), "on");
  bound = false;
  assert.equal(await delivery.state(), "off", "a different account cannot use the previous binding");
  current = null;
  assert.equal(await delivery.state(), "off");
  assert.deepEqual(checked, [{ endpoint }, { endpoint }, { endpoint }]);
});

test("browser teardown closes existing OS alerts and cancels worker deliveries", async (t) => {
  const messages = [];
  let closed = 0;
  installPushBrowser(t, {
    active: { postMessage: (data) => messages.push(data) },
    getNotifications: async () => [{ tag: "task-1", close: () => closed++ }, { tag: "test", close: () => closed++ }],
  });
  await clearPushSession();
  assert.deepEqual(messages, [{ type: "notification-session-changed" }]);
  assert.equal(closed, 2);
});
