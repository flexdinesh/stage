import assert from "node:assert/strict";
import { test } from "node:test";
import { blockedStartModes, scheduleBody, scheduleUpdateBody, taskCreateBody, taskUpdateBody, taskWorkspaceLocation, availableTaskWorkspaces, suggestedTaskWorkspace, workspaceLine } from "../src/lib/taskForm.ts";
import { setOption } from "../src/lib/options.ts";

const config = { model: "m", options: [], permission: "", mode: "" };
const template = { projectId: 1, title: "T", prompt: "P", connectionId: 3, workspaceId: "ws_saved", executionConfig: config };
const task = {
  ...template,
  id: 7,
  bindingId: null,
  attachments: [],
  handoff: true,
  startMode: null,
  startAt: null,
};
const update = (patch) => ({
  before: task,
  openedStart: { mode: "backlog", startLocal: "2026-11-23T09:00", tz: "UTC" },
  full: true,
  template,
  attachmentIds: [],
  handoff: null,
  treeStart: null,
  timed: null,
  ...patch,
});

test("taskCreateBody: start now, or at a time, or join a tree with needs and a start mode", () => {
  const base = { notes: "n", attachmentIds: [], timed: null, startNow: true, tree: undefined, treeStart: "inherit" };
  assert.deepEqual(taskCreateBody(template, base), { ...template, initialNote: "n", start: true });
  const timed = { startLocal: "2026-11-23T09:00", startTz: "UTC" };
  assert.deepEqual(taskCreateBody(template, { ...base, timed, attachmentIds: [4] }), { ...template, initialNote: "n", attachmentIds: [4], ...timed });
  assert.deepEqual(taskCreateBody(template, { ...base, startNow: false, tree: { treeId: 9, needs: [5] }, treeStart: "manual" }), {
    ...template,
    initialNote: "n",
    start: false,
    treeId: 9,
    needs: [5],
    startMode: "manual",
  });
});

test("scheduleBody carries the template, notes, images and repeat", () => {
  assert.deepEqual(scheduleBody(template, { notes: "n", attachmentIds: [1], cron: "0 9 * * *", tz: "UTC" }), {
    ...template,
    notes: "n",
    attachmentIds: [1],
    cron: "0 9 * * *",
    tz: "UTC",
  });
});

test("taskUpdateBody sends only edited fields; a task past backlog sends its title", () => {
  assert.deepEqual(taskUpdateBody(update({ template: { ...template, title: "New" } })), { title: "New" });
  assert.deepEqual(taskUpdateBody(update({})), {});
  assert.deepEqual(taskUpdateBody(update({ full: false, template: { ...template, title: "X" } })), { title: "X" });
});

test("a connection change sends reset options without altering unrelated task fields", () => {
  const executionConfig = { model: "", options: [], permission: "", mode: "" };
  const next = { ...template, connectionId: 4, executionConfig };
  assert.deepEqual(taskUpdateBody(update({ template: next })), { connectionId: 4, executionConfig });
});

test("task and schedule payloads pin the exact workspace and backlog can explicitly clear it", () => {
  const taskState = { notes: "", attachmentIds: [], timed: null, startNow: false, tree: undefined, treeStart: "inherit" };
  const scheduleState = { notes: "", attachmentIds: [], cron: "0 9 * * *", tz: "UTC" };
  const pin = { ...template, workspaceId: "ws_unbound" };
  assert.equal(taskCreateBody(pin, taskState).workspaceId, pin.workspaceId);
  assert.equal(scheduleBody(pin, scheduleState).workspaceId, pin.workspaceId);
  assert.equal(taskCreateBody({ ...template, workspaceId: null }, taskState).workspaceId, null);
  assert.deepEqual(taskUpdateBody(update({ template: { ...template, workspaceId: null } })), {
    workspaceId: null,
    executionConfig: config,
  });
});

test("an exact task edit sends the pin and scoped config only when choosing a replacement", () => {
  const before = { ...task, bindingId: 42, workspaceId: "ws_saved" };
  const next = { projectId: before.projectId, title: "Renamed", prompt: before.prompt, connectionId: before.connectionId, workspaceId: before.workspaceId, executionConfig: config };
  assert.deepEqual(taskUpdateBody(update({ before, template: next })), { title: "Renamed" });
  assert.deepEqual(taskUpdateBody(update({ before, template: { ...next, title: before.title, workspaceId: "ws_unbound" } })), {
    workspaceId: "ws_unbound",
    executionConfig: config,
  });
});

test("a schedule edit preserves the saved workspace and repeat until changed", () => {
  const timing = { notes: "n", attachmentIds: [], cron: "0 9 * * 0", tz: "UTC" };
  const next = { projectId: 1, title: "New", prompt: "P", connectionId: 3, workspaceId: "ws_saved", executionConfig: config };
  const before = { ...scheduleBody(next, timing), title: "T", cron: "00 09 * * 7", bindingId: 42, attachments: [] };
  assert.deepEqual(scheduleUpdateBody(before, scheduleBody(next, timing)), { title: "New" });
  const replacement = { ...next, title: before.title, connectionId: 4, workspaceId: "ws_unbound" };
  assert.deepEqual(scheduleUpdateBody(before, scheduleBody(replacement, timing)), {
    connectionId: 4,
    workspaceId: "ws_unbound",
    executionConfig: config,
  });
});

