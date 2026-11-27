import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { test } from "node:test";
import { runInNewContext } from "node:vm";

const workerSource = stripTypeScriptTypes(readFileSync(new URL("../src/sw.ts", import.meta.url), "utf8"));
const origin = "https://stage.example";
const ref = { binding: "opaque-binding", eventId: 12 };
const payload = { title: "Needs input", body: "Account work", url: "/tasks/7", tag: "task-7", badge: 2 };
const deliveryPath = "/api/push/delivery";
const statusPath = "/api/auth/status";
const tagsPath = "/api/notifications/unread-tags";

function runWorker({ respond, userAgent = "Firefox/100", focused = false }) {
  const handlers = new Map();
  const requests = [];
  const shown = [];
  const badges = [];
  const messages = [];
  const destinations = [];
  const client = {
    focused,
    visibilityState: "visible",
    url: `${origin}/dashboard`,
    postMessage: (message) => messages.push(structuredClone(message)),
    focus: async () => client,
    navigate: async (url) => destinations.push(url),
  };
  const self = {
    location: { origin },
    navigator: {
      userAgent,
      setAppBadge: async (count) => badges.push(count),
      clearAppBadge: async () => badges.push(0),
    },
    clients: { matchAll: async () => [client] },
    registration: {
      getNotifications: async () => shown.filter((item) => !item.closed),
      async showNotification(title, options) {
        const notification = {
          title, ...structuredClone(options), closed: false,
          close() { this.closed = true; },
        };
        shown.push(notification);
      },
    },
    addEventListener: (name, handler) => handlers.set(name, handler),
  };
  function fetch(path, init) {
    requests.push([path, structuredClone(init)]);
    if (path === tagsPath) return Response.json({ tags: [payload.tag] });
    return respond(path, init);
  }
  runInNewContext(workerSource, { self, fetch, URL });
  function dispatch(type, data) {
    let pending;
    handlers.get(type)({ ...data, waitUntil(promise) { pending = promise; } });
    return pending;
  }
  return {
    push: (data) => dispatch("push", { data: { json: () => data } }),
    click: (notification) => dispatch("notificationclick", { notification }),
    clear: () => dispatch("message", { data: { type: "notification-session-changed" } }),
    requests, shown, badges, messages, destinations,
  };
}

test("shared push redeems the reference with the current cookie and shows the server payload", async () => {
  const worker = runWorker({ respond: () => Response.json(payload) });
  await worker.push({ ...ref, title: "Stale transport title", url: "/tasks/99" });
  assert.deepEqual(worker.requests[0], [deliveryPath, {
    method: "POST", credentials: "same-origin", cache: "no-store", redirect: "error",
    headers: { "Content-Type": "application/json" }, body: JSON.stringify(ref),
  }]);
  assert.deepEqual(worker.badges, [2]);
  assert.deepEqual(worker.messages, [{ type: "push" }]);
  const [shown] = worker.shown;
  assert.equal(shown.title, payload.title);
  assert.equal(shown.body, payload.body);
  assert.deepEqual(shown.data, { ...ref, tag: payload.tag });
  await worker.click(shown);
  assert.equal(shown.closed, true);
  assert.deepEqual(worker.destinations, [payload.url]);
  assert.deepEqual(worker.requests[2], worker.requests[0], "click authorizes the reference again");
});

test("test pushes use event ID zero and retain their test tag", async () => {
  const worker = runWorker({ respond: () => Response.json({ ...payload, tag: "test", badge: 0 }) });
  await worker.push({ binding: ref.binding, eventId: 0 });
  assert.equal(JSON.parse(worker.requests[0][1].body).eventId, 0);
  assert.equal(worker.shown[0].closed, false);
  assert.deepEqual(worker.badges, [0]);
});

test("shared pushes from stale or unavailable sessions never show work or notify clients", async (t) => {
  for (const [name, respond] of [
    ["stale", () => new Response(null, { status: 204 })],
    ["revoked", () => new Response(null, { status: 401 })],
    ["offline", () => { throw new TypeError("offline"); }],
    ["invalid JSON", () => new Response("broken")],
    ["incomplete payload", () => Response.json({ title: payload.title })],
    ["external URL", () => Response.json({ ...payload, url: `${origin}//outside.example` })],
  ]) {
    await t.test(name, async () => {
      const worker = runWorker({ respond });
      await worker.push(ref);
      assert.deepEqual(worker.shown, []);
      assert.deepEqual(worker.messages, []);
      assert.ok(worker.badges.every((count) => count === 0));
    });
  }
});

test("a malformed shared reference cannot fall back to legacy details", async () => {
  const worker = runWorker({ respond: () => Response.json({ mode: "personal" }) });
  await worker.push({ ...payload, binding: ref.binding });
  await worker.push({ ...payload, eventId: -1 });
  assert.deepEqual(worker.shown, []);
  assert.deepEqual(worker.requests, []);
});

test("legacy payloads and click destinations require a verified personal server", async () => {
  let mode = "personal";
  const worker = runWorker({ respond: (path) => {
    assert.equal(path, statusPath);
    return Response.json({ mode, installationId: "stage-installation" });
  } });
  await worker.push(payload);
  assert.equal(worker.shown[0].title, payload.title);
  assert.deepEqual(worker.badges, [2]);
  assert.equal(worker.requests[0][1].cache, "no-store");
  await worker.click(worker.shown[0]);
  assert.deepEqual(worker.destinations, [payload.url]);
  for (mode of ["setup_pending", "claimed", "unknown"]) await worker.push(payload);
  mode = "claimed";
  await worker.click(worker.shown[0]);
  assert.equal(worker.shown.length, 1);
  assert.deepEqual(worker.destinations, [payload.url, "/"]);
});

test("verified personal pushes still update a focused Chromium client without showing an OS alert", async () => {
  const worker = runWorker({ userAgent: "Chrome/100", focused: true, respond: () => Response.json({ mode: "personal" }) });
  await worker.push(payload);
  assert.deepEqual(worker.shown, []);
  assert.deepEqual(worker.badges, [2]);
  assert.deepEqual(worker.messages, [{ type: "push" }]);
});

test("a previously shown shared alert opens home after this browser changes accounts", async () => {
  let bound = true;
  const worker = runWorker({ respond: () => bound ? Response.json(payload) : new Response(null, { status: 204 }) });
  await worker.push(ref);
  bound = false;
  await worker.click(worker.shown[0]);
  assert.equal(worker.shown[0].closed, true);
  assert.deepEqual(worker.destinations, ["/"]);
});

test("session teardown closes displayed notifications and discards an in-flight delivery", async () => {
  const pending = Promise.withResolvers();
  const worker = runWorker({ respond: (_path, init) => JSON.parse(init.body).eventId === ref.eventId ? Response.json(payload) : pending.promise });
  await worker.push(ref);
  const second = worker.push({ ...ref, eventId: ref.eventId + 1 });
  await worker.clear();
  pending.resolve(Response.json(payload));
  await second;
  assert.equal(worker.shown.length, 1);
  assert.equal(worker.shown[0].closed, true);
  assert.deepEqual(worker.badges, [2, 0]);
  assert.deepEqual(worker.messages, [{ type: "push" }]);
});
