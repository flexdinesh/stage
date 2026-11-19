import assert from "node:assert/strict";
import { test } from "node:test";
import {
  DEFAULT_FOLD,
  LINGER_MS,
  awaitingReview,
  ATTENTION_GROUPS,
  GROUP_KEYS,
  completedSince,
  endLinger,
  inGroup,
  isFolded,
  needsEpisode,
  nextLingerEnd,
  parseFold,
  parseGroupKey,
  pruneLinger,
  splitByKind,
  startLinger,
  toggleFold,
} from "../src/lib/dashboard.ts";

const task = (id, state, extra = {}) => ({ id, state, acceptanceId: state === "settled" ? id : null, treeId: null, readiness: null, ...extra });
const tree = (id, settledAt = null, status = "open") => ({ id, settledAt, status });
const ids = (list) => list.map((t) => t.id);

const SINCE = "2026-11-13T00:00:00Z";
const groupsOf = (t, lingering = new Set()) => GROUP_KEYS.filter((k) => inGroup(k, t, SINCE, lingering));

test("each open task lists in exactly one group", () => {
  assert.deepEqual(groupsOf(task(1, "needs_input")), ["needs"]);
  assert.deepEqual(groupsOf(task(1, "done")), ["done"]);
  assert.deepEqual(groupsOf(task(1, "backlog", { readiness: "ready", startAt: null })), ["ready"]);
  assert.deepEqual(groupsOf(task(1, "interrupted")), ["failed"]);
  assert.deepEqual(groupsOf(task(1, "launching")), ["running"]);
  assert.deepEqual(groupsOf(task(1, "backlog", { startAt: null })), ["backlog"]);
  assert.deepEqual(groupsOf(task(1, "backlog", { startAt: "2026-11-20T09:00:00Z" })), ["scheduled"]);
  assert.deepEqual(groupsOf(task(1, "settled", { completedAt: "2026-11-15T00:00:00Z" })), ["settled"]);
  assert.deepEqual(groupsOf(task(1, "settled", { completedAt: "2026-11-01T00:00:00Z" })), [], "completed before the window");
  assert.deepEqual(groupsOf(task(1, "failed", { acceptanceId: 4 })), [], "completed failures ask nothing");
});

test("a task completed here lingers in Awaiting review, not Completed", () => {
  const t = task(1, "done", { acceptanceId: 9, completedAt: "2026-11-15T00:00:00Z" });
  assert.deepEqual(groupsOf(t, new Set([1])), ["done"]);
  assert.deepEqual(groupsOf(t), ["settled"]);
});

test("the Dashboard lists only what asks for the user", () => {
  assert.deepEqual(ATTENTION_GROUPS, ["needs", "done", "ready", "failed"]);
  assert.equal(parseGroupKey("running"), "running");
  assert.equal(parseGroupKey("trees"), null);
  assert.equal(parseGroupKey(null), null);
});

test("splitByKind: tree members leave the task groups; members of unlisted trees stay", () => {
  const tasks = [task(1, "running", { treeId: 10 }), task(2, "backlog"), task(3, "settled", { treeId: 20 }), task(4, "done", { treeId: 99 })];
  const split = splitByKind(tasks, [tree(10), tree(20, "2026-11-15T10:00:00Z")]);
  assert.deepEqual(ids(split.active), [10]);
  assert.deepEqual(ids(split.settled), [20]);
  assert.deepEqual(ids(split.standalone), [2, 4]);
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

test("only Completed starts collapsed; bad storage reads as the default", () => {
  for (const raw of [null, "", "{", "null", "[]", '{"collapsed":3}']) assert.deepEqual(parseFold(raw), DEFAULT_FOLD, String(raw));
  assert.equal(isFolded(DEFAULT_FOLD, "settled"), true);
  assert.equal(isFolded(DEFAULT_FOLD, "running"), false);
  assert.deepEqual(parseFold('{"collapsed":["running",1],"seen":"x"}'), { collapsed: ["running"], seen: [] });
});

test("toggling round-trips through storage", () => {
  const opened = toggleFold(DEFAULT_FOLD, "settled");
  assert.equal(isFolded(opened, "settled"), false);
  const both = toggleFold(opened, "backlog");
  assert.equal(isFolded(parseFold(JSON.stringify(both)), "backlog"), true);
  assert.equal(isFolded(parseFold(JSON.stringify(both)), "settled"), false);
  assert.equal(isFolded(toggleFold(both, "backlog"), "backlog"), false);
});

test("collapsed Needs input reopens for a new needs-input episode", () => {
  const a = needsEpisode({ id: 1, stateSince: "2026-11-19T10:00:00Z" });
  const b = needsEpisode({ id: 2, stateSince: "2026-11-19T10:05:00Z" });
  const fold = toggleFold({ collapsed: [], seen: [] }, "needs", [a]);
  assert.equal(isFolded(fold, "needs", [a]), true);
  // The seen task resolving keeps it collapsed; a new task (or the same task asking again) reopens it.
  assert.equal(isFolded(fold, "needs", []), true);
  assert.equal(isFolded(fold, "needs", [a, b]), false);
  assert.equal(isFolded(fold, "needs", [needsEpisode({ id: 1, stateSince: "2026-11-19T11:00:00Z" })]), false);
  // Collapsing again while reopened acknowledges the new episode instead of expanding.
  const again = toggleFold(fold, "needs", [a, b]);
  assert.equal(isFolded(again, "needs", [a, b]), true);
  assert.deepEqual(again.collapsed, ["needs"]);
  assert.equal(isFolded(toggleFold(again, "needs", [a, b]), "needs", [a, b]), false);
});
