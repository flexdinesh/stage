import assert from "node:assert/strict";
import { test } from "node:test";
import {
  DEFAULT_FOLD,
  LINGER_MS,
  awaitingReview,
  DASHBOARD_GROUPS,
  GROUP_KEYS,
  dashboardGroups,
  dashboardLoad,
  needsYouCount,
  completedSince,
  endLinger,
  filterByTree,
  dashboardProblems,
  harnessNotice,
  inGroup,
  isFolded,
  needsEpisode,
  nextLingerEnd,
  parseFold,
  parseGroupKey,
  parseTreeFilter,
  pruneLinger,
  startLinger,
  toggleFold,
} from "../src/lib/dashboard.ts";

const task = (id, state, extra = {}) => ({ id, state, acceptanceId: null, completedAt: null, treeId: null, readiness: null, ...extra });
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
  assert.deepEqual(groupsOf(task(1, "done", { acceptanceId: 1, completedAt: "2026-11-15T00:00:00Z" })), ["completed"]);
  assert.deepEqual(groupsOf(task(1, "done", { acceptanceId: 1, completedAt: "2026-11-01T00:00:00Z" })), [], "completed before the window");
  assert.deepEqual(groupsOf(task(1, "failed", { acceptanceId: 4 })), [], "completed failures ask nothing");
});

test("a task completed here lingers in Awaiting review, not Completed", () => {
  const t = task(1, "done", { acceptanceId: 9, completedAt: "2026-11-15T00:00:00Z" });
  assert.deepEqual(groupsOf(t, new Set([1])), ["done"]);
  assert.deepEqual(groupsOf(t), ["completed"]);
});

test("the Dashboard lists what asks for the user, then running, then ready", () => {
  assert.deepEqual(DASHBOARD_GROUPS, ["needs", "done", "failed", "running", "ready"]);
  assert.equal(parseGroupKey("running"), "running");
  assert.equal(parseGroupKey("trees"), null);
  assert.equal(parseGroupKey(null), null);
});

test("tree filter: any keeps tree members; none keeps only tasks outside trees, in order", () => {
  const tasks = [task(1, "running", { treeId: 10 }), task(2, "backlog"), task(3, "done", { treeId: 20, acceptanceId: 3 }), task(4, "done")];
  assert.deepEqual(ids(filterByTree(tasks, "any")), [1, 2, 3, 4]);
  assert.deepEqual(ids(filterByTree(tasks, "none")), [2, 4]);
  assert.equal(parseTreeFilter("none"), "none");
  assert.equal(parseTreeFilter(null), "any");
  assert.equal(parseTreeFilter("trees"), "any");
});

test("a lingering task stays in Awaiting review and out of Completed until its linger ends", () => {
  const since = "2026-11-12T00:00:00Z";
  const completed = task(5, "done", { acceptanceId: 9, completedAt: "2026-11-19T10:00:00Z" });
  const review = task(6, "done");
  const none = new Set();
  const linger = new Set([5]);
  assert.equal(awaitingReview(completed, none), false);
  assert.equal(completedSince(completed, since, none), true);
  assert.equal(awaitingReview(completed, linger), true);
  assert.equal(completedSince(completed, since, linger), false);
  assert.equal(awaitingReview(review, none), true);
  // A lingering id never pulls in other states.
  assert.equal(awaitingReview(task(5, "running", { acceptanceId: 9 }), linger), false);
  assert.equal(completedSince(task(7, "done", { acceptanceId: 7, completedAt: "2026-11-01T00:00:00Z" }), since, none), false);
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
  assert.equal(isFolded(DEFAULT_FOLD, "completed"), true);
  assert.equal(isFolded(DEFAULT_FOLD, "running"), false);
  assert.deepEqual(parseFold('{"collapsed":["running",1],"seen":"x"}'), { collapsed: ["running"], seen: [] });
});

test("stored legacy completion folds stay collapsed", () => {
  const fold = parseFold('{"collapsed":["settled","running"],"seen":["12:now"]}');
  assert.deepEqual(fold, { collapsed: ["completed", "running"], seen: ["12:now"] });
  assert.equal(isFolded(fold, "completed"), true);
});

