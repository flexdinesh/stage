import assert from "node:assert/strict";
import { test } from "node:test";
import { connectionBlock, launchBlock, launchNotice, operationReason, readyTreeStarts } from "../src/lib/launch.ts";

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

const subject = { connectionId: 4, projectId: 7, projectTitle: "stage", bindingId: null };
const binding = (status, extra = {}) => ({ id: 42, connectionId: 4, connectionActive: true, primary: true, status, ...extra });
const ctx = (over = {}) => ({
  connection: { active: true, configured: true },
  machine: { kind: "remote", status: "online", label: "desk" },
  live: { tone: "ok", label: "T3 connected" },
  bindings: [binding("available")],
  name: "T3 · desk",
  where: "on desk",
  settings: "/settings/connections",
  ...over,
});

test("launchBlock lets a live, bound connection launch", () => {
  assert.equal(launchBlock(subject, ctx()), null);
});

test("a saved task checks its exact workspace independently of legacy bindings", () => {
  const task = { ...subject, id: 10, workspaceId: "ws_exact" };
  const row = { connectionId: task.connectionId, workspaceId: task.workspaceId, available: true };
  assert.equal(launchBlock(task, ctx({ bindings: [], destinations: [row] })), null);
  assert.match(launchBlock(task, ctx({ destinations: [{ ...row, connectionId: 9 }] }))?.reason, /unavailable/);
  assert.match(launchBlock(task, ctx({ destinations: [{ ...row, workspaceId: "ws_other" }] }))?.reason, /unavailable/);
  assert.match(launchBlock(task, ctx())?.reason, /Checking the saved workspace/);
  assert.match(launchBlock({ ...subject, id: 10 }, ctx())?.reason, /Choose a workspace/);
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
  for (const bindings of [[], [binding("missing")], [binding("available", { connectionId: 9 })]]) {
    const b = launchBlock(subject, ctx({ bindings }));
    assert.equal(b?.kind, "unbound");
    assert.equal(b?.reason, "stage has no available primary workspace on T3 · desk.");
    assert.deepEqual(b?.fix, { label: "Bind workspace", to: "/projects/7" });
  }
  // Projects not loaded yet block nothing.
  assert.equal(launchBlock(subject, ctx({ bindings: undefined })), null);
});

test("launchBlock checks the primary by default and never substitutes for an explicit binding", () => {
  const bindings = [binding("missing"), binding("available", { id: 53, primary: false })];
  assert.equal(launchBlock(subject, ctx({ bindings }))?.kind, "unbound");
  assert.equal(launchBlock({ ...subject, bindingId: 53 }, ctx({ bindings })), null);
  assert.equal(launchBlock({ ...subject, bindingId: 42 }, ctx({ bindings }))?.kind, "unbound");
  assert.equal(launchBlock({ ...subject, bindingId: 999 }, ctx())?.kind, "unbound");
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

test("Start, Stop and Track require their own available declaration", () => {
  const capabilities = {
    operations: { launch: { available: true }, stop: { available: false, reason: "Stop this run in its client." } },
  };
  assert.equal(operationReason("launch", capabilities), null);
  assert.equal(operationReason("stop", capabilities), "Stop this run in its client.");
  assert.match(operationReason("adopt", capabilities), /does not support tracking sessions/);
  assert.match(operationReason("launch", undefined), /Checking/);
  assert.equal(operationReason("launch", capabilities, new Error("Worker unavailable.")), "Worker unavailable.");
});

test("Stop and Track check connection readiness independently of the task workspace", () => {
  const unbound = ctx({ bindings: [] });
  assert.equal(launchBlock(subject, unbound)?.kind, "unbound");
  assert.equal(connectionBlock(unbound, "stop"), null);
  assert.equal(connectionBlock(unbound, "adopt"), null);
  const offline = ctx({ machine: { kind: "remote", status: "offline", label: "desk" } });
  assert.equal(connectionBlock(offline, "stop")?.reason, "desk is offline. Stop this task once it is back.");
  assert.equal(connectionBlock(offline, "adopt")?.reason, "desk is offline. Track sessions once it is back.");
});

const member = (id, fields = {}) => ({
  id, state: "backlog", treeStatus: "open", readiness: "ready", acceptanceId: null, adopted: false, connectionActive: true, ...fields,
});

test("Start ready targets only launchable members of a mixed tree, in ID order", () => {
  const blocked = { kind: "operation", reason: "Read-only harness.", fix: null };
  const ready = readyTreeStarts([
    member(5), member(3), member(4), member(2, { adopted: true }), member(1, { readiness: "blocked" }),
  ], (task) => task.id === 3 ? blocked : null);
  assert.deepEqual(ready.tasks.map((task) => task.id), [4, 5]);
  assert.equal(ready.readyCount, 3);
  assert.equal(ready.firstBlock, blocked);
});
