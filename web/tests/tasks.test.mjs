import assert from "node:assert/strict";
import { test } from "node:test";
import { api } from "../src/api/client.ts";
import { batchPath } from "../src/lib/sessions.ts";
import { batchSize, canReorder, canSelectForOrder, chainAction, chainProgress, moveItem } from "../src/lib/tasks.ts";

const task = (id, extra = {}) => ({
  id, title: `t${id}`, state: "backlog", afterTaskId: null, afterThreadId: null, afterThreadTitle: null, startAt: null, ...extra,
});

test("only backlog tasks can be picked to link in order", () => {
  assert.equal(canSelectForOrder(task(1)), true);
  assert.equal(canSelectForOrder(task(1, { afterThreadId: "plan" })), true); // its session wait is replaced or kept as head
  for (const state of ["running", "needs_input", "done", "settled", "failed"]) assert.equal(canSelectForOrder(task(1, { state })), false);
});

test("a chain is reorderable only while every step is backlog", () => {
  assert.equal(canReorder([task(1), task(2, { afterTaskId: 1 })]), true);
  assert.equal(canReorder([task(1, { state: "running" }), task(2, { afterTaskId: 1 })]), false);
  assert.equal(canReorder([task(1)]), false);
});

test("Start chain follows the head: start, start now (timed), confirm unlink (waiting), none (ran)", () => {
  assert.equal(chainAction(task(1)), "start");
  assert.equal(chainAction(task(1, { startAt: "2026-11-15T02:00:00Z" })), "start_now");
  assert.equal(chainAction(task(1, { afterThreadId: "plan" })), "confirm");
  assert.equal(chainAction(task(1, { afterTaskId: 9 })), "confirm");
  assert.equal(chainAction(task(1, { state: "running" })), "none");
  assert.equal(chainAction(task(1, { state: "failed" })), "none");
});

test("chain progress counts settled steps", () => {
  assert.equal(chainProgress([task(1), task(2)]), null);
  assert.equal(chainProgress([task(1, { state: "settled" }), task(2, { state: "running" }), task(3)]), "1 of 3 settled");
});

test("moveItem moves one item and clamps at the ends", () => {
  assert.deepEqual(moveItem([1, 2, 3], 2, -1), [1, 3, 2]);
  assert.deepEqual(moveItem([1, 2, 3], 0, 1), [2, 1, 3]);
  assert.deepEqual(moveItem([1, 2, 3], 0, -1), [1, 2, 3]);
  assert.deepEqual(moveItem([1, 2, 3], 2, 1), [1, 2, 3]);
});

test("batch size counts chains and independent tasks", () => {
  assert.equal(batchSize({ chains: [[task(1), task(2)], [task(3), task(4)]], independent: [task(5)] }), 5);
  assert.equal(batchSize({ chains: [], independent: [] }), 0);
});

test("HTTP contract: order posts ids in run order; batch links anchor the session panel", async (context) => {
  let call;
  context.mock.method(globalThis, "fetch", async (path, options) => {
    call = { path, method: options.method, body: JSON.parse(options.body) };
    return Response.json({ tasks: [] });
  });
  await api.orderTasks([7, 3, 5]);
  assert.equal(new URL(call.path, "http://stage.test").pathname, "/api/tasks/order");
  assert.equal(call.method, "POST");
  assert.deepEqual(call.body, { taskIds: [7, 3, 5] });
  assert.equal(batchPath("a/b"), "/sessions/a%2Fb#created-tasks");
});
