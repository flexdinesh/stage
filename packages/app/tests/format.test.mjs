import assert from "node:assert/strict";
import { test } from "node:test";
import { age, capCount, clockTime, configModelText, dayLabel, executionModes, groupByDay, modelName, shortDate } from "../src/lib/format.ts";

// Local times, so the expectations hold in any TZ.
const now = new Date(2026, 10, 16, 15, 0).getTime();
const at = (y, mo, d, h = 12, mi = 0) => new Date(y, mo, d, h, mi).toISOString();

test("shortDate anchors today, yesterday, this year, and other years", () => {
  assert.equal(shortDate(at(2026, 10, 16, 10, 20), now), "today 10:20");
  assert.equal(shortDate(at(2026, 10, 15, 16, 5), now), "yesterday 16:05");
  assert.equal(shortDate(at(2026, 10, 14), now), "Nov 14");
  assert.equal(shortDate(at(2025, 9, 3), now), "Oct 3, 2025");
  assert.equal(shortDate(null, now), "-");
  assert.equal(shortDate("nope", now), "-");
});

test("clockTime is a local 24h HH:MM", () => {
  assert.equal(clockTime(at(2026, 10, 16, 9, 5)), "09:05");
  assert.equal(clockTime(at(2026, 10, 16, 23, 59)), "23:59");
  assert.equal(clockTime(null), "-");
});

test("age is the largest unit only", () => {
  assert.equal(age(new Date(now - 45_000).toISOString(), now), "45s");
  assert.equal(age(new Date(now - 18 * 60_000).toISOString(), now), "18m");
  assert.equal(age(new Date(now - 50 * 3_600_000).toISOString(), now), "2d");
  assert.equal(age(new Date(now + 60_000).toISOString(), now), "now");
  assert.equal(age(null, now), "-");
});

test("dayLabel names today and yesterday, adds the year when not this year", () => {
  assert.equal(dayLabel(at(2026, 10, 16), now), "Today · Mon Nov 16");
  assert.equal(dayLabel(at(2026, 10, 15), now), "Yesterday · Sun Nov 15");
  assert.equal(dayLabel(at(2026, 10, 14), now), "Sat Nov 14");
  assert.equal(dayLabel(at(2025, 9, 3), now), "Fri Oct 3, 2025");
});

test("groupByDay keeps order and splits on local day changes", () => {
  const items = [
    { id: 1, at: at(2026, 10, 16, 14) },
    { id: 2, at: at(2026, 10, 16, 9) },
    { id: 3, at: at(2026, 10, 15, 17) },
    { id: 4, at: at(2026, 10, 14, 11) },
    { id: 5, at: at(2026, 10, 14, 9) },
  ];
  assert.deepEqual(
    groupByDay(items, now).map((g) => [g.label, g.items.map((i) => i.id)]),
    [
      ["Today · Mon Nov 16", [1, 2]],
      ["Yesterday · Sun Nov 15", [3]],
      ["Sat Nov 14", [4, 5]],
    ],
  );
  assert.deepEqual(groupByDay([], now), []);
});

test("executionModes reads executionConfig; none or empty is the harness default", () => {
  assert.deepEqual(executionModes({ executionConfig: null }), { permission: "", mode: "" });
  assert.deepEqual(executionModes({}), { permission: "", mode: "" });
  const config = { model: "", options: [], permission: "", mode: "" };
  assert.deepEqual(executionModes({ executionConfig: config }), { permission: "", mode: "" });
  assert.deepEqual(executionModes({ executionConfig: { ...config, permission: "auto", mode: "plan" } }), { permission: "auto", mode: "plan" });
});

test("model text names a composite key by its last part and an empty model as the harness default", () => {
  assert.equal(modelName(""), null);
  assert.equal(modelName("anthropic/claude"), "anthropic/claude");
  assert.equal(modelName('["codex","gpt-5"]'), "gpt-5");
  assert.equal(modelName("[not json"), "[not json");
  assert.equal(configModelText(null), "Harness default");
  assert.equal(configModelText({ model: '["codex","gpt-5"]', options: [{ id: "effort", value: "high" }], permission: "", mode: "" }), "gpt-5 · high");
});

test("capCount caps a badge at 99+", () => {
  assert.equal(capCount(0), "0");
  assert.equal(capCount(7), "7");
  assert.equal(capCount(99), "99");
  assert.equal(capCount(100), "99+");
  assert.equal(capCount(1234), "99+");
});
