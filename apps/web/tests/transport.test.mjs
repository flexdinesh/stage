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
