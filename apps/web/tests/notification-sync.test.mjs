import assert from "node:assert/strict";
import { test } from "node:test";
import { staleTags } from "../src/platform/webPush.ts";

test("closes task/session notifications whose group was viewed on any device", () => {
  const stale = staleTags(["task-1", "session-abc", "task-2", "session-xyz"], new Set(["task-2", "session-xyz"]));
  assert.deepEqual([...stale].toSorted(), ["session-abc", "task-1"]);
});

test("keeps unread groups, foreign tags and untagged notifications", () => {
  const stale = staleTags(["task-1", "test", null, "other-1", "thread-legacy", "tasks-1"], new Set(["task-1"]));
  assert.deepEqual([...stale], []);
});

test("nothing unread closes every shown task/session notification once", () => {
  const stale = staleTags(["task-1", "task-1", "session-a"], new Set());
  assert.deepEqual([...stale].toSorted(), ["session-a", "task-1"]);
});

test("delivery listeners fire on the service worker's push message only, and unsubscribe", async () => {
  const { webPushNotifications } = await import("../src/platform/webPush.ts");
  const sw = new EventTarget();
  const original = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  Object.defineProperty(globalThis, "navigator", { value: { serviceWorker: sw }, configurable: true });
  try {
    let delivered = 0;
    const off = webPushNotifications({}).onDelivered(() => delivered++);
    sw.dispatchEvent(new MessageEvent("message", { data: { type: "push" } }));
    sw.dispatchEvent(new MessageEvent("message", { data: { type: "other" } }));
    sw.dispatchEvent(new MessageEvent("message", { data: "push" }));
    off();
    sw.dispatchEvent(new MessageEvent("message", { data: { type: "push" } }));
    assert.equal(delivered, 1);
  } finally {
    if (original !== undefined) Object.defineProperty(globalThis, "navigator", original);
  }
});

test("without service workers, delivery subscription is a no-op", async () => {
  const { webPushNotifications } = await import("../src/platform/webPush.ts");
  const original = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  Object.defineProperty(globalThis, "navigator", { value: {}, configurable: true });
  try {
    const off = webPushNotifications({}).onDelivered(() => assert.fail("never"));
    off();
    await webPushNotifications({}).closeShown("task-1");
    await webPushNotifications({}).syncShown();
  } finally {
    if (original !== undefined) Object.defineProperty(globalThis, "navigator", original);
  }
});
