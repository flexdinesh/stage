import assert from "node:assert/strict";
import { test } from "node:test";
import {
  bandState,
  baseCandidates,
  closeBlocker,
  closedLabel,
  countMembers,
  edgeState,
  graphLayout,
  inState,
  layoutLevels,
  needCandidates,
  nextActionText,
  nodeState,
  rollupLabel,
  taskClosedLabel,
  treeClosed,
  treeMeta,
  treeRollup,
  treeMembers,
  treeSettled,
  wouldCycle,
} from "../src/lib/trees.ts";

const node = (id, needs = [], extra = {}) => ({ id, needs, ...extra });

// Diamond: A(1), B(2) -> C(3); A -> D(4); C, D -> E(5).
const diamond = [node(1), node(2), node(3, [1, 2]), node(4, [1]), node(5, [3, 4])];

test("levels follow the longest need path from a root", () => {
  // D (needs A at 0) sits before C (needs A and B, mean 0.5).
  assert.deepEqual(layoutLevels(diamond), [[1, 2], [4, 3], [5]]);
  // Skip level: 3 needs a root and a level-1 task, so it sits at level 2.
  assert.deepEqual(layoutLevels([node(1), node(2, [1]), node(3, [1, 2])]), [[1], [2], [3]]);
  assert.deepEqual(layoutLevels([]), []);
});

test("needs outside the node set and loops do not break layout", () => {
  assert.deepEqual(layoutLevels([node(1, [99]), node(2, [1])]), [[1], [2]]);
  const looped = layoutLevels([node(1, [2]), node(2, [1])]);
  assert.deepEqual(looped.flat().toSorted(), [1, 2]);
});

test("a band orders by the mean position of its needs, then id", () => {
  // Roots 1, 2, 3. 10 needs 3 (pos 2), 11 needs 1 (pos 0), 12 needs 1 and 3 (mean 1), 13 needs 2 (pos 1).
  const nodes = [node(1), node(2), node(3), node(10, [3]), node(11, [1]), node(12, [1, 3]), node(13, [2])];
  assert.deepEqual(layoutLevels(nodes), [[1, 2, 3], [11, 12, 13, 10]]);
});

test("wouldCycle catches self, direct and transitive loops", () => {
  assert.equal(wouldCycle(diamond, 3, 3), true);
  // 1 needing 3 loops: 3 needs 1.
  assert.equal(wouldCycle(diamond, 1, 3), true);
  // 1 needing 5 loops transitively through 3 / 4.
  assert.equal(wouldCycle(diamond, 1, 5), true);
  // 4 needing 2 is fine; 5 needing 2 is fine (already transitive, no loop).
  assert.equal(wouldCycle(diamond, 4, 2), false);
  assert.equal(wouldCycle(diamond, 5, 2), false);
});

test("need candidates exclude self, existing needs and loops", () => {
  assert.deepEqual(needCandidates(diamond, 4).map((n) => n.id), [2, 3]);
  // A root may need any task that does not depend on it.
  assert.deepEqual(needCandidates(diamond, 2).map((n) => n.id), [1, 4]);
  assert.deepEqual(needCandidates(diamond, 5).map((n) => n.id), [1, 2]);
  assert.deepEqual(needCandidates(diamond, 42), []);
});

const size = { nodeW: 100, nodeH: 40, gapX: 10, gapY: 30 };

test("graph layout stacks a chain in one column with vertical edges", () => {
  const chain = [node(1), node(2, [1]), node(3, [2])];
  const g = graphLayout(chain, size);
  assert.deepEqual(g.levels, [[1], [2], [3]]);
  assert.equal(g.width, 100);
  assert.equal(g.height, 3 * 40 + 2 * 30);
  assert.deepEqual(g.nodes, [
    { id: 1, level: 0, x: 0, y: 0 },
    { id: 2, level: 1, x: 0, y: 70 },
    { id: 3, level: 2, x: 0, y: 140 },
  ]);
  assert.deepEqual(g.edges.map((e) => [e.from, e.to]), [[1, 2], [2, 3]]);
  // Bottom centre of the need to just above the dependent's top centre.
  assert.match(g.edges[0].path, /^M50 40C50 \S+ 50 \S+ 50 68$/);
  assert.equal(g.edges[0].arrow, "M46 63L50 68L54 63");
});

test("graph layout centres rows and draws every edge of a fan-in", () => {
  const g = graphLayout(diamond, size);
  assert.deepEqual(g.levels, [[1, 2], [4, 3], [5]]);
  assert.equal(g.width, 210);
  const x = Object.fromEntries(g.nodes.map((n) => [n.id, n.x]));
  assert.deepEqual(x, { 1: 0, 2: 110, 4: 0, 3: 110, 5: 55 });
  assert.deepEqual(
    g.edges.map((e) => `${e.from}>${e.to}`).toSorted(),
    ["1>3", "1>4", "2>3", "3>5", "4>5"],
  );
});

