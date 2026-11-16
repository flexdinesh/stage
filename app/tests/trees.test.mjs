import assert from "node:assert/strict";
import { test } from "node:test";
import {
  baseCandidates,
  connectorPaths,
  countMembers,
  layoutLevels,
  needCandidates,
  nextActionText,
  nodeState,
  related,
  rollupLabel,
  treeMeta,
  treeRollup,
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

test("related is direct needs plus direct dependents", () => {
  assert.deepEqual([...related(diamond, 3)].toSorted(), [1, 2, 5]);
  assert.deepEqual([...related(diamond, 1)].toSorted(), [3, 4]);
  assert.deepEqual([...related(diamond, 5)].toSorted(), [3, 4]);
});

test("connectors run down, across in the lane above the dependent band, and down", () => {
  const rects = new Map([
    [1, { x: 0, y: 0, width: 100, height: 50 }],
    [2, { x: 200, y: 0, width: 100, height: 50 }],
    [3, { x: 0, y: 100, width: 100, height: 60 }],
    [4, { x: 200, y: 110, width: 100, height: 50 }],
  ]);
  const nodes = [node(1), node(2), node(3, [1, 2]), node(4, [1])];
  const paths = connectorPaths(nodes, rects, 10);
  assert.deepEqual(
    paths.map((p) => [p.from, p.to]),
    [
      [1, 3],
      [2, 3],
      [1, 4],
    ],
  );
  // Same centre: one straight drop from the need's bottom to the dependent's top.
  assert.equal(paths[0].d, "M50.5 50V100");
  // Lane is 10px above the band top (y 100, the highest box in level 1), not above the lower box.
  assert.equal(paths[1].d, "M250.5 50V90.5H50.5V100");
  assert.equal(paths[2].d, "M50.5 50V90.5H250.5V110");
});

test("connectors skip unmeasured nodes", () => {
  const rects = new Map([[1, { x: 0, y: 0, width: 100, height: 50 }]]);
  assert.deepEqual(connectorPaths([node(1), node(2, [1])], rects), []);
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
