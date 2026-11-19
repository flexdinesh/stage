import assert from "node:assert/strict";
import { test } from "node:test";
import {
  attentionOf,
  loadMark,
  parsePinned,
  parseScope,
  projectLoads,
  sidebarProjects,
  togglePin,
  totalLoad,
} from "../src/lib/scope.ts";

const task = (id, projectId, state, extra = {}) => ({
  id,
  projectId,
  state,
  readiness: null,
  acceptanceId: null,
  ...extra,
});

const project = (id, title, lastActivityAt = null) => ({ id, title, lastActivityAt });

test("attentionOf follows the Dashboard groups", () => {
  assert.equal(attentionOf(task(1, 1, "needs_input")), "need");
  assert.equal(attentionOf(task(1, 1, "running")), "run");
  assert.equal(attentionOf(task(1, 1, "launching", { acceptanceId: 9 })), "run");
  assert.equal(attentionOf(task(1, 1, "done")), "review");
  assert.equal(attentionOf(task(1, 1, "settled")), "review");
  assert.equal(attentionOf(task(1, 1, "settled", { acceptanceId: 3 })), null, "completed work asks nothing");
  assert.equal(attentionOf(task(1, 1, "interrupted")), "failed");
  assert.equal(attentionOf(task(1, 1, "backlog", { readiness: "ready" })), "ready");
  assert.equal(attentionOf(task(1, 1, "backlog")), null);
  assert.equal(attentionOf(task(1, 1, "cancelled")), null);
});

test("projectLoads counts open work per project and skips completed tasks", () => {
  const loads = projectLoads([
    task(1, 10, "needs_input"),
    task(2, 10, "running"),
    task(3, 10, "done"),
    task(4, 10, "backlog"),
    task(5, 10, "settled", { acceptanceId: 5 }),
    task(6, 20, "failed"),
  ]);
  assert.deepEqual(loads.get(10), { need: 1, waiting: 1, run: 1, open: 4 });
  assert.deepEqual(loads.get(20), { need: 0, waiting: 1, run: 0, open: 1 });
  assert.equal(loads.has(30), false);
  assert.deepEqual(totalLoad(loads.values()), { need: 1, waiting: 2, run: 1, open: 5 });
});

const load = (need, waiting, run) => ({ need, waiting, run, open: need + waiting + run });

test("loadMark: need outranks run, and the count is what the user owes", () => {
  assert.deepEqual(loadMark(load(1, 2, 1)), { square: "need", owed: 3 });
  assert.deepEqual(loadMark(load(0, 1, 1)), { square: "run", owed: 1 });
  assert.deepEqual(loadMark(load(0, 0, 2)), { square: "run", owed: 0 });
  assert.deepEqual(loadMark(load(0, 1, 0)), { square: null, owed: 1 });
  assert.deepEqual(loadMark(load(0, 0, 0)), { square: null, owed: 0 });
});

test("sidebarProjects: pinned first, active by recent activity, quiet folded", () => {
  const projects = [
    project(1, "alpha", "2026-11-01T00:00:00Z"),
    project(2, "beta", "2026-11-03T00:00:00Z"),
    project(3, "gamma", "2026-11-02T00:00:00Z"),
    project(4, "delta", null),
    project(5, "eps", "2026-10-01T00:00:00Z"),
  ];
  const loads = new Map([
    [1, { need: 0, waiting: 0, run: 0, open: 2 }],
    [3, { need: 1, waiting: 0, run: 0, open: 1 }],
    [5, { need: 0, waiting: 0, run: 0, open: 1 }],
  ]);
  const out = sidebarProjects(projects, loads, [5], null);
  assert.deepEqual(out.pinned.map((p) => p.title), ["eps"], "a pinned project leaves the other lists");
  assert.deepEqual(out.active.map((p) => p.title), ["gamma", "alpha"]);
  assert.deepEqual(out.quiet.map((p) => p.title), ["beta", "delta"], "never-active last");
  assert.deepEqual(
    sidebarProjects(projects, loads, [], 2).active.map((p) => p.title),
    ["beta", "gamma", "alpha", "eps"],
    "the selected scope stays visible while quiet",
  );
});

test("scope and pins survive malformed storage", () => {
  assert.equal(parseScope(null), null);
  assert.equal(parseScope("12"), 12);
  assert.equal(parseScope("abc"), null);
  assert.equal(parseScope("-3"), null);
  assert.deepEqual(parsePinned(null), []);
  assert.deepEqual(parsePinned("{"), []);
  assert.deepEqual(parsePinned('{"a":1}'), []);
  assert.deepEqual(parsePinned('[3,"x",4.5,7]'), [3, 7]);
  assert.deepEqual(togglePin([3, 7], 7), [3]);
  assert.deepEqual(togglePin([3], 7), [3, 7]);
});