test("graph layout spans skipped levels and ignores needs outside the tree", () => {
  const g = graphLayout([node(1), node(2, [1]), node(3, [1, 2, 99])], size);
  const skip = g.edges.find((e) => e.from === 1 && e.to === 3);
  assert.ok(skip);
  assert.match(skip.path, /^M50 40C/);
  assert.match(skip.path, / 50 138$/);
  assert.equal(g.edges.length, 3);
});

test("graph layout of an empty tree is empty", () => {
  assert.deepEqual(graphLayout([], size), { width: 0, height: 0, levels: [], nodes: [], edges: [] });
});

const bandMember = (state, readiness = null) => ({ state, readiness });

test("band state: live or ready work is now, all settled is done, else waits", () => {
  assert.equal(bandState([bandMember("settled"), bandMember("running")]), "now");
  assert.equal(bandState([bandMember("needs_input")]), "now");
  assert.equal(bandState([bandMember("launching")]), "now");
  assert.equal(bandState([bandMember("backlog", "ready")]), "now");
  assert.equal(bandState([bandMember("settled"), bandMember("settled")]), "done");
  assert.equal(bandState([bandMember("settled"), bandMember("done")]), "waits");
  assert.equal(bandState([bandMember("backlog", "blocked")]), "waits");
  assert.equal(bandState([bandMember("failed")]), "waits");
  assert.equal(bandState([]), "waits");
});

test("edge state follows the need: settled met, failed kinds failed, else waiting", () => {
  assert.equal(edgeState({ state: "settled" }), "met");
  for (const s of ["failed", "interrupted", "cancelled"]) assert.equal(edgeState({ state: s }), "failed");
  for (const s of ["backlog", "launching", "running", "needs_input", "done"]) assert.equal(edgeState({ state: s }), "waiting");
});

const member = (state, readiness = null) => ({ state, readiness });

test("counts split backlog into blocked and ready", () => {
  const c = countMembers([
    member("backlog", "ready"),
    member("backlog", "blocked"),
    member("backlog", "blocked"),
    member("needs_input"),
    member("settled"),
  ]);
  assert.equal(c.total, 5);
  assert.equal(c.backlog, 3);
  assert.equal(c.blocked, 2);
  assert.equal(c.ready, 1);
  assert.equal(c.needsInput, 1);
  assert.equal(c.settled, 1);
});

const counts = (members) => countMembers(members);

test("next action priority: answer > start > retry > settle > running > waiting > done", () => {
  const all = [
    member("needs_input"),
    member("backlog", "ready"),
    member("failed"),
    member("done"),
    member("running"),
    member("backlog", "blocked"),
  ];
  const kinds = [];
  for (let i = 0; i < all.length; i++) kinds.push(treeRollup(counts(all.slice(i))).next.kind);
  assert.deepEqual(kinds, ["answer", "start", "retry", "settle", "running", "waiting"]);
  assert.equal(treeRollup(counts([member("settled"), member("settled")])).next.kind, "done");
  assert.equal(treeRollup(counts([member("interrupted")])).next.kind, "retry");
});

test("segments order finished work first and drop empty states", () => {
  const r = treeRollup(
    counts([member("backlog", "blocked"), member("running"), member("settled"), member("settled"), member("backlog", "ready")]),
  );
  assert.deepEqual(
    r.segments.map((s) => [s.key, s.count, s.hollow]),
    [
      ["settled", 2, false],
      ["running", 1, false],
      ["ready", 1, false],
      ["waiting", 1, true],
    ],
  );
  assert.equal(rollupLabel(r), "5 tasks: 2 settled, 1 running, 1 ready, 1 blocked");
});

test("next action text", () => {
  assert.equal(nextActionText({ kind: "answer", count: 1 }), "1 needs input");
  assert.equal(nextActionText({ kind: "answer", count: 2 }), "2 need input");
  assert.equal(nextActionText({ kind: "settle", count: 2 }), "2 to settle in T3");
  assert.equal(nextActionText({ kind: "done", count: 3 }), "all settled");
});

test("tree meta reads the same for a list row and a detail", () => {
  const summary = { startMode: "auto", counts: { total: 7 }, levels: 3, projects: ["api", "web"] };
  assert.equal(treeMeta(summary), "7 tasks · 3 levels · 2 projects · auto");
  const detail = {
    startMode: "manual",
    tasks: [{ projectId: 1 }],
    levels: [[1]],
  };
  assert.equal(treeMeta(detail), "1 task · 1 level · 1 project · manual");
});

