import assert from "node:assert/strict";
import { test } from "node:test";
import { breaksOut, splitTreeMembers } from "../src/lib/dashboard.ts";

const task = (id, state, extra = {}) => ({ id, state, treeId: null, readiness: null, ...extra });
const tree = (id, settledAt = null, status = "open") => ({ id, settledAt, status });
const ids = (list) => list.map((t) => t.id);

test("members of an active tree roll up; loose tasks keep their rows", () => {
  const tasks = [
    task(1, "running", { treeId: 10 }),
    task(2, "done", { treeId: 10 }),
    task(3, "failed", { treeId: 10 }),
    task(4, "backlog", { treeId: 10, readiness: "blocked" }),
    task(5, "running"),
    task(6, "backlog"),
  ];
  const split = splitTreeMembers(tasks, [tree(10)]);
  assert.deepEqual(ids(split.rolledUp), [1, 2, 3, 4]);
  assert.deepEqual(ids(split.rows), [5, 6]);
  assert.deepEqual(ids(split.active), [10]);
  assert.deepEqual(split.settled, []);
});

test("needs-input and ready members break out of their tree row", () => {
  const tasks = [
    task(1, "needs_input", { treeId: 10 }),
    task(2, "backlog", { treeId: 10, readiness: "ready" }),
    task(3, "backlog", { treeId: 10, readiness: "blocked" }),
  ];
  const split = splitTreeMembers(tasks, [tree(10)]);
  assert.deepEqual(ids(split.rows), [1, 2]);
  assert.deepEqual(ids(split.rolledUp), [3]);
  assert.equal(breaksOut(tasks[0]), true);
  assert.equal(breaksOut(tasks[1]), true);
  assert.equal(breaksOut(tasks[2]), false);
});

test("settled members roll up only into a completed tree", () => {
  const tasks = [
    task(1, "settled", { treeId: 10 }), // active tree: stays in Settled
    task(2, "running", { treeId: 10 }),
    task(3, "settled", { treeId: 20 }), // completed tree: its row replaces it
    task(4, "settled", { treeId: 20 }),
    task(5, "settled"),
  ];
  const split = splitTreeMembers(tasks, [tree(10), tree(20, "2026-11-15T10:00:00Z")]);
  assert.deepEqual(ids(split.rows), [1, 5]);
  assert.deepEqual(ids(split.rolledUp), [2, 3, 4]);
  assert.deepEqual(ids(split.active), [10]);
  assert.deepEqual(ids(split.settled), [20]);
});

test("members of trees not listed keep their rows (trees not loaded, or filtered out)", () => {
  const tasks = [task(1, "running", { treeId: 10 }), task(2, "settled", { treeId: 10 })];
  const split = splitTreeMembers(tasks, []);
  assert.deepEqual(ids(split.rows), [1, 2]);
  assert.deepEqual(split.rolledUp, []);
});

test("done and archived trees are left out of the Dashboard split", () => {
  const tasks = [task(1, "running", { treeId: 10 }), task(2, "settled", { treeId: 20 })];
  const split = splitTreeMembers(tasks, [tree(10, null, "done"), tree(20, "2026-11-15T10:00:00Z", "archived"), tree(30)]);
  assert.deepEqual(ids(split.active), [30]);
  assert.deepEqual(split.settled, []);
  assert.deepEqual(ids(split.rows), [1, 2]);
  assert.deepEqual(split.rolledUp, []);
});
