import assert from "node:assert/strict";
import { test } from "node:test";
import { matchProjects } from "../src/lib/projects.ts";
import { fakeApi } from "./fake-transport.mjs";

test("project edits and bindings send JSON objects through the shared transport", async () => {
  const received = [];
  const { api } = fakeApi(async (path, init) => {
    received.push({ path, method: init.method, body: JSON.parse(init.body) });
    return Response.json({ id: 7, title: "Plan", bindings: [] });
  });
  await api.updateProject(7, {title: "Plan", archived: false});
  await api.bindProject(7, {connectionId: 1, externalId: "remote", workspace: "/work", title: "External", projectId: null});
  assert.deepEqual(received, [
    {path: "/projects/7", method: "PATCH", body: {title: "Plan", archived: false}},
    {path: "/projects/7/binding", method: "PUT", body: {connectionId: 1, externalProjectId: "remote", workspace: "/work"}},
  ]);
});

const project = (id, title, workspaceRoot, lastActivityAt) => ({
  id, title, bindings: [{connectionId: 1, externalProjectId: `t3-${id}`, workspace: workspaceRoot}], archivedAt: null,
  createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z", lastActivityAt,
});

const stage = project(1, "stage", "/home/user/workspace/stage", "2026-11-09T10:00:00Z");
const gitsy = project(2, "gitsy", "/home/user/workspace/gitsy/main", "2026-11-09T12:00:00Z");
const k8s = project(3, "k8s-cluster", "/home/user/workspace-lab/k8s-cluster/main", null);
const stash = project(4, "stash", "/home/user/workspace/stash/main", "2026-11-08T00:00:00Z");
const scratch = project(5, "No project", "/home/user/.t3/scratch", null);
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
  // "sta": stage and stash are both prefix matches, by recency; "scratch" path has no "sta".
  assert.deepEqual(ids(matchProjects(all, "sta")), [1, 4]);
  // "s": prefixes (by recency) stage, stash; then substrings gitsy (most recent overall), k8s-cluster; path only "No project" (/home/user/.t3/scratch).
  assert.deepEqual(ids(matchProjects(all, "s")), [1, 4, 2, 3, 5]);
});

test("matches the workspace path when the title does not", () => {
  const [m, ...rest] = matchProjects(all, "workspace-lab");
  assert.equal(rest.length, 0);
  assert.equal(m.project.id, 3);
  assert.equal(m.title, null);
  assert.deepEqual(m.path, { start: 11, end: 24 });
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
