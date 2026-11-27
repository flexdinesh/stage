import assert from "node:assert/strict";
import { test } from "node:test";
import { browserTransport } from "../src/platform/transport.ts";

/** EventSource stand-in: records listeners; tests fire events by name. */
class FakeEventSource {
  static instances = [];
  readyState = 0;
  closed = false;
  listeners = new Map();
  constructor(url) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type, fn) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  fire(type, event = new Event(type)) {
    for (const fn of this.listeners.get(type) ?? []) fn(event);
  }
  close() {
    this.closed = true;
    this.readyState = 2;
  }
}

const transportWith = (fetch = async () => new Response(null)) =>
  browserTransport({ fetch, eventSource: (url) => new FakeEventSource(url) });

const message = (type, data) => new MessageEvent(type, { data });

test("requests and asset URLs are same-origin under /api", async () => {
  const calls = [];
  const t = transportWith(async (input, init) => {
    calls.push({ input, init });
    return new Response("ok", { status: 418 });
  });
  const init = { method: "POST", body: "{}" };
  const res = await t.request("/tasks/1/start", init);
  assert.equal(res.status, 418, "any status resolves; the client maps errors");
  assert.deepEqual(calls, [{ input: "/api/tasks/1/start", init }]);
  assert.equal(t.assetUrl("/attachments/7"), "/api/attachments/7");
});

test("session changes and expired credentials clear the browser host's private state", async () => {
  const plan = new Map([
    ["/api/auth/logout", new Response(null, { status: 204 })],
    ["/api/account/password", new Response(null, { status: 204 })],
    ["/api/auth/invite", Response.json({ id: 2 })],
    ["/api/auth/login", Response.json({ code: "sign_in_failed" }, { status: 401 })],
    ["/api/tasks", Response.json({ code: "authentication_required" }, { status: 401 })],
    ["/api/admin/accounts", Response.json({ code: "principal_forbidden" }, { status: 403 })],
  ]);
  let changes = 0;
  const t = browserTransport({ fetch: async (path) => plan.get(path), onSessionChanged: () => changes++ });
  await t.request("/auth/logout", { method: "POST" });
  await t.request("/account/password", { method: "POST" });
  await t.request("/auth/invite", { method: "POST" });
  await t.request("/auth/login", { method: "POST" });
  await t.request("/admin/accounts", {});
  assert.equal(changes, 3);
  const expired = await t.request("/tasks", {});
  assert.equal(changes, 4);
  assert.deepEqual(await expired.json(), { code: "authentication_required" });
});

test("a late authorization error cannot clear the new session's notifications", async () => {
  const old = Promise.withResolvers();
  let changes = 0;
  const t = browserTransport({
    fetch: (path) => path.endsWith("/tasks") ? old.promise : Promise.resolve(Response.json({ id: 2 })),
    onSessionChanged: () => changes++,
  });
  const pending = t.request("/tasks", {});
  await t.request("/auth/login", { method: "POST" });
  old.resolve(Response.json({ code: "authentication_required" }, { status: 401 }));
  await pending;
  assert.equal(changes, 1);
});

test("stream delivers open, named events and errors, then nothing after close", () => {
  const seen = [];
  const sub = transportWith().subscribe("/threads/t/transcript/stream?after=3", {
    open: () => seen.push("open"),
    error: (closed) => seen.push(`error:${closed}`),
    events: { item: (d) => seen.push(`item:${d}`), state: (d) => seen.push(`state:${d}`) },
  });
  const es = FakeEventSource.instances.at(-1);
  assert.equal(es.url, "/api/threads/t/transcript/stream?after=3");
  es.fire("open");
  es.fire("item", message("item", '{"position":4}'));
  es.fire("state", message("state", '{"active":true}'));
  es.fire("other", message("other", "ignored"));
  es.readyState = 0; // the browser is reconnecting
  es.fire("error");
  sub.close();
  assert.equal(es.closed, true, "close stops the EventSource (cancels the request)");
  es.fire("item", message("item", "late"));
  es.fire("error");
  assert.deepEqual(seen, ["open", 'item:{"position":4}', 'state:{"active":true}', "error:false"]);
});

test("a refused stream reports closed so the UI stops showing reconnecting", () => {
  const errors = [];
  transportWith().subscribe("/s", { error: (closed) => errors.push(closed), events: {} });
  const es = FakeEventSource.instances.at(-1);
  es.readyState = 2;
  es.fire("error");
  assert.deepEqual(errors, [true]);
});

test("a handler may close its own stream (transcript reset)", () => {
  let sub;
  let resets = 0;
  sub = transportWith().subscribe("/s", {
    events: {
      reset: () => {
        resets++;
        sub.close();
      },
      item: () => assert.fail("no events after close"),
    },
  });
  const es = FakeEventSource.instances.at(-1);
  es.fire("reset", message("reset", ""));
  es.fire("item", message("item", "x"));
  assert.equal(resets, 1);
  assert.equal(es.closed, true);
});

test("non-message events and non-string data are ignored", () => {
  const items = [];
  transportWith().subscribe("/s", { events: { item: (d) => items.push(d) } });
  const es = FakeEventSource.instances.at(-1);
  es.fire("item", new Event("item"));
  es.fire("item", message("item", { not: "text" }));
  assert.deepEqual(items, []);
});
