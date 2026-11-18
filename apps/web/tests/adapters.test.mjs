import assert from "node:assert/strict";
import { test } from "node:test";
import { FAVICON_IDLE, FAVICON_NEED, browserAttention } from "../src/platform/attention.ts";
import { browserClipboard } from "../src/platform/clipboard.ts";
import { browserLinks } from "../src/platform/links.ts";
import { browserStorage } from "../src/platform/storage.ts";

test("clipboard uses the Clipboard API in a secure context", async () => {
  const written = [];
  const c = browserClipboard({ secure: () => true, writeText: async (t) => void written.push(t), legacyCopy: () => assert.fail("no fallback") });
  await c.writeText("[stage task #1](http://h/tasks/1)");
  assert.deepEqual(written, ["[stage task #1](http://h/tasks/1)"]);
});

test("clipboard falls back to legacy copy over plain HTTP and reports refusal", async () => {
  const copied = [];
  const ok = browserClipboard({ secure: () => false, writeText: () => assert.fail("no Clipboard API"), legacyCopy: (t) => (copied.push(t), true) });
  await ok.writeText("x");
  assert.deepEqual(copied, ["x"]);
  const refused = browserClipboard({ secure: () => false, writeText: () => assert.fail(), legacyCopy: () => false });
  await assert.rejects(refused.writeText("x"), /copy failed/);
});

test("clipboard API rejection reaches the caller (Copy failed)", async () => {
  const c = browserClipboard({ secure: () => true, writeText: async () => Promise.reject(new Error("denied")), legacyCopy: () => true });
  await assert.rejects(c.writeText("x"), /denied/);
});

test("links: copied links use the page origin; external opens a tab; app links navigate in place", () => {
  const calls = [];
  const l = browserLinks({ origin: () => "https://stage.lan", open: (u) => calls.push(["open", u]), assign: (u) => calls.push(["assign", u]) });
  assert.equal(l.executionDestination({ native: "t3code://threads/e/t", web: "https://t3.example/t" }), "t3code://threads/e/t");
  assert.equal(l.executionDestination({ native: null, web: "https://harness.example/t" }), "https://harness.example/t");
  assert.equal(l.stageLink("/tasks/12"), "https://stage.lan/tasks/12");
  l.openExternal("https://github.com/x/y/pull/1");
  l.openApp("t3code://threads/e/t");
  assert.deepEqual(calls, [["open", "https://github.com/x/y/pull/1"], ["assign", "t3code://threads/e/t"]]);
});

function memoryStore() {
  const m = new Map();
  return { m, getItem: (k) => m.get(k) ?? null, setItem: (k, v) => m.set(k, v), removeItem: (k) => m.delete(k) };
}

test("storage reads, writes and removes", () => {
  const store = memoryStore();
  const s = browserStorage(() => store);
  assert.equal(s.get("stage-theme"), null);
  s.set("stage-theme", "dark");
  assert.equal(s.get("stage-theme"), "dark");
  s.set("stage-theme", null);
  assert.equal(store.m.has("stage-theme"), false);
});

test("unavailable storage reads null and drops writes without throwing", () => {
  const s = browserStorage(() => {
    throw new DOMException("denied", "SecurityError");
  });
  assert.equal(s.get("k"), null);
  s.set("k", "v");
  s.set("k", null);
});

test("attention swaps the favicon and mirrors the count on the app badge", async () => {
  const calls = [];
  const a = browserAttention({
    setFavicon: (h) => calls.push(["icon", h]),
    badge: { set: async (n) => void calls.push(["badge", n]), clear: async () => void calls.push(["clear"]) },
  });
  a.setNeedsInput(undefined);
  a.setNeedsInput(3);
  a.setNeedsInput(0);
  await Promise.resolve();
  assert.deepEqual(calls, [["icon", FAVICON_IDLE], ["icon", FAVICON_NEED], ["badge", 3], ["icon", FAVICON_IDLE], ["clear"]]);
});

test("attention without the Badging API still swaps the favicon; badge failures are swallowed", async () => {
  const icons = [];
  browserAttention({ setFavicon: (h) => icons.push(h), badge: null }).setNeedsInput(2);
  assert.deepEqual(icons, [FAVICON_NEED]);
  const failing = browserAttention({ setFavicon: () => undefined, badge: { set: () => Promise.reject(new Error("no")), clear: () => Promise.reject(new Error("no")) } });
  failing.setNeedsInput(1);
  failing.setNeedsInput(0);
  await new Promise((r) => setTimeout(r, 0));
});
