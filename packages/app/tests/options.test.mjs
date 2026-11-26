import { test } from "node:test";
import assert from "node:assert/strict";
import { incompatibleSettings, optionAccepts, optionApplies, optionKind, optionLabel, optionValue, setOption, shownOptions } from "../src/lib/options.ts";

// A harness that declares a worktree with a base ref and branch, as T3 does; HarnessFields renders them by kind.
const worktree = { id: "worktree", kind: "boolean", label: "New worktree", values: [true, false], default: false };
const baseRef = { id: "baseRef", kind: "text", label: "Base ref", values: [], default: null, dependsOn: { option: "worktree", value: true } };
const branch = { id: "branch", kind: "text", label: "Branch", values: [], dependsOn: { option: "worktree", value: true } };
const effort = { id: "effort", kind: "choice", label: "Effort", values: ["low", "high"] };
const all = [worktree, baseRef, branch, effort];
const config = (options, model = "m") => ({ model, options, permission: "", mode: "" });

test("kind: declared, else boolean when every value is boolean, else choice", () => {
  assert.equal(optionKind(baseRef), "text");
  assert.equal(optionKind({ values: [true, false] }), "boolean");
  assert.equal(optionKind({ values: ["low", true] }), "choice");
  assert.equal(optionKind({ values: [] }), "choice");
});

test("label: the harness's words, else the id", () => {
  assert.equal(optionLabel(worktree), "New worktree");
  assert.equal(optionLabel({ id: "fast" }), "fast");
  assert.equal(optionLabel({ id: "fast", label: "" }), "fast");
});

test("value: the selection, else the default", () => {
  assert.equal(optionValue(worktree, []), false);
  assert.equal(optionValue(worktree, [{ id: "worktree", value: true }]), true);
  assert.equal(optionValue(baseRef, []), undefined);
});

test("dependsOn: an option applies only while its dependency has the value, selected or by default", () => {
  assert.equal(optionApplies(effort, [], all), true);
  assert.equal(optionApplies(baseRef, [], all), false, "worktree defaults to off");
  assert.equal(optionApplies(baseRef, [{ id: "worktree", value: true }], all), true);
  assert.equal(optionApplies(baseRef, [{ id: "worktree", value: false }], all), false);
  assert.equal(optionApplies(baseRef, [{ id: "worktree", value: true }], [baseRef]), false, "an undeclared dependency never applies");
  const onByDefault = { ...worktree, default: true };
  assert.equal(optionApplies(branch, [], [onByDefault, branch]), true);
});

test("accepts: a choice's values, booleans, non-empty text", () => {
  assert.equal(optionAccepts(effort, "low"), true);
  assert.equal(optionAccepts(effort, "max"), false);
  assert.equal(optionAccepts(worktree, true), true);
  assert.equal(optionAccepts(worktree, "true"), false);
  assert.equal(optionAccepts(baseRef, "origin/main"), true);
  assert.equal(optionAccepts(baseRef, "  "), false);
  assert.equal(optionAccepts(baseRef, true), false);
  assert.equal(optionAccepts({ id: "fast", values: [true, false] }, false), true, "an inferred boolean");
});

test("setOption trims text, clears empty text, and drops options whose dependency turned off", () => {
  let sel = setOption([], all, "worktree", true);
  assert.deepEqual(sel, [{ id: "worktree", value: true }]);
  sel = setOption(sel, all, "baseRef", "  origin/main ");
  sel = setOption(sel, all, "branch", "feat/x");
  sel = setOption(sel, all, "effort", "high");
  assert.deepEqual(sel, [
    { id: "worktree", value: true },
    { id: "baseRef", value: "origin/main" },
    { id: "branch", value: "feat/x" },
    { id: "effort", value: "high" },
  ]);
  assert.deepEqual(setOption(sel, all, "branch", ""), [
    { id: "worktree", value: true },
    { id: "baseRef", value: "origin/main" },
    { id: "effort", value: "high" },
  ]);
  assert.deepEqual(setOption(sel, all, "worktree", false), [
    { id: "effort", value: "high" },
    { id: "worktree", value: false },
  ]);
  assert.deepEqual(setOption(sel, all, "worktree", undefined), [{ id: "effort", value: "high" }], "cleared: the default (off) applies");
});

test("setOption keeps options the harness does not declare (the form offers to clear them)", () => {
  assert.deepEqual(setOption([{ id: "legacy", value: "x" }], all, "effort", "low"), [
    { id: "legacy", value: "x" },
    { id: "effort", value: "low" },
  ]);
});

test("shownOptions: what HarnessFields renders, in order; text and branch appear once worktree is on", () => {
  assert.deepEqual(shownOptions(all, []).map((o) => o.id), ["worktree", "effort"]);
  assert.deepEqual(shownOptions(all, [{ id: "worktree", value: true }]).map((o) => o.id), ["worktree", "baseRef", "branch", "effort"]);
});

test("incompatibleSettings flags undeclared options, values an option does not take, and unmet dependencies", () => {
  const caps = { options: [worktree, baseRef, branch], operations: {}, models: [{ id: "m", label: "M", available: true, group: "", options: [effort], permissions: null }], modes: [] };
  assert.deepEqual(incompatibleSettings(config([{ id: "worktree", value: true }, { id: "baseRef", value: "origin/main" }, { id: "effort", value: "low" }]), caps), []);
  assert.deepEqual(incompatibleSettings(config([{ id: "baseRef", value: "origin/main" }]), caps), ["baseRef"]);
  assert.deepEqual(incompatibleSettings(config([{ id: "worktree", value: "yes" }]), caps), ["worktree"]);
  assert.deepEqual(incompatibleSettings(config([{ id: "effort", value: "low" }], ""), caps), ["effort"], "a model's option without its model");
  assert.deepEqual(incompatibleSettings(config([{ id: "sandbox", value: "x" }]), caps), ["sandbox"]);
  assert.deepEqual(incompatibleSettings(config([]), undefined), []);
});