test("toggling round-trips through storage", () => {
  const opened = toggleFold(DEFAULT_FOLD, "completed");
  assert.equal(isFolded(opened, "completed"), false);
  const both = toggleFold(opened, "backlog");
  assert.equal(isFolded(parseFold(JSON.stringify(both)), "backlog"), true);
  assert.equal(isFolded(parseFold(JSON.stringify(both)), "completed"), false);
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

const conn = (harness, over = {}) => ({ harness, machineId: 1, active: true, configured: true, ...over });

test("harness notice: connect while no harness is set up anywhere, nothing once one is or while loading", () => {
  assert.equal(harnessNotice(null), null, "connections loading");
  assert.equal(harnessNotice([]), "connect", "first run: no connections");
  assert.equal(harnessNotice([conn("a", { configured: false }), conn("b", { configured: false })]), "connect", "none set up");
  assert.equal(harnessNotice([conn("a", { active: false })]), "connect", "a replaced connection does not count");
  assert.equal(harnessNotice([conn("a")]), null, "any set-up harness counts");
  assert.equal(harnessNotice([conn("a", { machineId: 2 })]), null, "on any machine");
  assert.equal(harnessNotice([conn("a", { configured: false }), conn("b")]), null);
});

const item = (key, tone) => ({ key, group: "Harnesses", name: key, tone, state: tone, label: `${key} ${tone}` });

test("dashboard problems are the status button's yellow and red items, in its order", () => {
  const items = [item("x", "bad"), item("y", "need"), item("z", "ok"), item("w", "idle")];
  assert.deepEqual(dashboardProblems(items).map((p) => p.key), ["x", "y"]);
  assert.deepEqual(dashboardProblems([item("z", "ok")]), []);
});

const counts = (extra = {}) => ({
  total: 0, backlog: 0, blocked: 0, ready: 0, launching: 0, running: 0, needsInput: 0, done: 0, completed: 0, failed: 0, interrupted: 0, cancelled: 0,
  ...extra,
});
const tree = (id, c, extra = {}) => ({ id, status: "open", completedAt: null, counts: counts(c), ...extra });
const shape = (groups) => groups.map((g) => [g.key, ids(g.trees), ids(g.tasks)]);

test("dashboard groups: tree members list only through their tree, in Dashboard order", () => {
  const tasks = [
    task(1, "running"),
    task(2, "backlog", { readiness: "ready", startAt: null }),
    task(3, "needs_input"),
    task(4, "running", { treeId: 10 }),
    task(5, "backlog", { readiness: "ready", startAt: null, treeId: 11 }),
    task(6, "failed"),
    task(7, "done"),
  ];
  const trees = [tree(10, { total: 2, running: 1, backlog: 1 }), tree(11, { total: 1, backlog: 1, ready: 1 })];
  assert.deepEqual(shape(dashboardGroups(tasks, trees, SINCE, new Set())), [
    ["needs", [], [3]],
    ["done", [], [7]],
    ["failed", [], [6]],
    ["running", [10], [1]],
    ["ready", [11], [2]],
  ]);
});

test("dashboard groups: a tree lists once, under its most pressing step as on the Trees view", () => {
  const mixed = tree(1, { total: 3, running: 1, backlog: 1, ready: 1, done: 1 });
  const asking = tree(2, { total: 2, needsInput: 1, failed: 1 });
  const review = tree(3, { total: 2, done: 1, running: 1 });
  assert.deepEqual(shape(dashboardGroups([], [mixed, asking, review], SINCE, new Set())), [
    ["needs", [2], []],
    ["done", [3], []],
    ["ready", [1], []],
  ]);
});

test("dashboard groups: waiting, completed and closed trees stay off, with their members", () => {
  const waiting = tree(1, { total: 2, backlog: 2, blocked: 2 });
  const completed = tree(2, { total: 1, completed: 1 }, { completedAt: "2026-11-20T00:00:00Z" });
  const closed = tree(3, { total: 1, done: 1 }, { status: "done" });
  const members = [task(4, "backlog", { treeId: 1 }), task(5, "done", { treeId: 3 })];
  assert.deepEqual(dashboardGroups(members, [waiting, completed, closed], SINCE, new Set()), []);
});

test("dashboard counts: a tree counts once; Running is not owed; the load has no backlog", () => {
  const tasks = [task(1, "running"), task(2, "needs_input"), task(3, "done"), task(4, "backlog", { startAt: null })];
  const trees = [tree(10, { total: 2, running: 2 }), tree(11, { total: 3, backlog: 3, ready: 2 })];
  const groups = dashboardGroups(tasks, trees, SINCE, new Set());
  assert.equal(needsYouCount(groups), 3, "needs input, review and one ready tree");
  assert.deepEqual(dashboardLoad(groups), { need: 1, waiting: 2, run: 2, open: 5 });
  assert.deepEqual(dashboardLoad([]), { need: 0, waiting: 0, run: 0, open: 0 });
});
