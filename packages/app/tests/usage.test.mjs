import assert from "node:assert/strict";
import { test } from "node:test";
import { compactCount, usageRows, workingTime } from "../src/lib/usage.ts";

const usage = (extra = {}) => ({
  turns: 1,
  inputTokens: 2,
  cacheReadTokens: 27618,
  cacheWriteTokens: 16652,
  outputTokens: 4875,
  workingMs: 23015,
  costUsd: null,
  ...extra,
});

test("compactCount keeps small counts and rounds larger ones to one decimal", () => {
  assert.equal(compactCount(0), "0");
  assert.equal(compactCount(999), "999");
  assert.equal(compactCount(1000), "1k");
  assert.equal(compactCount(4875), "4.9k");
  assert.equal(compactCount(999_949), "999.9k");
  assert.equal(compactCount(999_950), "1M");
  assert.equal(compactCount(1_260_000), "1.3M");
});

test("workingTime shows the two largest units", () => {
  assert.equal(workingTime(400), "0s");
  assert.equal(workingTime(23_015), "23s");
  assert.equal(workingTime(252_000), "4m 12s");
  assert.equal(workingTime(3_780_000), "1h 3m");
});

test("rows: input counts every input token and splits out the cache, with exact counts in the tooltip", () => {
  assert.deepEqual(usageRows(usage()), [
    { label: "Turns", value: "1" },
    { label: "Input tokens", value: "44.3k", exact: "44,272 tokens", detail: ["27.6k cache read", "16.7k cache write"] },
    { label: "Output tokens", value: "4.9k", exact: "4,875 tokens" },
    { label: "Working time", value: "23s" },
  ]);
});

test("rows: no cache line without cache tokens, no working time before a turn ends, no cost unless reported", () => {
  const rows = usageRows(usage({ turns: 2, inputTokens: 120, cacheReadTokens: 0, cacheWriteTokens: 0, outputTokens: 0, workingMs: 0 }));
  assert.deepEqual(
    rows.map((r) => r.label),
    ["Turns", "Input tokens", "Output tokens"],
  );
  assert.equal(rows[1].detail, undefined);
  assert.deepEqual(usageRows(usage({ cacheWriteTokens: 0 }))[1].detail, ["27.6k cache read"]);
});

test("rows: a harness-reported cost shows to the cent", () => {
  const cost = usageRows(usage({ costUsd: 1.234 })).at(-1);
  assert.deepEqual(cost, { label: "Cost", value: "$1.23", exact: "$1.234" });
});
