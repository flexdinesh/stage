import assert from "node:assert/strict";
import { test } from "node:test";
import { fakeApi } from "./fake-transport.mjs";
import { batchSize, canStart, isFullyEditable, isWaiting, moveItem, statusChips, waitTarget } from "../src/lib/tasks.ts";

const task = (id, extra = {}) => ({
  acceptanceId: null, treeStatus: null, id, title: `t${id}`, state: "backlog", adopted: false, readiness: null, needs: [], startAt: null, ...extra,
});

const tree = (id) => ({ acceptanceId: null, treeStatus: null, id, title: `tree ${id}`, startMode: "auto" });

test("an adopted backlog session keeps its launch controls in the harness", () => {
  const adopted = task(1, { adopted: true });
  assert.equal(canStart(adopted), false);
  assert.equal(isFullyEditable(adopted), false);
  assert.equal(isFullyEditable(task(2)), true);
});

test("a task waits only while blocked on its needs", () => {
  assert.equal(isWaiting(task(1, { needs: [3], readiness: "blocked" })), true);
  // Ready (needs settled, manual) or outside a tree: not waiting.
  assert.equal(isWaiting(task(1, { needs: [3], readiness: "ready" })), false);
  assert.equal(isWaiting(task(1)), false);
  assert.equal(isWaiting(task(1, { state: "running", needs: [3] })), false);
});

test("wait target lists the needs", () => {
  assert.equal(waitTarget(task(1, { needs: [12] })), "#12");
  assert.equal(waitTarget(task(1, { needs: [3, 5] })), "#3, #5");
  assert.equal(waitTarget(task(1)), "");
});

test("start is offered for startable states, blocked excluded, never for an adopted session", () => {
  assert.equal(canStart(task(1)), true);
  assert.equal(canStart(task(1, { needs: [3], readiness: "blocked" })), false);
  assert.equal(canStart(task(1, { state: "failed" })), true);
  assert.equal(canStart(task(1, { state: "running" })), false);
  assert.equal(canStart(task(1, { adopted: true })), false);
  assert.equal(canStart(task(1, { adopted: true, state: "failed" })), false);
});

test("moveItem moves one item and clamps at the ends", () => {
  assert.deepEqual(moveItem([1, 2, 3], 2, -1), [1, 3, 2]);
  assert.deepEqual(moveItem([1, 2, 3], 0, 1), [2, 1, 3]);
  assert.deepEqual(moveItem([1, 2, 3], 0, -1), [1, 2, 3]);
  assert.deepEqual(moveItem([1, 2, 3], 2, 1), [1, 2, 3]);
});

test("batch size counts tree members and independent tasks", () => {
  assert.equal(batchSize({ trees: [{ tree: tree(1), tasks: [task(1), task(2)] }, { tree: tree(3), tasks: [task(3), task(4)] }], independent: [task(5)] }), 5);
  assert.equal(batchSize({ trees: [], independent: [] }), 0);
});

test("HTTP contract: needs, trees and adopt calls", async () => {
  const calls = [];
  const { api } = fakeApi(async (path, options) => {
    calls.push({ path: new URL(path, "http://stage.test").pathname + new URL(path, "http://stage.test").search, method: options.method ?? "GET", body: options.body === undefined ? undefined : JSON.parse(options.body) });
    return Response.json({});
  });
  await api.setNeeds(7, [3, 5]);
  await api.trees({ status: "all", project: 2 });
  await api.createTree({ title: "T", tasks: [{ taskId: 3 }, { taskId: 7, needs: [3] }] });
  await api.updateTree(4, { startMode: "manual" });
  await api.addToTree(4, 9);
  await api.addToTree(4, 9, [7]);
  await api.startTree(4);
  await api.updateTree(4, { status: "done" });
  await api.deleteTree(4);
  await api.dissolveTree(5);
  await api.archiveTask(7);
  await api.unarchiveTask(7);
  await api.tasks({ project: 2, closed: "true" });
  await api.adoptSession({ connectionId: 1, externalSessionId: "a/b" }, { title: "Plan" });
  assert.deepEqual(calls, [
    { path: "/tasks/7/needs", method: "PUT", body: { needs: [3, 5] } },
    { path: "/trees?status=all&project=2", method: "GET", body: undefined },
    { path: "/trees", method: "POST", body: { title: "T", tasks: [{ taskId: 3 }, { taskId: 7, needs: [3] }] } },
    { path: "/trees/4", method: "PATCH", body: { startMode: "manual" } },
    { path: "/trees/4/tasks", method: "POST", body: { taskId: 9 } },
    { path: "/trees/4/tasks", method: "POST", body: { taskId: 9, needs: [7] } },
    { path: "/trees/4/start", method: "POST", body: undefined },
    { path: "/trees/4", method: "PATCH", body: { status: "done" } },
    { path: "/trees/4", method: "DELETE", body: undefined },
    { path: "/trees/5/dissolve", method: "POST", body: undefined },
    { path: "/tasks/7/archive", method: "POST", body: undefined },
    { path: "/tasks/7/unarchive", method: "POST", body: undefined },
    { path: "/tasks?project=2&closed=true", method: "GET", body: undefined },
    { path: "/connections/1/sessions/a%2Fb/adopt", method: "POST", body: { title: "Plan" } },
  ]);
});

test("a completed task shows one chip until execution moves on", () => {
  for (const state of ["done", "settled", "backlog"]) assert.deepEqual(statusChips(task(1, { state, acceptanceId: 9 })), ["completed"]);
  for (const state of ["running", "needs_input", "launching", "failed", "interrupted", "cancelled"])
    assert.deepEqual(statusChips(task(1, { state, acceptanceId: 9 })), ["completed", state]);
});

test("a finished run not completed yet reads to review", () => {
  assert.deepEqual(statusChips(task(1, { state: "done" })), ["to_review"]);
  assert.deepEqual(statusChips(task(1, { state: "settled" })), ["to_review"]);
});

test("open tasks show readiness in backlog, else their execution state", () => {
  assert.deepEqual(statusChips(task(1, { readiness: "ready" })), ["ready"]);
  assert.deepEqual(statusChips(task(1, { readiness: "blocked", needs: [3] })), ["blocked"]);
  assert.deepEqual(statusChips(task(1)), ["backlog"]);
  assert.deepEqual(statusChips(task(1, { state: "running" })), ["running"]);
  assert.deepEqual(statusChips(task(1, { state: "failed" })), ["failed"]);
});

test("a ready task its connection cannot launch reads backlog; other chips stay", () => {
  assert.deepEqual(statusChips(task(1, { readiness: "ready" }), { blocked: true }), ["backlog"]);
  assert.deepEqual(statusChips(task(1, { readiness: "ready" }), { blocked: false }), ["ready"]);
  assert.deepEqual(statusChips(task(1, { readiness: "blocked", needs: [3] }), { blocked: true }), ["blocked"]);
  assert.deepEqual(statusChips(task(1, { state: "failed" }), { blocked: true }), ["failed"]);
  assert.deepEqual(statusChips(task(1, { state: "backlog", acceptanceId: 9 }), { blocked: true }), ["completed"]);
});
