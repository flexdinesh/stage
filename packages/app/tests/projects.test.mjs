import assert from "node:assert/strict";
import { test } from "node:test";
import { importNotice, matchProjects, projectDetail, projectsForConnection, repoLabel } from "../src/lib/projects.ts";
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

const project = (id, title, workspaceRoot, lastActivityAt, repos = []) => ({
  id, title, bindings: [{connectionId: 1, externalProjectId: `t3-${id}`, workspace: workspaceRoot, connectionActive: true}], archivedAt: null,
  createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z", lastActivityAt, origin: null, importedAt: null, repos,
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

const site = project(6, "site", "/home/user/workspace/blog", null, ["github.com/flex/dineshpandiyan.com"]);
const lab = project(7, "lab", "/home/user/lab", null, ["gitlab.com/team/infra"]);

test("subtext is path, then repo; GitHub's host is implied", () => {
  assert.equal(projectDetail(site), "/home/user/workspace/blog · flex/dineshpandiyan.com");
  assert.equal(projectDetail(lab), "/home/user/lab · gitlab.com/team/infra");
  assert.equal(projectDetail(stage), "/home/user/workspace/stage");
  assert.equal(repoLabel("github.com/o/r"), "o/r");
});

test("matches repo metadata after title and path, highlighting it in the subtext", () => {
  const [m, ...rest] = matchProjects([...all, site, lab], "pandiyan");
  assert.equal(rest.length, 0);
  assert.equal(m.project.id, 6);
  assert.equal(m.repo, true);
  const detail = projectDetail(site);
  assert.equal(detail.slice(m.path.start, m.path.end), "pandiyan");
  // Non-GitHub hosts stay searchable.
  assert.deepEqual(ids(matchProjects([site, lab], "gitlab")), [7]);
  // A title match ranks before a repo-only match.
  const flexTitle = project(8, "flex tools", "/x", null);
  assert.deepEqual(ids(matchProjects([site, flexTitle], "flex")), [8, 6]);
});

test("a chosen harness narrows the picker to its projects and unbound ones", () => {
  const openCode = {...project(9, "oc", "/w/oc", null), bindings: [{connectionId: 2, workspace: "/w/oc", connectionActive: true}]};
  const unbound = {...project(10, "plan", "/w/plan", null), bindings: []};
  const retiredOnly = {...project(11, "old", "/w/old", null), bindings: [{connectionId: 3, workspace: "/w/old", connectionActive: false}]};
  const list = [stage, openCode, unbound, retiredOnly];
  assert.deepEqual(projectsForConnection(list, 0, "").map((p) => p.id), [1, 9, 10, 11]);
  assert.deepEqual(projectsForConnection(list, 2, "").map((p) => p.id), [9, 10, 11]);
  // The current selection stays listed.
  assert.deepEqual(projectsForConnection(list, 2, "1").map((p) => p.id), [1, 9, 10, 11]);
});

test("import notice names harnesses and the latest import", () => {
  const now = Date.parse("2026-11-19T12:00:00Z");
  assert.equal(importNotice([], now), null);
  assert.equal(importNotice([{connectionId: 2, harness: "opencode", count: 12, at: "2026-11-19T10:00:00Z"}], now), "12 imported from OpenCode 2h ago");
  assert.equal(
    importNotice([
      {connectionId: 2, harness: "opencode", count: 12, at: "2026-11-19T10:00:00Z"},
      {connectionId: 1, harness: "t3", count: 3, at: "2026-11-18T10:00:00Z"},
    ], now),
    "12 imported from OpenCode, 3 from T3 2h ago",
  );
});
