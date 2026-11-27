import assert from "node:assert/strict";
import { test } from "node:test";
import {
  importNotice, matchProjects, orderedWorkspaces, repoLabel, workspaceLocation,
} from "../src/lib/projects.ts";
import { fakeApi } from "./fake-transport.mjs";

test("project writes and workspace inventory use their current routes", async () => {
  const received = [];
  const { api } = fakeApi(async (path, init) => {
    received.push({ path, method: init.method, body: init.body === undefined ? null : JSON.parse(init.body) });
    if (path.endsWith("/workspaces")) return Response.json({ workspaces: [] });
    return Response.json({ id: 7, title: "Plan", bindings: [] });
  });
  await api.createProject({ title: "Plan" });
  await api.updateProject(7, { title: "Plan", archived: false });
  assert.deepEqual(await api.workspaces(1), { workspaces: [] });
  assert.deepEqual(received, [
    { path: "/projects", method: "POST", body: { title: "Plan" } },
    { path: "/projects/7", method: "PATCH", body: { title: "Plan", archived: false } },
    { path: "/connections/1/workspaces", method: "GET", body: null },
  ]);
});

test("project action declarations and dispatch address the same exact workspace", async () => {
  const requested = [];
  const { api } = fakeApi(async (path, init) => {
    requested.push({
      path, method: init.method, signal: init.signal,
      confirmed: new Headers(init.headers).get("Stage-Action-Confirmed"), body: init.body,
    });
    return Response.json([]);
  });
  const { signal } = new AbortController();
  const scope = { connectionId: 1, workspaceId: "ws_secondary" };
  await api.projectActions(7, scope, signal);
  await api.runProjectAction(7, scope, "test/refresh", { name: "run" }, true);
  assert.deepEqual(requested, [
    { path: "/projects/7/actions?connectionId=1&workspaceId=ws_secondary", method: "GET", signal, confirmed: null, body: undefined },
    { path: "/projects/7/actions/test%2Frefresh?connectionId=1&workspaceId=ws_secondary", method: "POST", signal: undefined, confirmed: "1", body: '{"name":"run"}' },
  ]);
});

test("destination reads and capabilities carry their query scope and cancellation signal", async () => {
  const requests = [];
  const { api } = fakeApi(async (path, init) => {
    requests.push({ path, signal: init.signal });
    return Response.json({ projects: [], connections: [], destinations: [] });
  });
  const { signal } = new AbortController();
  await api.destinations(signal);
  await api.capabilities(8, { workspaceId: "ws_abc" }, signal);
  await api.capabilities(8, undefined, signal);
  assert.deepEqual(requests, [
    { path: "/projects?view=destinations&archived=all", signal },
    { path: "/connections/8/capabilities?workspaceId=ws_abc", signal },
    { path: "/connections/8/capabilities", signal },
  ]);
});

test("a stale workspace confirmation carries the fresh preview", async () => {
  const preview = { applied: false, previewToken: "fresh", workspace: { connectionId: 1, workspaceId: "ws_lab" } };
  const { api } = fakeApi(async () => Response.json(
    { error: "The preview changed. Review it again.", code: "preview_changed", preview },
    { status: 409 },
  ));
  const input = { connectionId: 1, workspaceId: "ws_lab", targetProjectId: 2 };
  await assert.rejects(api.editProjectGroup(input, new AbortController().signal, "stale"), (error) => {
    assert.equal(error.code, "preview_changed");
    assert.deepEqual(error.preview, preview);
    return true;
  });
});

const project = (id, title, lastActivityAt, repos = []) => ({
  id, title, bindings: [], archivedAt: null,
  createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z", lastActivityAt, origin: null, importedAt: null, repos,
});

const stage = project(1, "stage", "2026-11-09T10:00:00Z");
const gitsy = project(2, "gitsy", "2026-11-09T12:00:00Z");
const k8s = project(3, "k8s-cluster", null);
const stash = project(4, "stash", "2026-11-08T00:00:00Z");
const scratch = project(5, "No project", null);
const all = [stage, gitsy, k8s, stash, scratch];
const groups = new Map([
  [stage.id, { members: [{ path: "/home/user/workspace/stage", title: "stage", catalogState: "listed" }] }],
  [gitsy.id, { members: [{ path: "/home/user/workspace/gitsy/main", title: "gitsy", catalogState: "listed" }] }],
  [k8s.id, { members: [{ path: "/home/user/workspace-lab/k8s-cluster/main", title: "k8s-cluster", catalogState: "listed" }] }],
  [stash.id, { members: [{ path: "/home/user/workspace/stash/main", title: "stash", catalogState: "listed" }] }],
  [scratch.id, { members: [{ path: "/home/user/.t3/scratch", title: "No project", catalogState: "listed" }] }],
]);
const match = (projects, query) => matchProjects(projects, groups, query);
const ids = (matches) => matches.map((m) => m.project.id);

