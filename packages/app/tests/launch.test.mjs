import assert from "node:assert/strict";
import { test } from "node:test";
import { launchBlock, launchNotice } from "../src/lib/launch.ts";

const NOW = Date.parse("2026-11-24T04:10:00Z");
const launch = (over = {}) => ({ attempt: 1, revision: 4, phase: "unconfirmed", reason: "", retryAt: null, retryCount: 0, session: null, ...over });

test("launchNotice keeps the checking copy before any check failed", () => {
  const n = launchNotice({ launch: launch(), stateSince: "2026-11-24T04:07:00Z" }, "T3", NOW);
  assert.equal(n.title, "Launch unconfirmed");
  assert.equal(n.session, null);
  assert.equal(n.reason, null);
  assert.equal(n.meta, null);
  assert.match(n.body, /checking the existing attempt/);
});

test("launchNotice shows the cause, failed checks and next retry", () => {
  const n = launchNotice({ launch: launch({ reason: "connection reset", retryCount: 3, retryAt: "2026-11-24T04:10:40Z" }), stateSince: "2026-11-24T04:07:00Z" }, "T3", NOW);
  assert.equal(n.title, "Launch unconfirmed");
  assert.equal(n.reason, "connection reset");
  assert.equal(n.meta, "3 failed checks · next retry in 40s · started 3m ago");
});

test("launchNotice leads with the session a launch returned but Stage could not record", () => {
  const session = { connectionId: 4, externalSessionId: "thread-1", title: "delete viewed pages", state: "needs_input" };
  const n = launchNotice({ launch: launch({ reason: "constraint failed", retryCount: 1, retryAt: "2026-11-24T04:09:00Z", session }), stateSince: null }, "T3", NOW);
  assert.equal(n.title, "Launched in T3; Stage can't record it");
  assert.equal(n.session, "delete viewed pages · needs input");
  assert.equal(n.reason, "Recording failed: constraint failed");
  assert.equal(n.meta, "1 failed check · retrying now");
  assert.match(n.body, /until Stage records the session/);
});

test("launchNotice names an untitled session by its id", () => {
  const session = { connectionId: 4, externalSessionId: "thread-1", title: "", state: "running" };
  assert.equal(launchNotice({ launch: launch({ session }), stateSince: null }, "T3", NOW).session, "thread-1 · running");
});

const subject = { connectionId: 4, projectId: 7, projectTitle: "stage" };
const ctx = (over = {}) => ({
  connection: { active: true, configured: true },
  machine: { kind: "remote", status: "online", label: "desk" },
  live: { tone: "ok", label: "T3 connected" },
  bindings: [{ connectionId: 4, status: "available" }],
  name: "T3 · desk",
  where: "on desk",
  settings: "/settings/connections",
  ...over,
});

test("launchBlock lets a live, bound connection launch", () => {
  assert.equal(launchBlock(subject, ctx()), null);
});

test("launchBlock refuses a disconnected connection and links to Settings", () => {
  const b = launchBlock(subject, ctx({ connection: { active: true, configured: false } }));
  assert.equal(b?.kind, "disconnected");
  assert.equal(b?.reason, "T3 · desk is disconnected. Connect it to start this task.");
  assert.deepEqual(b?.fix, { label: "Connect in Settings", to: "/settings/connections" });
});

test("launchBlock refuses to start while the worker is offline, negotiating, or incompatible", () => {
  const off = launchBlock(subject, ctx({ machine: { kind: "remote", status: "offline", label: "desk" } }));
  assert.equal(off?.kind, "offline");
  assert.equal(off?.reason, "desk is offline. Start this task once it is back.");
  assert.equal(off?.fix, null);
  const negotiating = launchBlock(subject, ctx({ machine: { kind: "remote", status: "negotiating", label: "desk" } }));
  assert.equal(negotiating?.kind, "negotiating");
  assert.equal(negotiating?.fix, null);
  const update = launchBlock(subject, ctx({ machine: { kind: "remote", status: "needs_update", label: "desk", statusReason: "Update Stage on the main machine." } }));
  assert.equal(update?.kind, "needs_update");
  assert.equal(update?.reason, "Update Stage on the main machine.");
  // The local machine always answers.
  assert.equal(launchBlock(subject, ctx({ machine: { kind: "local", status: "offline", label: "here" } })), null);
});

test("launchBlock refuses a harness whose live status is a problem, never one still checking", () => {
  const b = launchBlock(subject, ctx({ live: { tone: "bad", label: "T3 unreachable" } }));
  assert.equal(b?.kind, "unreachable");
  assert.equal(b?.reason, "T3 unreachable on desk, so this task cannot start.");
  assert.equal(launchBlock(subject, ctx({ live: { tone: "need", label: "T3 pairing expired" }, where: "" }))?.reason, "T3 pairing expired, so this task cannot start.");
  assert.equal(launchBlock(subject, ctx({ live: { tone: "idle", label: "Checking T3" } })), null);
  assert.equal(launchBlock(subject, ctx({ live: undefined })), null);
});

test("launchBlock refuses a project without an available workspace on the connection", () => {
  for (const bindings of [[], [{ connectionId: 4, status: "missing" }], [{ connectionId: 9, status: "available" }]]) {
    const b = launchBlock(subject, ctx({ bindings }));
    assert.equal(b?.kind, "unbound");
    assert.equal(b?.reason, "stage has no workspace on T3 · desk. Bind one to start this task.");
    assert.deepEqual(b?.fix, { label: "Bind workspace", to: "/projects/7" });
  }
  // Projects not loaded yet block nothing.
  assert.equal(launchBlock(subject, ctx({ bindings: undefined })), null);
});

test("launchBlock checks in order: disconnected, machine, live status, workspace", () => {
  const all = { connection: { active: true, configured: false }, machine: { kind: "remote", status: "offline", label: "desk" }, live: { tone: "bad", label: "T3 unreachable" }, bindings: [] };
  assert.equal(launchBlock(subject, ctx(all))?.kind, "disconnected");
  assert.equal(launchBlock(subject, ctx({ ...all, connection: { active: true, configured: true } }))?.kind, "offline");
  assert.equal(launchBlock(subject, ctx({ ...all, connection: { active: true, configured: true }, machine: undefined }))?.kind, "unreachable");
});

test("launchBlock leaves a replaced or unloaded connection to its own path", () => {
  assert.equal(launchBlock(subject, ctx({ connection: { active: false, configured: false } })), null);
  assert.equal(launchBlock(subject, ctx({ connection: undefined })), null);
});
