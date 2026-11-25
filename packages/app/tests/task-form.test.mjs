import assert from "node:assert/strict";
import { test } from "node:test";
import { blockedStartModes, formWorkspace, scheduleBody, taskCreateBody, taskUpdateBody } from "../src/lib/taskForm.ts";

const config = { model: "m", options: [], permission: "", mode: "" };
const template = { projectId: 1, title: "T", prompt: "P", connectionId: 3, executionConfig: config, workspace: { type: "root" } };
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

test("formWorkspace leaves base and branch to a harness that picks its own worktree", () => {
  assert.deepEqual(formWorkspace("worktree", "origin/main", "feat", null, true), { type: "worktree", baseRef: "", branch: null });
  assert.deepEqual(formWorkspace("root", "origin/main", "", null, false), { type: "root" });
});

test("formWorkspace sends a predecessor base without a ref or branch to a harness that names its worktree", () => {
  assert.deepEqual(formWorkspace("predecessor", "origin/main", "feat", 12, true), {
    type: "worktree",
    baseRef: "",
    branch: null,
    base: "predecessor",
    baseTaskId: 12,
  });
  assert.deepEqual(formWorkspace("predecessor", "origin/main", "feat", 12, false), {
    type: "worktree",
    baseRef: "origin/main",
    branch: "feat",
    base: "predecessor",
    baseTaskId: 12,
  });
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
