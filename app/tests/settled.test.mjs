import assert from "node:assert/strict";
import { test } from "node:test";
import { duration } from "../src/lib/format.ts";
import { mergeSettled } from "../src/lib/settled.ts";

const at = (s) => new Date(Date.UTC(2026, 10, 10) + s * 1000).toISOString();

test("duration formats elapsed spans compactly", () => {
  assert.equal(duration(at(0), at(45)), "45s");
  assert.equal(duration(at(0), at(8 * 60 + 59)), "8m");
  assert.equal(duration(at(0), at(3600)), "1h");
  assert.equal(duration(at(0), at(3600 + 12 * 60)), "1h 12m");
  assert.equal(duration(at(0), at(2 * 86400)), "2d");
  assert.equal(duration(at(0), at(2 * 86400 + 3 * 3600 + 59)), "2d 3h");
});

test("duration is null without both ends or for reversed spans", () => {
  assert.equal(duration(null, "2026-11-10T00:00:00Z"), null);
  assert.equal(duration("2026-11-10T00:00:00Z", null), null);
  assert.equal(duration("2026-11-10T01:00:00Z", "2026-11-10T00:00:00Z"), null);
  assert.equal(duration("nope", "2026-11-10T00:00:00Z"), null);
});

const task = (id, settledAt) => ({ id, settledAt });
const thread = (t3ThreadId, settledAt) => ({ t3ThreadId, settledAt });

test("settled tasks and threads interleave newest settle first", () => {
  const merged = mergeSettled(
    [task(1, "2026-11-10T01:00:00Z"), task(2, "2026-11-10T04:00:00Z"), task(3, null)],
    [thread("a", "2026-11-10T03:00:00Z"), thread("b", "2026-11-10T00:30:00Z")],
  );
  assert.deepEqual(
    merged.map((i) => (i.kind === "task" ? `task ${i.task.id}` : `thread ${i.thread.t3ThreadId}`)),
    ["task 2", "thread a", "task 1", "thread b", "task 3"],
  );
});

test("completed trees interleave with settled tasks and threads", () => {
  const merged = mergeSettled(
    [task(1, "2026-11-10T01:00:00Z")],
    [thread("a", "2026-11-10T03:00:00Z")],
    [{ id: 7, settledAt: "2026-11-10T02:00:00Z" }],
  );
  assert.deepEqual(
    merged.map((i) => (i.kind === "task" ? i.task.id : i.kind === "thread" ? i.thread.t3ThreadId : `tree ${i.tree.id}`)),
    ["a", "tree 7", 1],
  );
});
