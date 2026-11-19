import assert from "node:assert/strict";
import { test } from "node:test";
import {
  LINGER_MS,
  awaitingReview,
  breaksOut,
  completedSince,
  endLinger,
  nextLingerEnd,
  pruneLinger,
  splitTreeMembers,
  startLinger,
} from "../src/lib/dashboard.ts";

const task = (id, state, extra = {}) => ({ id, state, acceptanceId: state === "settled" ? id : null, treeId: null, readiness: null, ...extra });
const tree = (id, settledAt = null, status = "open") => ({ id, settledAt, status });
const ids = (list) => list.map((t) => t.id);

test("awaiting review and loose tasks have their own rows", () => {
  const tasks = [
    task(1, "running", { treeId: 10 }),
    task(2, "done", { treeId: 10 }),
    task(3, "failed", { treeId: 10 }),
    task(4, "backlog", { treeId: 10, readiness: "blocked" }),
    task(5, "running"),
    task(6, "backlog"),
  ];
  const split = splitTreeMembers(tasks, [tree(10)]);
  assert.deepEqual(ids(split.rolledUp), [1, 3, 4]);
  assert.deepEqual(ids(split.rows), [2, 5, 6]);
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

test("a lingering completed member keeps its row even when its tree completes", () => {
  // #2 was just completed here; its tree is now complete, so without linger it would roll up into the tree row.
  const tasks = [task(1, "settled", { treeId: 20 }), task(2, "done", { treeId: 20, acceptanceId: 7 })];
  const trees = [tree(20, "2026-11-15T10:00:00Z")];
  assert.deepEqual(ids(splitTreeMembers(tasks, trees).rows), []);
  const split = splitTreeMembers(tasks, trees, new Set([2]));
  assert.deepEqual(ids(split.rows), [2]);
  assert.deepEqual(ids(split.rolledUp), [1]);
});

test("a lingering task stays in Awaiting review and out of Completed until its linger ends", () => {
  const since = "2026-11-12T00:00:00Z";
  const completed = task(5, "done", { acceptanceId: 9, completedAt: "2026-11-19T10:00:00Z" });
  const review = task(6, "settled", { acceptanceId: null, completedAt: null });
  const none = new Set();
  const linger = new Set([5]);
  assert.equal(awaitingReview(completed, none), false);
  assert.equal(completedSince(completed, since, none), true);
  assert.equal(awaitingReview(completed, linger), true);
  assert.equal(completedSince(completed, since, linger), false);
  assert.equal(awaitingReview(review, none), true);
  // A lingering id never pulls in other states.
  assert.equal(awaitingReview(task(5, "running", { acceptanceId: 9 }), linger), false);
  assert.equal(completedSince(task(7, "settled", { completedAt: "2026-11-01T00:00:00Z" }), since, none), false);
});

test("lingers start at LINGER_MS, end on undo, and prune when expired", () => {
  let linger = new Map();
  assert.equal(nextLingerEnd(linger), null);
  linger = startLinger(linger, 5, 1000);
  linger = startLinger(linger, 6, 3000);
  assert.deepEqual([...linger], [[5, 1000 + LINGER_MS], [6, 3000 + LINGER_MS]]);
  assert.equal(nextLingerEnd(linger), 1000 + LINGER_MS);
  assert.equal(pruneLinger(linger, 1000 + LINGER_MS - 1), linger, "nothing ended: same map");
  assert.deepEqual([...pruneLinger(linger, 1000 + LINGER_MS).keys()], [6]);
  assert.deepEqual([...endLinger(linger, 5).keys()], [6]);
  assert.equal(linger.size, 2, "inputs are not mutated");
});
