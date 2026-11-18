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

const task = (id, settledAt) => ({ id, completedAt: settledAt });
test("completed trees interleave with settled tasks", () => {
  const merged = mergeSettled(
    [task(1, "2026-11-10T01:00:00Z")],
    [{ id: 7, settledAt: "2026-11-10T02:00:00Z" }],
  );
  assert.deepEqual(
    merged.map((i) => (i.kind === "task" ? i.task.id : `tree ${i.tree.id}`)),
    ["tree 7", 1],
  );
});
