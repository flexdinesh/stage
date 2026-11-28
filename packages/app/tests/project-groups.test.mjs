import assert from "node:assert/strict";
import { test } from "node:test";
import { createApi } from "../src/api/client.ts";
import { hostAccessScope } from "../src/lib/host.ts";
import { canRunProjectActions, duplicateGroupTitles, groupWorkspaceBlocked, groupWorkspaceSummary, isCurrentGroup, requestGroupEdit } from "../src/lib/projectGroups.ts";
import { createSharedSession } from "../src/lib/sharedSession.ts";

test("same-title current groups remain distinct from each other and from project history", () => {
  const first = { id: 1, title: "Build", state: "current", archivedAt: null };
  const second = { id: 2, title: "build", state: "current", archivedAt: null };
  const emptied = { ...first, id: 3, state: "empty" };
  const retired = { ...first, id: 4, state: "retired" };
  const archived = { ...first, id: 5, archivedAt: "2026-11-27" };
  const current = [first, second, emptied, retired, archived].filter(isCurrentGroup);
  assert.deepEqual(current.map(({ id }) => id), [1, 2]);
  assert.deepEqual([...duplicateGroupTitles(current)], ["build"]);
});

test("project actions require a current, listed workspace in that exact group", () => {
  const workspace = { workspaceId: "ws_lab", connectionId: 5, catalogState: "listed", path: "/lab", title: "Lab" };
  const row = { ...workspace, available: true, projectId: 7, groupProjectId: 8 };
  assert.equal(canRunProjectActions(8, workspace, [row]), true);
  assert.equal(canRunProjectActions(7, workspace, [row]), false);
  assert.equal(canRunProjectActions(8, workspace, [{ ...row, connectionId: 9 }]), false);
  assert.equal(canRunProjectActions(8, workspace, [{ ...row, workspaceId: "ws_other" }]), false);
  assert.equal(canRunProjectActions(8, workspace, [{ ...row, available: false }]), false);
  assert.equal(canRunProjectActions(8, { ...workspace, catalogState: "missing" }, [row]), false);
  assert.equal(canRunProjectActions(8, { ...workspace, catalogState: "retired" }, [row]), false);
  assert.equal(canRunProjectActions(8, workspace, undefined), false);
});

test("a workspace its harness archived keeps its project actions, so it can be restored", () => {
  const workspace = { workspaceId: "ws_old", connectionId: 5, catalogState: "archived", path: "/old", title: "Old" };
  // Archived rows are never destinations; membership comes from the group itself.
  assert.equal(canRunProjectActions(8, workspace, []), true);
  assert.equal(canRunProjectActions(8, workspace, undefined), true);
  assert.equal(groupWorkspaceBlocked({ ...workspace, blockedReason: "x" }), null);
});

test("a listed workspace the harness won't start in carries its reason and keeps its project actions", () => {
  const reason = "Not trusted in Claude Code. Use Trust in Claude Code, or run claude in this folder once and accept the trust prompt.";
  const workspace = { workspaceId: "ws_lab", connectionId: 5, catalogState: "listed", path: "/lab", title: "Lab", blockedReason: reason };
  assert.equal(groupWorkspaceBlocked(workspace), reason);
  assert.equal(groupWorkspaceBlocked({ ...workspace, blockedReason: "" }), null);
  assert.equal(groupWorkspaceBlocked({ ...workspace, catalogState: "missing" }), null);
  const row = { ...workspace, available: false, reason, groupProjectId: 8 };
  assert.equal(canRunProjectActions(8, workspace, [row]), true);
  assert.equal(canRunProjectActions(7, workspace, [row]), false);
  assert.equal(canRunProjectActions(8, { ...workspace, catalogState: "missing" }, [row]), false);
});

test("project summaries describe the group workspace count when there are several", () => {
  const member = { path: "/lab", title: "Lab" };
  assert.equal(groupWorkspaceSummary({ members: [] }), "No workspaces");
  assert.equal(groupWorkspaceSummary({ members: [member] }), "/lab");
  assert.equal(groupWorkspaceSummary({ members: [{ ...member, path: "" }] }), "Lab");
  assert.equal(groupWorkspaceSummary({ members: [member, { ...member, path: "/other" }] }), "2 workspaces");
});

test("preview and confirmation use the exact workspace on its connection", async () => {
  const requests = [];
  const api = createApi({
    async request(path, init) {
      requests.push({ path, method: init.method, body: init.body === undefined ? undefined : JSON.parse(init.body), signal: init.signal });
      return Response.json({});
    },
  });
  const signal = new AbortController().signal;
  await api.projectGroups(signal);
  await api.projectGroup(41, signal);
  const input = { connectionId: 5, workspaceId: "ws_lab", targetProjectId: null };
  assert.deepEqual(requests.map(({ path }) => path), ["/project-groups?archived=all", "/project-groups/41"]);
  assert.ok(requests.every((request) => request.signal === signal));

  await requestGroupEdit(api, undefined, hostAccessScope(null), input, signal);
  await requestGroupEdit(api, undefined, hostAccessScope(null), input, signal, "reviewed");
  assert.deepEqual(requests.slice(2), [
    { path: "/project-groups/membership", method: "POST", body: { ...input, previewToken: "" }, signal },
    { path: "/project-groups/membership", method: "POST", body: { ...input, previewToken: "reviewed" }, signal },
  ]);
});

test("a desktop switch blocks an open preview before it can send a write for identical IDs", async () => {
  let state = { target: { id: "main", url: "https://stage-a.example.test" }, accountEpoch: 0 };
  const host = { current: () => state };
  const requests = [];
  const api = { editProjectGroup: (...args) => requests.push(args) };
  const scope = hostAccessScope(host.current());
  const input = { connectionId: 1, workspaceId: "ws_same", targetProjectId: 2 };
  const signal = new AbortController().signal;
  await requestGroupEdit(api, host, scope, input, signal);
  state = { ...state, target: { ...state.target, url: "https://stage-b.example.test" } };
  await assert.rejects(requestGroupEdit(api, host, scope, input, signal, "old-installation"), /changed/);
  state = { target: { id: "main", url: "https://stage-a.example.test" }, accountEpoch: 1 };
  await assert.rejects(requestGroupEdit(api, host, scope, input, signal, "old-account"), /changed/);
  assert.deepEqual(requests, [[input, signal, ""]]);
});

test("closing a shared account aborts a preview and rejects a queued confirmation", async () => {
  const requests = [];
  const session = createSharedSession({
    attention: { setNeedsInput() {} },
    transport: {
      request(path, init) {
        requests.push(path);
        return new Promise((_, reject) => {
          init.signal.addEventListener("abort", () => reject(init.signal.reason), { once: true });
        });
      },
    },
  }, () => {}, () => {});
  const api = createApi(session.platform.transport);
  const signal = new AbortController().signal;
  const input = { connectionId: 1, workspaceId: "ws_same", targetProjectId: 2 };
  const preview = requestGroupEdit(api, undefined, hostAccessScope(null), input, signal);
  session.close();
  await assert.rejects(preview, { name: "AbortError" });
  await assert.rejects(requestGroupEdit(api, undefined, hostAccessScope(null), input, signal, "old-account"), { name: "AbortError" });
  assert.deepEqual(requests, ["/project-groups/membership"]);
});
