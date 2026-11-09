import assert from "node:assert/strict";
import { test } from "node:test";
import { staleTags } from "../src/lib/push.ts";

test("closes task/thread notifications whose group was viewed on any device", () => {
  const stale = staleTags(["task-1", "thread-abc", "task-2", "thread-xyz"], new Set(["task-2", "thread-xyz"]));
  assert.deepEqual([...stale].toSorted(), ["task-1", "thread-abc"]);
});

test("keeps unread groups, foreign tags and untagged notifications", () => {
  const stale = staleTags(["task-1", "test", null, "other-1", "tasks-1"], new Set(["task-1"]));
  assert.deepEqual([...stale], []);
});

test("nothing unread closes every shown task/thread notification once", () => {
  const stale = staleTags(["task-1", "task-1", "thread-a"], new Set());
  assert.deepEqual([...stale].toSorted(), ["task-1", "thread-a"]);
});
