import assert from "node:assert/strict";
import { test } from "node:test";
import { statusGroups, statusRow } from "../src/lib/status.ts";

const item = (key, tone, label, group = "Harnesses") => ({ key, group, name: key, tone, state: tone, label });
const opts = { checking: false, empty: "Connect a harness" };

test("all healthy reads All connected", () => {
  const row = statusRow([item("T3", "ok", "T3 connected"), item("OpenCode", "ok", "OpenCode connected")], opts);
  assert.deepEqual([row.tone, row.text, row.problems], ["ok", "All connected", 0]);
});

test("one problem names itself", () => {
  const row = statusRow([item("T3", "ok", "T3 connected"), item("OpenCode", "need", "OpenCode expired")], opts);
  assert.deepEqual([row.tone, row.text], ["need", "OpenCode expired"]);
  assert.deepEqual(
    row.items.map((i) => i.key),
    ["OpenCode", "T3"],
  );
});

test("several problems count, the worst tone wins and sorts first", () => {
  const row = statusRow(
    [item("T3", "need", "T3 pairing expired"), item("OpenCode", "bad", "OpenCode unavailable"), item("w", "ok", "w online", "Workers")],
    opts,
  );
  assert.deepEqual([row.tone, row.text, row.problems], ["bad", "2 need attention", 2]);
  assert.deepEqual(
    row.items.map((i) => i.key),
    ["OpenCode", "T3", "w"],
  );
});

test("nothing set up asks to connect; loading reads Checking", () => {
  assert.deepEqual(
    [statusRow([], opts).tone, statusRow([], opts).text],
    ["idle", "Connect a harness"],
  );
  assert.equal(statusRow([], { ...opts, checking: true }).text, "Checking");
});

test("an item still checking holds the row at Checking unless something is wrong", () => {
  assert.deepEqual(
    (({ tone, text }) => [tone, text])(statusRow([item("T3", "ok", "T3 connected"), item("OpenCode", "idle", "Checking OpenCode")], opts)),
    ["idle", "Checking"],
  );
  assert.equal(statusRow([item("T3", "bad", "T3 offline"), item("OpenCode", "idle", "Checking OpenCode")], opts).text, "T3 offline");
});

test("groups keep first-seen order", () => {
  const groups = statusGroups([item("a", "ok", "a"), item("w", "ok", "w", "Workers"), item("b", "ok", "b")]);
  assert.deepEqual(
    groups.map((g) => [g.group, g.items.length]),
    [
      ["Harnesses", 2],
      ["Workers", 1],
    ],
  );
});