test("empty query lists every project by recent activity, inactive last by title", () => {
  assert.deepEqual(ids(match(all, "")), [2, 1, 4, 3, 5]);
  assert.deepEqual(ids(match(all, "   ")), [2, 1, 4, 3, 5]);
  assert.ok(match(all, "").every((m) => m.title === null && m.path === null));
});

test("matches title case-insensitively and reports the matched span", () => {
  const [m] = match(all, "GIT");
  assert.equal(m.project.id, 2);
  assert.deepEqual(m.title, { start: 0, end: 3 });
});

test("ranks title prefix, then title substring, then path only", () => {
  // "sta": stage and stash are both prefix matches, by recency; "scratch" path has no "sta".
  assert.deepEqual(ids(match(all, "sta")), [1, 4]);
  // "s": prefixes (by recency) stage, stash; then substrings gitsy (most recent overall), k8s-cluster; path only "No project" (/home/user/.t3/scratch).
  assert.deepEqual(ids(match(all, "s")), [1, 4, 2, 3, 5]);
});

test("matches the workspace path when the title does not", () => {
  const [m, ...rest] = match(all, "workspace-lab");
  assert.equal(rest.length, 0);
  assert.equal(m.project.id, 3);
  assert.equal(m.title, null);
  assert.deepEqual(m.path, { start: 11, end: 24 });
});

test("ignores surrounding whitespace and returns nothing for no match", () => {
  assert.deepEqual(ids(match(all, "  stash ")), [4]);
  assert.deepEqual(match(all, "zzz"), []);
});

test("does not reorder the input array", () => {
  const input = [...all];
  match(input, "");
  assert.deepEqual(input, all);
});

const site = project(6, "site", null, ["github.com/flex/dineshpandiyan.com"]);
const lab = project(7, "lab", null, ["gitlab.com/team/infra"]);

test("matches repository metadata after title and workspace", () => {
  assert.equal(repoLabel("github.com/o/r"), "o/r");
  const [result, ...rest] = match([...all, site, lab], "pandiyan");
  assert.equal(rest.length, 0);
  assert.equal(result.project.id, 6);
  assert.equal(result.repo, true);
  const label = repoLabel(site.repos[0]);
  assert.equal(label.slice(result.path.start, result.path.end), "pandiyan");
  assert.deepEqual(ids(match([site, lab], "gitlab")), [7]);
  const flexTitle = project(8, "flex tools", null);
  assert.deepEqual(ids(match([site, flexTitle], "flex")), [8, 6]);
});

const row = (id, machineId, connectionId, workspace, primary = false, connectionActive = true) =>
  ({ id, machineId, connectionId, workspace, primary, connectionActive, externalProjectId: `workspace-${id}` });

test("project search follows current group membership and preserves title search for history", () => {
  const item = { ...stage, bindings: [row(9, 1, 1, "/previous", true)] };
  const members = [
    { path: "/moved", title: "Moved", catalogState: "missing" },
    { path: "/new", title: "New", catalogState: "listed" },
    { path: "/retired", title: "Retired", catalogState: "retired" },
    { path: "", title: "Network workspace", catalogState: "listed" },
  ];
  const currentGroups = new Map([[item.id, { members }]]);
  for (const q of ["/moved", "/new", "network workspace", item.title]) {
    assert.deepEqual(ids(matchProjects([item], currentGroups, q)), [item.id]);
  }
  for (const q of ["/previous", "/retired"]) {
    assert.deepEqual(matchProjects([item], currentGroups, q), []);
  }
  assert.deepEqual(ids(matchProjects([item], new Map(), item.title)), [item.id]);
});

test("orders recorded workspaces by machine and connection, then recorded primary, path and ID", () => {
  const retired = row(9, 1, 1, "/retired", true, false);
  const main = row(5, 1, 2, "/z", true);
  const remote = row(1, 2, 3, "/a", true);
  const other = row(4, 1, 2, "/branch");
  const duplicatePath = row(3, 1, 2, "/branch");
  const ordered = orderedWorkspaces([remote, main, other, retired, duplicatePath]);
  assert.deepEqual(ordered.map((b) => b.id), [9, 5, 3, 4, 1]);
  assert.equal(workspaceLocation({ ...main, workspace: "", title: "Network workspace" }), "Network workspace");
  assert.equal(workspaceLocation({ ...main, workspace: "", title: "" }), "workspace-5");
});

const kindName = (kind) => ({ t3: "T3", opencode: "OpenCode" })[kind] ?? kind;

test("import notice names harnesses and the latest import", () => {
  const now = Date.parse("2026-11-19T12:00:00Z");
  assert.equal(importNotice([], kindName, now), null);
  assert.equal(importNotice([{connectionId: 2, harness: "opencode", count: 12, at: "2026-11-19T10:00:00Z"}], kindName, now), "12 imported from OpenCode 2h ago");
  assert.equal(
    importNotice([
      {connectionId: 2, harness: "opencode", count: 12, at: "2026-11-19T10:00:00Z"},
      {connectionId: 1, harness: "t3", count: 3, at: "2026-11-18T10:00:00Z"},
    ], kindName, now),
    "12 imported from OpenCode, 3 from T3 2h ago",
  );
});
