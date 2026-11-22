import assert from "node:assert/strict";
import { test } from "node:test";
import { changedFields, sameValue } from "../src/lib/changes.ts";
import { taskEditBaseline, taskEditChanges } from "../src/lib/tasks.ts";

test("sameValue compares JSON-like values structurally", () => {
  assert.equal(sameValue({ a: 1, b: [1, { c: "x" }] }, { b: [1, { c: "x" }], a: 1 }), true);
  assert.equal(sameValue({ a: 1, b: undefined }, { a: 1 }), true);
  assert.equal(sameValue([1, 2], [2, 1]), false);
  assert.equal(sameValue({ a: null }, { a: undefined }), false);
  assert.equal(sameValue("1", 1), false);
});

test("changedFields keeps only what differs from the baseline", () => {
  assert.deepEqual(changedFields({ title: "a", prompt: "p", n: 1 }, { title: "b", prompt: "p", n: 1 }), { title: "b" });
  assert.deepEqual(changedFields({ title: "a" }, { title: "a" }), {});
});

const task = {
  id: 7,
  projectId: 1,
  title: "Fix the bug",
  prompt: "Do it",
  connectionId: 3,
  executionConfig: { model: "m", options: [], permission: "", mode: "" },
  workspace: { type: "local" },
  attachments: [{ id: 11 }],
};

test("an untouched field is not sent, so a concurrent server change survives", () => {
  // Form opened on `task`; an agent then changed the prompt on the server. The user edits only the title.
  const next = { ...taskEditBaseline(task), title: "Fix the bug quickly" };
  assert.deepEqual(taskEditChanges(taskEditBaseline(task), next), { title: "Fix the bug quickly" });
});

test("a missing options list compares equal to an empty one", () => {
  const legacy = { ...task, executionConfig: { model: "m", permission: "", mode: "" } };
  const next = { ...taskEditBaseline(task) };
  assert.deepEqual(taskEditChanges(taskEditBaseline(legacy), next), {});
});

test("a new harness or project also carries the config and workspace the server checks against it", () => {
  const next = { ...taskEditBaseline(task), connectionId: 4 };
  assert.deepEqual(taskEditChanges(taskEditBaseline(task), next), {
    connectionId: 4,
    executionConfig: task.executionConfig,
    workspace: task.workspace,
  });
});

test("a changed image set is sent as the exact set", () => {
  const next = { ...taskEditBaseline(task), attachmentIds: [11, 12] };
  assert.deepEqual(taskEditChanges(taskEditBaseline(task), next), { attachmentIds: [11, 12] });
});
