import assert from "node:assert/strict";
import { test } from "node:test";
import { matchProjects } from "../src/lib/projects.ts";

const project = (id, title, workspaceRoot, lastActivityAt) => ({
  id, t3ProjectId: `t3-${id}`, title, workspaceRoot, source: "t3", archivedAt: null,
  createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z", lastActivityAt,
});

const stage = project(1, "t3-stage", "/home/dee/workspace/t3-stage", "2026-11-09T10:00:00Z");
const gitsy = project(2, "gitsy", "/home/dee/workspace/gitsy/main", "2026-11-09T12:00:00Z");
const k8s = project(3, "k8s-cluster", "/home/dee/workspace-lab/k8s-cluster/main", null);
const stash = project(4, "stash", "/home/dee/workspace/stash/main", "2026-11-08T00:00:00Z");
const scratch = project(5, "No project", "/home/dee/.t3/scratch", null);
const all = [stage, gitsy, k8s, stash, scratch];
const ids = (matches) => matches.map((m) => m.project.id);

test("empty query lists every project by recent activity, inactive last by title", () => {
  assert.deepEqual(ids(matchProjects(all, "")), [2, 1, 4, 3, 5]);
  assert.deepEqual(ids(matchProjects(all, "   ")), [2, 1, 4, 3, 5]);
  assert.ok(matchProjects(all, "").every((m) => m.title === null && m.path === null));
});

test("matches title case-insensitively and reports the matched span", () => {
  const [m] = matchProjects(all, "GIT");
  assert.equal(m.project.id, 2);
  assert.deepEqual(m.title, { start: 0, end: 3 });
});

test("ranks title prefix, then title substring, then path only", () => {
  // "sta": stash and t3-stage... stash is a prefix match; t3-stage a substring; "scratch" path has no "sta".
  assert.deepEqual(ids(matchProjects(all, "sta")), [4, 1]);
  // "s": prefix stash; substrings (by recency) gitsy, t3-stage, k8s-cluster; path only none left except "No project" (/home/dee/.t3/scratch).
  assert.deepEqual(ids(matchProjects(all, "s")), [4, 2, 1, 3, 5]);
});

test("matches the workspace path when the title does not", () => {
  const [m, ...rest] = matchProjects(all, "workspace-lab");
  assert.equal(rest.length, 0);
  assert.equal(m.project.id, 3);
  assert.equal(m.title, null);
  assert.deepEqual(m.path, { start: 10, end: 23 });
});

test("ignores surrounding whitespace and returns nothing for no match", () => {
  assert.deepEqual(ids(matchProjects(all, "  stash ")), [4]);
  assert.deepEqual(matchProjects(all, "zzz"), []);
});

test("does not reorder the input array", () => {
  const input = [...all];
  matchProjects(input, "");
  assert.deepEqual(input, all);
});
