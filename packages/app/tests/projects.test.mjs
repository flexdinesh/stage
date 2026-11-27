import assert from "node:assert/strict";
import { test } from "node:test";
import {
  currentBindings, filterWorkspaceInventory, importNotice, matchProjects, orderedWorkspaces, primaryWorkspace, projectDetail,
  projectsForConnection, projectWorkspace, repoLabel, suggestedWorkspaceFor, workspaceLocation,
} from "../src/lib/projects.ts";
import { fakeApi } from "./fake-transport.mjs";

test("workspace inventory, pathless binding repair and specific unbind use the scoped routes", async () => {
  const received = [];
  const { api } = fakeApi(async (path, init) => {
    received.push({ path, method: init.method, body: init.body === undefined ? null : JSON.parse(init.body) });
    if (path.endsWith("/workspaces")) return Response.json({ workspaces: [] });
    return Response.json({ id: 7, title: "Plan", bindings: [] });
  });
  await api.updateProject(7, {title: "Plan", archived: false});
  assert.deepEqual(await api.workspaces(1), { workspaces: [] });
  await api.bindProject(7, {connectionId: 1, externalProjectId: "remote", workspace: "", replaces: 42});
  await api.unbindProject(7, 42);
  assert.deepEqual(received, [
    {path: "/projects/7", method: "PATCH", body: {title: "Plan", archived: false}},
    {path: "/connections/1/workspaces", method: "GET", body: null},
    {path: "/projects/7/binding", method: "PUT", body: {connectionId: 1, externalProjectId: "remote", workspace: "", replaces: 42}},
    {path: "/projects/7/bindings/42", method: "DELETE", body: null},
  ]);
});

