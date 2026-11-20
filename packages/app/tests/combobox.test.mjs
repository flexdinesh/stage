import assert from "node:assert/strict";
import { test } from "node:test";
import { edgeIndex, filterOptions, stepIndex, typeaheadIndex } from "../src/lib/combobox.ts";

const models = [
  { value: "", label: "Harness default" },
  { value: "gpt-sol", label: "GPT-6.2 Sol", tag: "openai" },
  { value: "neon", label: "Gemini 4 Neon", tag: "github-copilot" },
  { value: "copilot-sol", label: "GPT-6.2 Sol", tag: "github-copilot" },
  { value: "mai", label: "MAI Code 2", tag: "github-copilot", detail: "fast tier" },
];
const values = (list) => list.map((o) => o.value);

test("empty or blank query lists every option in the given order without spans", () => {
  assert.deepEqual(values(filterOptions(models, "")), ["", "gpt-sol", "neon", "copilot-sol", "mai"]);
  assert.deepEqual(values(filterOptions(models, "  ")), values(models));
  assert.ok(filterOptions(models, "").every((o) => o.labelMatch === undefined));
});

test("ranks label prefix, label substring, tag, then detail; ties keep the given order", () => {
  // "gpt" prefixes two labels; "co" prefixes nothing, is inside "MAI Code 2" and in github-copilot tags.
  assert.deepEqual(values(filterOptions(models, "GPT")), ["gpt-sol", "copilot-sol"]);
  assert.deepEqual(values(filterOptions(models, "co")), ["mai", "neon", "copilot-sol"]);
  assert.deepEqual(values(filterOptions(models, "tier")), ["mai"]);
  assert.deepEqual(filterOptions(models, "zzz"), []);
});

test("reports matched spans in label, tag and detail", () => {
  const [neon] = filterOptions(models, "neon");
  assert.deepEqual(neon.labelMatch, { start: 9, end: 13 });
  assert.equal(neon.tagMatch, null);
  const [byTag] = filterOptions(models, "openai");
  assert.equal(byTag.labelMatch, null);
  assert.deepEqual(byTag.tagMatch, { start: 0, end: 6 });
  const [byDetail] = filterOptions(models, "tier");
  assert.deepEqual(byDetail.detailMatch, { start: 5, end: 9 });
});

const days = [
  { value: "1", label: "Monday" },
  { value: "2", label: "Tuesday" },
  { value: "3", label: "Wednesday" },
  { value: "4", label: "Thursday", disabled: true },
  { value: "5", label: "Friday" },
  { value: "6", label: "Saturday" },
  { value: "0", label: "Sunday" },
];

test("type-ahead finds the next option starting with a single key, wrapping", () => {
  assert.equal(typeaheadIndex(days, "s", -1), 5);
  assert.equal(typeaheadIndex(days, "s", 5), 6);
  assert.equal(typeaheadIndex(days, "S", 6), 5);
  assert.equal(typeaheadIndex(days, "x", 0), -1);
  assert.equal(typeaheadIndex([], "s", 0), -1);
});

test("type-ahead matches a typed prefix from the current option and skips disabled ones", () => {
  assert.equal(typeaheadIndex(days, "su", 5), 6);
  assert.equal(typeaheadIndex(days, "sa", 5), 5);
  // Thursday is disabled: "t" from Tuesday wraps past it to Tuesday again.
  assert.equal(typeaheadIndex(days, "t", 1), 1);
  assert.equal(typeaheadIndex(days, "th", 0), -1);
});

test("repeating one key cycles through options starting with it", () => {
  assert.equal(typeaheadIndex(days, "ss", 5), 6);
  assert.equal(typeaheadIndex(days, "sss", 6), 5);
});

test("arrow steps and Home/End skip disabled options", () => {
  assert.equal(stepIndex(days, 2, 1), 4);
  assert.equal(stepIndex(days, 4, -1), 2);
  assert.equal(stepIndex(days, 6, 1), 0);
  assert.equal(stepIndex(days, 0, -1), 6);
  const one = [{ value: "a", label: "A" }, { value: "b", label: "B", disabled: true }];
  assert.equal(stepIndex(one, 0, 1), 0);
  assert.equal(edgeIndex(days, "first"), 0);
  assert.equal(edgeIndex([{ value: "x", label: "X", disabled: true }, ...days], "first"), 1);
  assert.equal(edgeIndex(days, "last"), 6);
  assert.equal(edgeIndex([{ value: "x", label: "X", disabled: true }], "last"), -1);
});