test("base candidates are the task's needs in its own project", () => {
  const tree = {
    tasks: [
      { id: 1, projectId: 10 },
      { id: 2, projectId: 20 },
      { id: 3, projectId: 10 },
      { id: 4, projectId: 10 },
    ],
  };
  assert.deepEqual(baseCandidates({ needs: [3, 2, 1], projectId: 10 }, tree).map((t) => t.id), [1, 3]);
  assert.deepEqual(baseCandidates({ needs: [], projectId: 10 }, tree), []);
});

test("node state reads readiness for backlog members", () => {
  assert.equal(nodeState(member("backlog", "ready")), "ready");
  assert.equal(nodeState(member("backlog", "blocked")), "blocked");
  assert.equal(nodeState(member("backlog")), "backlog");
  assert.equal(nodeState(member("running")), "running");
});

test("a tree is settled only when every member is", () => {
  assert.equal(treeSettled([member("settled"), member("settled")]), true);
  assert.equal(treeSettled([member("settled"), member("done")]), false);
  assert.equal(treeSettled([]), false);
});

test("new tree members: parallel roots, or a chain in the given order", () => {
  const order = [{ id: 5 }, { id: 2 }, { id: 9 }];
  assert.deepEqual(treeMembers(order, "parallel"), [{ taskId: 5 }, { taskId: 2 }, { taskId: 9 }]);
  assert.deepEqual(treeMembers(order, "chain"), [{ taskId: 5 }, { taskId: 2, needs: [5] }, { taskId: 9, needs: [2] }]);
  assert.deepEqual(treeMembers([], "chain"), []);
});

const nov = (d, h, m = 0) => new Date(2026, 10, d, h, m).toISOString();

test("time in state reads per state from when it was entered", () => {
  const now = new Date(2026, 10, 16, 15, 0).getTime();
  const base = { readiness: null, stateSince: null, readyAt: null, createdAt: nov(14, 9, 12), launchedAt: null, finishedAt: null, settledAt: null };
  const t = (extra) => inState({ ...base, ...extra }, now);
  assert.deepEqual(t({ state: "running", stateSince: nov(16, 14, 42) }), { text: "running 18m", at: nov(16, 14, 42) });
  // No running event yet: falls back to the launch.
  assert.equal(t({ state: "running", launchedAt: nov(16, 14, 30) }).text, "running 30m");
  assert.equal(t({ state: "needs_input", stateSince: nov(16, 14, 54) }).text, "waiting 6m");
  assert.equal(t({ state: "done", finishedAt: nov(15, 17, 0), stateSince: nov(15, 17, 5) }).text, "done 22h ago");
  assert.equal(t({ state: "settled", settledAt: nov(15, 10, 5) }).text, "settled yesterday 10:05");
  assert.equal(t({ state: "settled", settledAt: nov(12, 10, 5) }).text, "settled Nov 12");
  assert.equal(t({ state: "failed", stateSince: nov(16, 12, 0) }).text, "failed 3h ago");
  assert.equal(t({ state: "backlog", readiness: "ready", readyAt: nov(16, 12, 0) }).text, "ready 3h");
  // Blocked or plain backlog counts from creation when there is no later backlog event.
  assert.deepEqual(t({ state: "backlog", readiness: "blocked" }), { text: "queued 2d", at: nov(14, 9, 12) });
});

test("closed trees: chip text, blocker and task label", () => {
  const left = countMembers([
    { state: "settled", readiness: null },
    { state: "cancelled", readiness: null },
    { state: "backlog", readiness: "blocked" },
    { state: "backlog", readiness: "blocked" },
  ]);
  assert.equal(closedLabel({ status: "open" }, left), null);
  assert.equal(closedLabel({ status: "done" }, left), "done · 2 not run · 1 cancelled");
  assert.equal(closedLabel({ status: "done" }, countMembers([{ state: "settled", readiness: null }])), "done");
  assert.equal(closedLabel({ status: "archived" }, left), "archived");
  assert.equal(treeClosed({ status: "open" }), false);
  assert.equal(treeClosed({ status: "done" }), true);

  assert.equal(closeBlocker([{ id: 2, state: "cancelled" }, { id: 1, state: "backlog" }]), null);
  assert.equal(closeBlocker([{ id: 5, state: "needs_input" }, { id: 4, state: "running" }]), "#4 is running");
  assert.equal(closeBlocker([{ id: 5, state: "needs_input" }]), "#5 needs input");

  assert.equal(taskClosedLabel({ archivedAt: null, treeStatus: null }), null);
  assert.equal(taskClosedLabel({ archivedAt: null, treeStatus: "open" }), null);
  assert.equal(taskClosedLabel({ archivedAt: "2026-11-16T10:00:00Z", treeStatus: null }), "archived");
  assert.equal(taskClosedLabel({ archivedAt: null, treeStatus: "done" }), "tree done");
  assert.equal(taskClosedLabel({ archivedAt: null, treeStatus: "archived" }), "tree archived");
});