test("workspace choices use only ready, eligible rows on the selected connection", () => {
  const linked = { workspaceId: "ws_linked", connectionId: 3, projectId: 5, groupProjectId: 5, title: "Linked", workspace: "/linked", available: true };
  const unbound = { ...linked, groupProjectId: null, workspaceId: "ws_unbound", title: "Alone" };
  const foreign = { ...linked, connectionId: 4 };
  const archived = { ...linked, workspaceId: "ws_archived", harnessArchived: true };
  const missing = { ...linked, workspaceId: "ws_missing", available: false };
  const inventory = { workspaces: [unbound, foreign, linked, archived, missing], inventory: { state: "ready" } };

  const choices = availableTaskWorkspaces(inventory, 3, 5);
  assert.deepEqual(choices, [linked, unbound]);
  assert.equal(suggestedTaskWorkspace(choices, 5), linked);
  assert.equal(suggestedTaskWorkspace(choices, 6), undefined);
  assert.equal(suggestedTaskWorkspace(choices, undefined), undefined);
  assert.equal(suggestedTaskWorkspace([unbound], undefined), unbound);
  assert.equal(suggestedTaskWorkspace([unbound], 5), undefined);
  const regrouped = { ...linked, groupProjectId: 6 };
  assert.equal(suggestedTaskWorkspace([regrouped], 5), undefined);
  assert.equal(suggestedTaskWorkspace([regrouped], 6), regrouped);
  assert.deepEqual(availableTaskWorkspaces(inventory, 4, undefined), [foreign]);
  for (const state of ["pending", "refreshing", "stale", "unavailable", "retired"]) {
    assert.deepEqual(availableTaskWorkspaces({ ...inventory, inventory: { state } }, 3, 5), []);
  }
});

test("task workspace display keeps the recorded path or pathless external identity", () => {
  const workspace = { bindingId: 42, path: "/original", externalId: "checkout-2", source: "pinned" };
  assert.equal(taskWorkspaceLocation(workspace), "/original");
  assert.equal(taskWorkspaceLocation({ ...workspace, path: "" }), "checkout-2");
  assert.equal(taskWorkspaceLocation({ bindingId: 42, path: "", source: "pinned" }), "Binding #42");
});

test("the workspace line reads status, then the launch check, then the path, then what is missing", () => {
  const none = { status: null, path: null, checkingLaunch: false, backlogNeedsWorkspace: false, hasConnection: false };
  assert.deepEqual(workspaceLine(none), { text: "No connection selected", isPath: false });
  assert.deepEqual(workspaceLine({ ...none, hasConnection: true }), { text: "No workspace selected", isPath: false });
  assert.deepEqual(workspaceLine({ ...none, hasConnection: true, backlogNeedsWorkspace: true }),
    { text: "Choose a workspace before starting this backlog task.", isPath: false });
  const chosen = { ...none, hasConnection: true, path: "/home/dee/stage" };
  assert.deepEqual(workspaceLine(chosen), { text: "/home/dee/stage", isPath: true });
  assert.deepEqual(workspaceLine({ ...chosen, checkingLaunch: true }), { text: "Checking launch support…", isPath: false });
  assert.deepEqual(workspaceLine({ ...chosen, checkingLaunch: true, status: "Discovering workspaces…" }),
    { text: "Discovering workspaces…", isPath: false });
  assert.deepEqual(workspaceLine({ ...none, status: "Connection unavailable.", backlogNeedsWorkspace: true }),
    { text: "Connection unavailable.", isPath: false });
});

test("task creation sends explicit Off and omits the option after choosing Harness default", () => {
  const boolean = { id: "fast", kind: "boolean", values: [true, false], default: false };
  const state = { notes: "", attachmentIds: [], timed: null, startNow: false, tree: undefined, treeStart: "inherit" };
  const off = setOption([], [boolean], "fast", false);
  const defaults = setOption(off, [boolean], "fast", undefined);
  const payload = (options) => JSON.parse(JSON.stringify(taskCreateBody({
    ...template, executionConfig: { ...config, options },
  }, state))).executionConfig.options;
  assert.deepEqual(payload(off), [{ id: "fast", value: false }]);
  assert.deepEqual(payload(defaults), []);
});

test("taskUpdateBody: handoff and tree start only when edited and changed", () => {
  assert.deepEqual(taskUpdateBody(update({ handoff: true })), {});
  assert.deepEqual(taskUpdateBody(update({ handoff: false })), { handoff: false });
  assert.deepEqual(taskUpdateBody(update({ treeStart: "inherit" })), {});
  assert.deepEqual(taskUpdateBody(update({ treeStart: "auto" })), { startMode: "auto" });
});

test("taskUpdateBody: an unchanged start time is not resent; a new one is; leaving At clears it", () => {
  const at = { startLocal: "2026-11-23T09:00", startTz: "UTC" };
  const scheduled = { ...task, startAt: "2026-11-23T09:00:00Z" };
  const opened = { mode: "at", startLocal: at.startLocal, tz: "UTC" };
  assert.deepEqual(taskUpdateBody(update({ before: scheduled, openedStart: opened, timed: at })), {});
  const later = { startLocal: "2026-11-23T10:00", startTz: "UTC" };
  assert.deepEqual(taskUpdateBody(update({ before: scheduled, openedStart: opened, timed: later })), later);
  assert.deepEqual(taskUpdateBody(update({ before: scheduled, openedStart: opened, timed: null })), { startLocal: null, startTz: null });
});

test("a harness that cannot launch leaves a new task Backlog only", () => {
  const modes = ["now", "backlog", "at", "repeat"];
  assert.deepEqual(blockedStartModes(modes, true, null), ["now", "at", "repeat"]);
  assert.deepEqual(blockedStartModes(modes, false, null), []);
});

test("an edited task keeps the timing it opened with while its harness cannot launch", () => {
  assert.deepEqual(blockedStartModes(["backlog", "at"], true, "at"), []);
  assert.deepEqual(blockedStartModes(["backlog", "at"], true, "backlog"), ["at"]);
  assert.deepEqual(blockedStartModes([], true, "backlog"), []);
});
