import assert from "node:assert/strict";
import { test } from "node:test";
import { blockedStartModes, scheduleBody, scheduleUpdateBody, taskCreateBody, taskUpdateBody, taskWorkspaceLocation, taskWorkspaceSelection } from "../src/lib/taskForm.ts";
import { setOption } from "../src/lib/options.ts";

const config = { model: "m", options: [], permission: "", mode: "" };
const template = { projectId: 1, title: "T", prompt: "P", connectionId: 3, bindingId: null, executionConfig: config };
const task = {
  ...template,
  id: 7,
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

test("task and schedule payloads use null for Primary and the id for an explicit workspace", () => {
  const taskState = { notes: "", attachmentIds: [], timed: null, startNow: false, tree: undefined, treeStart: "inherit" };
  const scheduleState = { notes: "", attachmentIds: [], cron: "0 9 * * *", tz: "UTC" };
  for (const bindingId of [null, 42]) {
    const placement = { ...template, bindingId };
    assert.equal(taskCreateBody(placement, taskState).bindingId, bindingId);
    assert.equal(scheduleBody(placement, scheduleState).bindingId, bindingId);
  }
});

test("task edits preserve the saved workspace until it is changed or cleared", () => {
  const before = { ...task, bindingId: 42 };
  const titleOnly = { ...template, bindingId: 42, title: "Renamed" };
  assert.deepEqual(taskUpdateBody(update({ before, template: titleOnly })), { title: "Renamed" });
  assert.deepEqual(taskUpdateBody(update({ before, template })), { bindingId: null });
  assert.deepEqual(taskUpdateBody(update({ before, template: { ...template, bindingId: 43 } })), { bindingId: 43 });
  assert.deepEqual(taskUpdateBody(update({ before, template: { ...template, projectId: 2 } })), {
    projectId: 2,
    bindingId: null,
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

test("workspace placement chooses the primary by default and scopes explicit choices to the live connection", () => {
  const primary = { id: 42, connectionId: 3, machineId: 1, connectionActive: true, primary: true, status: "available", workspace: "/project" };
  const alternate = { ...primary, id: 43, primary: false, workspace: "/review", revision: 4 };
  const otherConnection = { ...primary, id: 44, connectionId: 4 };
  const retired = { ...primary, id: 45, connectionActive: false };
  const bindings = [alternate, otherConnection, retired, primary];

  const defaultPlacement = taskWorkspaceSelection(bindings, 3, null);
  assert.deepEqual(defaultPlacement.choices, [primary, alternate]);
  assert.equal(defaultPlacement.available, primary);
  assert.equal(defaultPlacement.issue, null);
  assert.equal(taskWorkspaceSelection(bindings, 3, 43).available, alternate);
  for (const id of [44, 45]) {
    const placement = taskWorkspaceSelection(bindings, 3, id);
    assert.equal(placement.available, undefined);
    assert.match(placement.issue, /no longer available/);
  }
});

test("an unavailable explicit choice requires repair while a missing primary leaves the project unbound", () => {
  const primary = { id: 42, connectionId: 3, machineId: 1, connectionActive: true, primary: true, status: "missing", workspace: "/project" };
  const changed = { ...primary, id: 43, primary: false, status: "changed" };
  const bindings = [primary, changed];

  const defaultPlacement = taskWorkspaceSelection(bindings, 3, null);
  assert.deepEqual(defaultPlacement.choices, []);
  assert.equal(defaultPlacement.available, undefined);
  assert.equal(defaultPlacement.issue, null);
  assert.match(taskWorkspaceSelection(bindings, 3, 42).issue, /no longer reports/);
  assert.match(taskWorkspaceSelection(bindings, 3, 43).issue, /changed/);
  assert.match(taskWorkspaceSelection(bindings, 3, 404).issue, /no longer available/);
});

test("task workspace display keeps the recorded path or pathless external identity", () => {
  const workspace = { bindingId: 42, path: "/original", externalId: "checkout-2", source: "pinned" };
  assert.equal(taskWorkspaceLocation(workspace), "/original");
  assert.equal(taskWorkspaceLocation({ ...workspace, path: "" }), "checkout-2");
  assert.equal(taskWorkspaceLocation({ bindingId: 42, path: "", source: "pinned" }), "Binding #42");
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