test("capabilities and project actions can address a non-primary binding", async () => {
  const requested = [];
  const { api } = fakeApi(async (path) => {
    requested.push(path);
    return Response.json([]);
  });
  await api.capabilities(1, { projectId: 7, bindingId: 42 });
  await api.projectActions(7, { connectionId: 1, bindingId: 42 });
  assert.deepEqual(requested, [
    "/connections/1/capabilities?projectId=7&bindingId=42",
    "/projects/7/actions?connectionId=1&bindingId=42",
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
  assert.deepEqual(requests, [
    { path: "/projects?view=destinations&archived=all", signal },
    { path: "/connections/8/capabilities?workspaceId=ws_abc", signal },
  ]);
});

test("a stale workspace confirmation carries the fresh preview", async () => {
  const preview = { applied: false, previewToken: "fresh", placements: [], created: 0, joined: 0, moved: 0, review: 0 };
  const { api } = fakeApi(async () => Response.json(
    { error: "The preview changed. Review it again.", code: "preview_changed", preview },
    { status: 409 },
  ));
  await assert.rejects(api.resolveWorkspaces(1, { apply: true, previewToken: "stale" }), (error) => {
    assert.equal(error.code, "preview_changed");
    assert.deepEqual(error.preview, preview);
    return true;
  });
});

const project = (id, title, workspaceRoot, lastActivityAt, repos = []) => ({
  id, title, bindings: [{id, machineId: 1, connectionId: 1, externalProjectId: `t3-${id}`, workspace: workspaceRoot, connectionActive: true, primary: true}], archivedAt: null,
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

const row = (id, machineId, connectionId, workspace, primary = false, connectionActive = true) =>
  ({ id, machineId, connectionId, workspace, primary, connectionActive, externalProjectId: `workspace-${id}` });

test("project workspaces and search keep all current bindings regardless of availability", () => {
  const retired = row(9, 1, 1, "/old-checkout", true, false);
  const primary = { ...row(10, 2, 2, "/main", true), status: "missing" };
  const alternate = { ...row(11, 2, 2, "/branch"), status: "changed" };
  const item = { ...stage, bindings: [retired, primary, alternate] };

  assert.deepEqual(currentBindings(item.bindings), [primary, alternate]);
  assert.equal(projectWorkspace(item), "/main");
  assert.equal(primaryWorkspace(orderedWorkspaces(item.bindings), retired.connectionId), undefined);
  for (const binding of [primary, alternate]) {
    assert.deepEqual(ids(matchProjects([item], binding.workspace)), [item.id]);
    assert.deepEqual(ids(matchProjects([item], binding.externalProjectId)), [item.id]);
  }
  assert.deepEqual(matchProjects([item], retired.workspace), []);
  assert.deepEqual(matchProjects([item], retired.externalProjectId), []);
});

test("a project with only retired bindings remains selectable and displays as unbound", () => {
  const retired = row(7, 1, 1, "/previous", true, false);
  const item = { ...stage, bindings: [retired] };

  assert.deepEqual(currentBindings(item.bindings), []);
  assert.equal(projectDetail(item), "Workspace not bound");
  assert.deepEqual(ids(matchProjects([item], item.title)), [item.id]);
  assert.deepEqual(matchProjects([item], retired.workspace), []);
  assert.deepEqual(matchProjects([item], retired.externalProjectId), []);
  assert.deepEqual(item.bindings, [retired]);
});

test("orders workspaces by machine and connection, then primary, path and ID", () => {
  const retired = row(9, 1, 1, "/retired", true, false);
  const main = row(5, 1, 2, "/z", true);
  const remote = row(1, 2, 3, "/a", true);
  const other = row(4, 1, 2, "/branch");
  const duplicatePath = row(3, 1, 2, "/branch");
  const bindings = [remote, main, other, retired, duplicatePath];
  const ordered = orderedWorkspaces(bindings);
  assert.deepEqual(ordered.map((b) => b.id), [9, 5, 3, 4, 1]);
  assert.equal(primaryWorkspace(ordered), main);
  assert.equal(primaryWorkspace(ordered, 3), remote);
  assert.equal(primaryWorkspace(ordered, 8), undefined);
  assert.equal(projectWorkspace({ bindings }), "/z");
  assert.equal(workspaceLocation({ ...main, workspace: "", title: "Network workspace" }), "Network workspace");
  assert.equal(workspaceLocation({ ...main, workspace: "", title: "" }), "workspace-5");
  assert.equal(matchProjects([{ ...stage, bindings }], "/branch")[0]?.project.id, stage.id);
});

test("preview requests and confirmations keep scope and the reviewed token", async () => {
  const received = [];
  const { api } = fakeApi(async (path, init) => {
    received.push([path, JSON.parse(init.body)]);
    return Response.json({});
  });
  await api.resolveWorkspaces(7, { apply: false });
  await api.resolveWorkspaces(7, { apply: true, previewToken: "changed-workspaces" });
  await api.mergeProject(42, { from: 43 });
  await api.mergeProject(42, { from: 43, confirm: true, previewToken: "changed-merge" });
  assert.deepEqual(received, [
    ["/connections/7/resolve", { apply: false }],
    ["/connections/7/resolve", { apply: true, previewToken: "changed-workspaces" }],
    ["/projects/42/merge", { from: 43 }],
    ["/projects/42/merge", { from: 43, confirm: true, previewToken: "changed-merge" }],
  ]);
});

test("bind inventory ranks resolver suggestions and filters by the recorded repository", () => {
  const samePath = { title: "App", externalId: "different", workspace: "/work/app", remote: "git.example/fork", projectId: null, resolution: { outcome: "new", candidates: [] } };
  const suggestion = { ...samePath, externalId: "suggestion", remote: "git.example/app", resolution: { outcome: "review", candidates: [7] } };
  const owned = { ...suggestion, externalId: "owned", projectId: 8 };
  const declined = { ...suggestion, externalId: "declined", declined: true };
  const options = { projectId: 7, previousId: undefined, repository: "", query: "app" };
  assert.deepEqual(filterWorkspaceInventory([samePath, owned, suggestion], options), [suggestion, samePath, owned]);
  assert.equal(suggestedWorkspaceFor(samePath, 7), false);
  assert.equal(suggestedWorkspaceFor(owned, 7), false);
  assert.equal(suggestedWorkspaceFor(declined, 7), false);
  assert.deepEqual(filterWorkspaceInventory([samePath, owned, suggestion], { ...options, repository: "git.example/app" }), [suggestion, owned]);
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
